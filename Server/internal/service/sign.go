package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/facephoto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/observe"
	"xbt2/server/internal/xxt"
)

type signClient interface {
	PreSign(mobile, password string, fixed xxt.FixedParams, signType int, code, enc string) (xxt.PreSignResult, error)
	Sign(mobile, password string, fixed xxt.FixedParams, signType int, special map[string]interface{}) (xxt.SignResult, error)
	GetSignDetail(mobile, password string, activityID, courseID, classID, uid int64) (xxt.SignDetail, error)
	HasActivityInCourse(mobile, password string, courseID, classID, activityID int64) (bool, error)
	UploadPanFile(mobile, password, filename, contentType string, data []byte) (string, error)
	UploadFaceTemp(mobile, password, filename, contentType string, data []byte) (string, error)
	CheckFaceResult(mobile, password string, activeID int64, faceObjectID, clientID string) (xxt.FaceCheckResult, error)
}

type SignService struct {
	db                  *gorm.DB
	xxt                 signClient
	cc                  *CredentialCrypto
	courseWebhook       *EnterpriseWechatWebhookNotifier
	courseNotifyMu      sync.Mutex
	courseNotifyBatches map[courseSignNotificationKey]*courseSignNotificationBatch
	// faceUploads deduplicates face_temp uploads: the same account and photo
	// upload once per activity, then every check reuses the objectId.
	faceUploadMu    sync.Mutex
	faceUploads     map[faceUploadKey]faceUploadEntry
	faceUploadGroup singleflight.Group
	// faceImportGroup coalesces concurrent empty-pool imports for the same
	// account, so one fetch is shared instead of storing duplicate bases.
	faceImportGroup singleflight.Group
	// faceBaseRefreshGroup coalesces legacy official base refreshes per
	// account. faceBaseRefreshes bounds them to one attempt per cooldown; a
	// failed attempt also waits out the cooldown so signing never loops on it.
	faceBaseRefreshGroup singleflight.Group
	faceBaseRefreshMu    sync.Mutex
	faceBaseRefreshes    map[int64]time.Time
	// sampleLocation is replaceable in tests. Production uses the uniform
	// sampler from xxt with the global random source. It only serves requests
	// without an explicit selection.
	sampleLocation func(center xxt.GeoPoint, radiusMeters float64) xxt.GeoPoint
	// sampleJitter randomizes an explicitly selected point. It is a separate
	// seam because the invariant differs: the offset is bounded by
	// locationJitterMeters and is never zero (XBT-S26).
	sampleJitter func(center xxt.GeoPoint, radiusMeters float64) xxt.GeoPoint
	// locationJitterMeters bounds the offset applied around a caller-selected
	// point. It never affects auto mode (XBT-S26).
	locationJitterMeters float64
	// facePhotoRand is replaceable in tests so pool selection and per-execution
	// transforms are assertable.
	facePhotoRand facephoto.RandSource
	// faceTransformMild selects the diagnostic mild transform preset
	// (OMP-58). It never disables the transform and defaults to false.
	faceTransformMild bool
	// faceUploadMode selects the upload shape for diagnostic A/B testing:
	// face_temp (default) or generic (UploadPanFile). Config-validated.
	faceUploadMode string
	// facePhotos is the on-demand official photo importer. A nil importer
	// leaves accounts without a pool entry unable to sign with a face.
	facePhotos FacePhotoImporter
	// faceDevice fetches clientId when storage cannot provide a signature. It
	// is nil when the upstream client does not implement FaceDeviceClient.
	faceDevice FaceDeviceClient
	// faceClientIDMu guards the per-account failure cooldown. A failed backfill
	// must not hit upstream on every subsequent sign.
	faceClientIDMu       sync.Mutex
	faceClientIDFailures map[int64]time.Time
	metrics              *observe.SignMetrics
}

// signExecutionStats collects the fields that must appear in the single
// result line. It is written only by the request goroutine.
type signExecutionStats struct {
	attempts          int
	upstreamStatus    int
	validateTriggered bool
	captchaPassed     bool
	locationError     bool
	upstream403       bool
}

func (s *signExecutionStats) record(result xxt.SignResult) {
	if result.Attempts > 0 {
		s.attempts += result.Attempts
	} else {
		s.attempts++
	}
	s.upstreamStatus = result.HTTPStatus
	if result.Kind == xxt.SignResultCaptchaRequired || result.CaptchaAttempted {
		s.validateTriggered = true
	}
	if result.CaptchaPassed {
		s.captchaPassed = true
	}
	if result.Kind == xxt.SignResultLocationOutOfRange {
		s.locationError = true
	}
	if result.HTTPStatus == 403 {
		s.upstream403 = true
	}
}

func NewSignService(db *gorm.DB, xxtClient signClient, cc *CredentialCrypto, courseWebhook *EnterpriseWechatWebhookNotifier) *SignService {
	svc := &SignService{
		db:                   db,
		xxt:                  xxtClient,
		cc:                   cc,
		courseWebhook:        courseWebhook,
		courseNotifyBatches:  map[courseSignNotificationKey]*courseSignNotificationBatch{},
		faceUploads:          map[faceUploadKey]faceUploadEntry{},
		faceClientIDFailures: map[int64]time.Time{},
		metrics:              observe.NewSignMetrics(),
		locationJitterMeters: defaultLocationJitterMeters,
		sampleLocation: func(center xxt.GeoPoint, radiusMeters float64) xxt.GeoPoint {
			position := xxt.SampleLocation(center, radiusMeters, nil)
			return xxt.GeoPoint{Latitude: position.Latitude, Longitude: position.Longitude}
		},
		sampleJitter: func(center xxt.GeoPoint, radiusMeters float64) xxt.GeoPoint {
			position := xxt.SampleLocationJitter(center, radiusMeters, nil)
			return xxt.GeoPoint{Latitude: position.Latitude, Longitude: position.Longitude}
		},
	}
	// *xxt.Client satisfies FaceDeviceClient; narrower test doubles may not.
	if lookup, ok := xxtClient.(FaceDeviceClient); ok {
		svc.faceDevice = lookup
	}
	return svc
}

const courseSignNotificationDebounce = 8 * time.Second

// defaultLocationJitterMeters is the offset bound applied around an explicitly
// selected point (XBT-S26). Auto mode uses the activity radius instead.
const defaultLocationJitterMeters = 5.0

// FacePhotoImporter fetches and stores an account's official photo as a base
// image. *FacePhotoService implements it; tests may inject a narrower stub.
type FacePhotoImporter interface {
	ImportOfficial(ctx context.Context, operatorUID, targetUID int64) (model.FacePhoto, error)
	// RefreshOfficialBase re-downloads one legacy official base and stores it
	// as an untouched original (OMP-59). It never touches upload sources.
	RefreshOfficialBase(ctx context.Context, targetUID int64, photoID uint) (model.FacePhoto, error)
}

// SetFacePhotoService enables the sign-time fallback that imports the official
// photo when an account has no pool entry yet.
func (s *SignService) SetFacePhotoService(importer FacePhotoImporter) {
	s.facePhotos = importer
}

// SetRand installs the randomness used for pool selection and the transform
// that runs inside every execution. Tests inject a deterministic source;
// production leaves it nil (time-seeded).
func (s *SignService) SetRand(rnd facephoto.RandSource) { s.facePhotoRand = rnd }

const (
	faceUploadModeFaceTemp = "face_temp"
	faceUploadModeGeneric  = "generic"
)

// SetFaceDiagnostics installs the OMP-58 A/B switches. Both are diagnostics:
// mild keeps a transform (99-100% crop, 0 degrees, q92) and generic keeps the
// mandatory transform while changing only the upload endpoint shape. Unknown
// upload modes fall back to face_temp so a hand-edited config cannot silently
// change production behavior; config.Load rejects them before startup.
func (s *SignService) SetFaceDiagnostics(mild bool, uploadMode string) {
	s.faceTransformMild = mild
	if uploadMode == faceUploadModeGeneric {
		s.faceUploadMode = faceUploadModeGeneric
		return
	}
	s.faceUploadMode = faceUploadModeFaceTemp
}

type faceTransformInfo struct {
	CropWidthRatio  float64
	CropHeightRatio float64
	AngleDegrees    float64
	Quality         int
}

// SetLocationJitterMeters overrides the jitter bound used for explicitly
// selected points. Non-positive values keep the default so a misconfigured
// config.yaml cannot degrade into exact-point submission (XBT-S26).
func (s *SignService) SetLocationJitterMeters(meters float64) {
	if meters > 0 {
		s.locationJitterMeters = meters
	}
}

type courseSignNotificationKey struct {
	ActivityID int64
	CourseID   int64
	ClassID    int64
	SignType   int
}

type courseSignNotificationBatch struct {
	Records []model.SignRecord
	Version int
	Timer   *time.Timer
}

type ExecuteSignRequest struct {
	ActivityID    int64
	TargetUID     int64
	SignType      int
	CourseID      int64
	ClassID       int64
	IfRefreshEWM  bool
	IfOpenAddress bool
	ActivityName  string
	CourseName    string
	CourseTeacher string
	Special       map[string]interface{}
	// Context scopes on-demand upstream work (clientId backfill) to the
	// caller's request. A nil Context means context.Background().
	Context context.Context
	// locationBase is the unrandomized point used for the single
	// errorLocation_ retry. It is set by ExecuteOne, never by handlers.
	locationBase *xxt.SignPosition
	// locationProvided records whether this execution carried usable
	// coordinates: a point sampled from activity metadata or a caller-selected
	// point. signFailure uses it to tell a missing point (location_point_required)
	// from a genuinely out-of-range one (XBT-S24).
	locationProvided bool
	// faceUsedClientID records whether the S13 device signature was attached
	// to the face request. The value itself never leaves applyFaceCredentials.
	faceUsedClientID bool
	// facePhotoID is the pool entry selected for this execution. A retry
	// inside the same execution reuses it and never counts it twice.
	facePhotoID      uint
	facePhotoCounted bool
	// facePhotoSource labels the selected base for logs: pool or
	// official_auto (imported during this execution).
	facePhotoSource string
	// faceTransform caches the transform output for this execution. The base
	// image in the database is never modified; the cached bytes keep the
	// upload key stable across retries of the same execution.
	faceTransform []byte
	// faceTransformInfo keeps the transform geometry for the failure log. It
	// is written together with faceTransform and reused on retries.
	faceTransformInfo *faceTransformInfo
	// facePhotoImportDone makes the on-demand official import happen at most
	// once per execution, including the failure case.
	facePhotoImportDone bool
	facePhotoImportErr  error
	// faceMildRetried records that this execution already spent its one mild
	// retry after an upstream face rejection (OMP-59). It never retries twice.
	faceMildRetried bool
}

type ExecutePhotoSignRequest struct {
	ActivityID    int64
	TargetUID     int64
	CourseID      int64
	ClassID       int64
	IfRefreshEWM  bool
	IfFace        bool
	ActivityName  string
	CourseName    string
	CourseTeacher string
	ObjectID      string
	Filename      string
	ContentType   string
	Photo         []byte
	// Context scopes on-demand upstream work to the caller's request.
	Context context.Context
}

// SignOutRequest asks for one user's sign-out of the sign-out activity that
// belongs to a completed sign-in. ActivityID is the primary sign-in activity;
// the sign-out activity is resolved from its signOutId metadata.
type SignOutRequest struct {
	ActivityID    int64
	TargetUID     int64
	CourseID      int64
	ClassID       int64
	IfRefreshEWM  bool
	IfOpenAddress bool
	ActivityName  string
	CourseName    string
	CourseTeacher string
	Special       map[string]interface{}
}

type SignOutResult struct {
	UserID           int64  `json:"user_id"`
	Success          bool   `json:"success"`
	AlreadySignedOut bool   `json:"already_signed_out"`
	SignOutID        int64  `json:"sign_out_id"`
	SignOutTimeMS    int64  `json:"sign_out_time_ms"`
	Message          string `json:"message"`
	ErrorCode        string `json:"error_code"`
	Retryable        bool   `json:"retryable"`
}

type SignCheckItem struct {
	UserID           int64  `json:"user_id"`
	Signed           bool   `json:"signed"`
	RecordSource     int64  `json:"record_source"`
	RecordSourceName string `json:"record_source_name"`
	Message          string `json:"message"`
	// CheckedUpstream reports whether preSign supplied the state. Local records
	// are never re-queried, so it stays false for already-recorded users.
	CheckedUpstream bool `json:"checked_upstream"`
}

type SignExecuteResult struct {
	UserID           int64   `json:"user_id"`
	Success          bool    `json:"success"`
	AlreadySigned    bool    `json:"already_signed"`
	RecordSource     int64   `json:"record_source"`
	RecordSourceName string  `json:"record_source_name"`
	Message          string  `json:"message"`
	ErrorCode        string  `json:"error_code"`
	Retryable        bool    `json:"retryable"`
	DistanceM        float64 `json:"distance_m,omitempty"`
}

// Error codes exposed by /api/sign/execute. They mirror xxt.SignResultKind so
// the web client never parses raw upstream strings.
const (
	SignErrorNone            = ""
	SignErrorCaptchaRequired = "captcha_required"
	SignErrorFaceRequired    = "face_required"
	// SignErrorFacePhotoUnavailable means the account had no pool entry and the
	// on-demand official import did not produce a usable base image.
	SignErrorFacePhotoUnavailable  = "face_photo_unavailable"
	SignErrorLocationOutRange      = "location_out_of_range"
	SignErrorLocationPointRequired = "location_point_required"
	SignErrorQRExpired             = "qr_expired"
	SignErrorActivityExpired       = "activity_expired"
	// SignErrorActivityEnded is upstream's success2: the submission arrived
	// after the activity closed. It is a distinct terminal outcome and never
	// writes a local record.
	SignErrorActivityEnded  = "activity_ended"
	SignErrorSessionExpired = "session_expired"
	SignErrorUpstream       = "upstream_error"
	// Sign-out prerequisites are explicit results, never silent successes.
	SignErrorSignInRequired     = "sign_in_required"
	SignErrorSignOutUnavailable = "sign_out_unavailable"
	SignErrorSignOutUnpublished = "sign_out_not_published"
)

// ErrFacePhotoUnavailable reports that no usable base image could be prepared:
// the pool was empty and the official import failed, or a stored base could
// not be decoded/transformed.
var ErrFacePhotoUnavailable = errors.New("face photo unavailable")

// facePhotoUnavailableError keeps a user-facing message while matching
// ErrFacePhotoUnavailable. The underlying cause belongs in logs, not the API.
type facePhotoUnavailableError struct{ message string }

func (e facePhotoUnavailableError) Error() string { return e.message }
func (e facePhotoUnavailableError) Is(target error) bool {
	return target == ErrFacePhotoUnavailable
}

func (s *SignService) CheckSignStates(operatorUID, activityID, courseID, classID int64, userIDs []int64) ([]SignCheckItem, error) {
	targets := make([]int64, 0, len(userIDs)+1)
	targets = append(targets, operatorUID)
	targets = append(targets, userIDs...)
	uniq := dedupeUIDs(targets)
	if err := s.AuthorizeTargets(operatorUID, activityID, courseID, classID, uniq); err != nil {
		return nil, err
	}
	items := make([]SignCheckItem, 0, len(uniq))
	for _, uid := range uniq {
		state, err := s.resolveSignState(activityID, uid)
		if err != nil {
			return nil, err
		}
		if !state.Signed {
			state, err = s.mergeUpstreamSignState(activityID, courseID, classID, state)
			if err != nil {
				return nil, err
			}
		}
		items = append(items, state)
	}
	return items, nil
}

// mergeUpstreamSignState asks 学习通 whether the user already signed. It only
// writes a record when upstream reports a signature. Credential, transport and
// upstream errors leave the local unsigned state intact so a check still
// answers; database errors are returned because the answer would be trusted
// without the record the rest of the system relies on.
func (s *SignService) mergeUpstreamSignState(activityID, courseID, classID int64, state SignCheckItem) (SignCheckItem, error) {
	target, err := LoadActiveUser(s.db, state.UserID)
	if err != nil {
		log.Printf("check upstream state skipped: activity=%d uid=%d err=%v", activityID, state.UserID, err)
		return state, nil
	}
	password, err := s.cc.Decrypt(target.CredentialCipher)
	if err != nil {
		log.Printf("check upstream credentials unavailable: activity=%d uid=%d err=%v", activityID, state.UserID, err)
		return state, nil
	}
	detail, err := s.xxt.GetSignDetail(target.Mobile, password, activityID, courseID, classID, state.UserID)
	if err != nil {
		log.Printf("check upstream metadata failed: activity=%d uid=%d err=%v", activityID, state.UserID, err)
		return state, nil
	}
	fixed := xxt.FixedParams{
		ActiveID: activityID,
		UID:      state.UserID,
		CourseID: courseID,
		ClassID:  classID,
		Ext:      detail.Ext,
	}
	if fixed.Ext == "" {
		fixed.Ext = s.cachedActivityExt(activityID)
	}
	result, err := s.xxt.PreSign(target.Mobile, password, fixed, detail.SignType, "", "")
	if err != nil {
		if errors.Is(err, xxt.ErrSessionExpired) {
			state.Message = "学习通登录已过期，无法确认签到状态"
		}
		log.Printf("check upstream preSign failed: activity=%d uid=%d err=%v", activityID, state.UserID, err)
		return state, nil
	}
	switch result.Status {
	case xxt.PreSignAlreadySigned:
		return s.recordUpstreamSigned(activityID, courseID, classID, detail.SignType, state)
	case xxt.PreSignExpired:
		state.CheckedUpstream = true
		state.Message = result.Message
	case xxt.PreSignReady:
		state.CheckedUpstream = true
	case xxt.PreSignNoPermission:
		state.Message = "无法确认该活动的签到状态"
	}
	return state, nil
}

// recordUpstreamSigned persists an official self-sign discovered by preSign.
// The unique (user_uid, activity_id) index makes concurrent checks idempotent.
func (s *SignService) recordUpstreamSigned(activityID, courseID, classID int64, signType int, state SignCheckItem) (SignCheckItem, error) {
	rec := model.SignRecord{
		UserUID:    state.UserID,
		ActivityID: activityID,
		SourceUID:  -1,
		CourseID:   courseID,
		ClassID:    classID,
		SignType:   signType,
		SignTimeMS: time.Now().UnixMilli(),
	}
	if err := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_uid"}, {Name: "activity_id"}}, DoNothing: true}).Create(&rec).Error; err != nil {
		return SignCheckItem{}, err
	}
	state.Signed = true
	state.RecordSource = -1
	state.RecordSourceName = "学习通"
	state.Message = "该同学已在学习通签到"
	state.CheckedUpstream = true
	return state, nil
}

// cachedActivityExt reads the ext captured by activity discovery. A missing
// row or empty value is normal (the activity was never listed) and yields "".
func (s *SignService) cachedActivityExt(activityID int64) string {
	var cached model.SignActivity
	err := s.db.Select("ext_json").Where("activity_id = ?", activityID).First(&cached).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("load cached activity ext failed: activity=%d err=%v", activityID, err)
		}
		return ""
	}
	return cached.ExtJSON
}

func (s *SignService) AuthorizeTargets(operatorUID, activityID, courseID, classID int64, targetUIDs []int64) error {
	operator, err := LoadActiveUser(s.db, operatorUID)
	if err != nil {
		return err
	}
	if activityID <= 0 {
		return ErrSignForbidden
	}
	selected, err := UserSelectedCourse(s.db, operatorUID, courseID, classID)
	if err != nil {
		return err
	}
	if !selected {
		return ErrSignForbidden
	}
	for _, uid := range targetUIDs {
		if uid <= 0 {
			return ErrSignForbidden
		}
	}
	targets := dedupeUIDs(targetUIDs)
	if len(targets) > 0 {
		var count int64
		if err := s.db.Table("user_courses uc").
			Joins("JOIN users u ON u.uid = uc.user_uid").
			Joins("JOIN whitelists w ON w.mobile = u.mobile").
			Where("uc.course_id = ? AND uc.class_id = ? AND uc.is_selected = true", courseID, classID).
			Where("u.uid IN ? AND u.permission > 0 AND w.permission > 0", targets).
			Distinct("u.uid").Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(targets)) {
			return ErrSignForbidden
		}
	}
	var count int64
	if err := s.db.Model(&model.SignActivityScope{}).
		Where("activity_id = ? AND course_id = ? AND class_id = ?", activityID, courseID, classID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	password, err := s.cc.Decrypt(operator.CredentialCipher)
	if err != nil {
		return fmt.Errorf("%w: operator credential unavailable", ErrActivityScopeUnavailable)
	}
	found, err := s.xxt.HasActivityInCourse(operator.Mobile, password, courseID, classID, activityID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrActivityScopeUnavailable, err)
	}
	if !found {
		return ErrSignForbidden
	}
	scope := model.SignActivityScope{ActivityID: activityID, CourseID: courseID, ClassID: classID}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&scope).Error
}

func (s *SignService) ExecuteOne(operatorUID int64, req ExecuteSignRequest) (res SignExecuteResult, err error) {
	startedAt := time.Now()
	stats := &signExecutionStats{}
	defer func() {
		s.metrics.ObserveSign(observe.SignEvent{
			ActivityID:        req.ActivityID,
			UserUID:           req.TargetUID,
			SignType:          req.SignType,
			ResultCode:        executeResultCode(res, err),
			Attempt:           stats.attempts,
			UpstreamStatus:    stats.upstreamStatus,
			DurationMS:        time.Since(startedAt).Milliseconds(),
			Success:           res.Success,
			AlreadySigned:     res.AlreadySigned,
			ValidateTriggered: stats.validateTriggered,
			CaptchaPassed:     stats.captchaPassed,
			LocationError:     stats.locationError,
			Upstream403:       stats.upstream403,
			HasCxcid:          req.faceUsedClientID,
		})
	}()
	if err := s.AuthorizeTargets(operatorUID, req.ActivityID, req.CourseID, req.ClassID, []int64{req.TargetUID}); err != nil {
		return SignExecuteResult{}, err
	}
	if req.Special == nil {
		req.Special = map[string]interface{}{}
	} else {
		// Share links reuse one request map across targets; randomization and
		// state-machine fields must not leak between accounts.
		copied := make(map[string]interface{}, len(req.Special)+4)
		for key, value := range req.Special {
			copied[key] = value
		}
		req.Special = copied
	}
	if _, provided := req.Special["if_face"]; !provided {
		var activity model.SignActivity
		if err := s.db.Select("if_face").Where("activity_id = ?", req.ActivityID).First(&activity).Error; err == nil && activity.IfFace {
			req.Special["if_face"] = true
		}
	}
	state, err := s.resolveSignState(req.ActivityID, req.TargetUID)
	if err != nil {
		return SignExecuteResult{}, err
	}
	if state.Signed {
		return SignExecuteResult{
			UserID:           req.TargetUID,
			Success:          true,
			AlreadySigned:    true,
			RecordSource:     state.RecordSource,
			RecordSourceName: state.RecordSourceName,
			Message:          state.Message,
		}, nil
	}

	target, err := LoadActiveUser(s.db, req.TargetUID)
	if err != nil {
		if errors.Is(err, ErrAccountInactive) {
			return SignExecuteResult{}, ErrSignForbidden
		}
		return SignExecuteResult{}, err
	}
	password, err := s.cc.Decrypt(target.CredentialCipher)
	if err != nil {
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "该同学登录信息已过期，请先重新登录", ErrorCode: SignErrorSessionExpired}, nil
	}

	fixed := xxt.FixedParams{
		ActiveID:      req.ActivityID,
		UID:           req.TargetUID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		IfRefreshEWM:  req.IfRefreshEWM,
		IfOpenAddress: req.IfOpenAddress,
	}
	// Activity metadata is authoritative for both the location toggle and the
	// opaque `ext` that preSign must post (30s cache per account, XBT-S1).
	detail, detailErr := s.xxt.GetSignDetail(target.Mobile, password, req.ActivityID, req.CourseID, req.ClassID, req.TargetUID)
	if detailErr == nil {
		fixed.IfOpenAddress = fixed.IfOpenAddress || detail.IfOpenAddress
		fixed.Ext = detail.Ext
	} else {
		log.Printf("sign metadata lookup failed: activity=%d uid=%d err=%v", req.ActivityID, req.TargetUID, detailErr)
	}
	if fixed.Ext == "" {
		// Discovery caches the opaque ext so an execute still posts it when
		// the metadata endpoint is unavailable (XBT-S8).
		fixed.Ext = s.cachedActivityExt(req.ActivityID)
	}
	if result, ok := s.prepareSignLocation(&req, detail, detailErr == nil); !ok {
		return result, nil
	}

	enc, _ := req.Special["enc"].(string)
	code, _ := req.Special["c"].(string)
	preSign, err := s.xxt.PreSign(target.Mobile, password, fixed, req.SignType, code, enc)
	if err != nil {
		if errors.Is(err, xxt.ErrSessionExpired) {
			// preSign already retried once after a 403; a terminal expiry is
			// the observable 403 outcome for this execute.
			stats.upstream403 = true
			return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorSessionExpired, Message: "学习通登录已过期，请重新登录"}, nil
		}
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "预签到失败，请重试", ErrorCode: SignErrorUpstream, Retryable: true}, nil
	}
	switch preSign.Status {
	case xxt.PreSignExpired:
		// A closed activity never gets a record, even if the page still
		// renders a signed marker.
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorActivityExpired, Message: preSign.Message}, nil
	case xxt.PreSignAlreadySigned:
		return s.recordAlreadySigned(req)
	case xxt.PreSignNoPermission:
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorUpstream, Message: preSign.Message}, nil
	}

	if ifFace, _ := req.Special["if_face"].(bool); ifFace {
		if err := s.applyFaceCredentials(&target, password, &req); err != nil {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: faceCredentialErrorCode(err), Message: err.Error()}, nil
		}
	}

	result, terminal, err := s.submitSign(&target, password, &req, fixed, stats)
	if err != nil {
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: s.toUserSignMessage(err.Error()), ErrorCode: SignErrorUpstream, Retryable: true}, nil
	}
	if terminal != nil {
		return *terminal, nil
	}
	if result.Kind == xxt.SignResultFaceRequired {
		MarkFacePhotoFailed(s.db, req.facePhotoID)
	}

	switch result.Kind {
	case xxt.SignResultSuccess:
		return s.recordSigned(req, operatorUID)
	case xxt.SignResultAlreadySigned:
		return s.recordAlreadySigned(req)
	}
	code, retryable, distance, message := s.signFailure(result, req.locationProvided)
	return SignExecuteResult{
		UserID:    req.TargetUID,
		Success:   false,
		DistanceM: distance,
		Message:   message,
		ErrorCode: code,
		Retryable: retryable,
	}, nil
}

// executeResultCode normalises the public error contract into the single
// result_code field. Empty results never reach the client as success.
func executeResultCode(res SignExecuteResult, err error) string {
	if err != nil {
		switch {
		case errors.Is(err, ErrSignForbidden):
			return "forbidden"
		case errors.Is(err, ErrAccountInactive):
			return "account_inactive"
		case errors.Is(err, ErrActivityScopeUnavailable):
			return "activity_scope_unavailable"
		default:
			return "internal_error"
		}
	}
	if res.ErrorCode != "" {
		return res.ErrorCode
	}
	if res.Success && res.AlreadySigned {
		return "already_signed"
	}
	if res.Success {
		return "success"
	}
	return "failed"
}

// ObserveSign and the other observer accessors expose the ledger to handlers
// that reject a request before it reaches ExecuteOne.
func (s *SignService) ObserveSign(event observe.SignEvent) { s.metrics.ObserveSign(event) }

func (s *SignService) ObserveActivities(duration time.Duration) {
	s.metrics.ObserveActivities(duration)
}

func (s *SignService) ObserveIM(uid int64, enabled, fetched bool, errorCode string, latency time.Duration) {
	s.metrics.ObserveIM(uid, enabled, fetched, errorCode, latency)
}

func (s *SignService) MetricsSnapshot() observe.SignSnapshot { return s.metrics.Snapshot() }

// submitSign runs the bounded recovery state machine shared by sign-in and
// sign-out: captcha is resolved inside xxt.Sign (at most one extra request),
// face and location recoveries happen here and each may retry once. It returns
// a terminal result only when face credentials cannot be prepared.
func (s *SignService) submitSign(target *model.User, password string, req *ExecuteSignRequest, fixed xxt.FixedParams, stats *signExecutionStats) (xxt.SignResult, *SignExecuteResult, error) {
	if stats == nil {
		// Sign-out reuses the state machine but has its own result ledger.
		stats = &signExecutionStats{}
	}
	faceRetried := false
	locationRetried := false
	for {
		result, err := s.xxt.Sign(target.Mobile, password, fixed, req.SignType, req.Special)
		if err != nil {
			// The transport failed before a response was available; still
			// count the request so the attempt ledger is not zero.
			stats.attempts++
			return xxt.SignResult{}, nil, err
		}
		stats.record(result)
		switch {
		case result.Kind == xxt.SignResultFaceRequired && !faceRetried:
			faceRetried = true
			if err := s.applyFaceCredentials(target, password, req); err != nil {
				return xxt.SignResult{}, &SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: faceCredentialErrorCode(err), Message: err.Error()}, nil
			}
			if result.Enc2 != "" {
				req.Special["enc2"] = result.Enc2
			}
			continue
		case result.Kind == xxt.SignResultLocationOutOfRange && !locationRetried && req.locationBase != nil:
			// The first errorLocation_ retry must not randomize again: send the
			// unrandomized base point (validated preset or metadata center).
			// Without a base the request carried no point, so a retry would
			// repeat the same -1/-1 submission (XBT-S24).
			locationRetried = true
			applySignPosition(req.Special, req.locationBase)
			continue
		}
		return result, nil, nil
	}
}

// signFailure maps a typed upstream outcome to the public error contract. Both
// sign-in and sign-out share it so the codes stay identical. locationProvided
// tells whether the submitted request carried usable coordinates: upstream
// reports errorLocation_ for -1/-1 too, and that is a missing point, not a
// ~12,000 km miss (XBT-S24).
func (s *SignService) signFailure(result xxt.SignResult, locationProvided bool) (code string, retryable bool, distanceM float64, message string) {
	switch result.Kind {
	case xxt.SignResultCaptchaRequired:
		return SignErrorCaptchaRequired, true, 0, "验证码校验未通过，请重试"
	case xxt.SignResultLocationOutOfRange:
		if !locationProvided {
			return SignErrorLocationPointRequired, false, 0, locationPointRequiredMessage
		}
		message := "签到位置超出范围，请重新选择位置"
		if result.DistanceM > 0 {
			message = locationOutOfRangeMessage(result.DistanceM)
		}
		return SignErrorLocationOutRange, false, result.DistanceM, message
	case xxt.SignResultQRExpired:
		return SignErrorQRExpired, false, 0, "二维码已过期，请重新扫码"
	case xxt.SignResultActivityEnded:
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "签到已结束（迟到或活动已关闭）"
		}
		return SignErrorActivityEnded, false, 0, message
	case xxt.SignResultSessionExpired:
		return SignErrorSessionExpired, false, 0, "学习通登录已过期，请重新登录"
	case xxt.SignResultFaceRequired:
		message := "人脸验证未通过，请更新人脸照片后重试"
		if strings.TrimSpace(result.Message) != "" {
			message = "人脸验证未通过：" + strings.TrimSpace(result.Message)
		}
		return SignErrorFaceRequired, false, 0, message
	default:
		return SignErrorUpstream, true, 0, s.toUserSignMessage(result.Message)
	}
}

// SignOutOne signs out the sign-out activity that belongs to a completed
// sign-in. The request never overwrites sign_records: success produces one
// sign_out_records row keyed by the primary activity.
//
// Upstream model (CSF 9ae5398): getPPTActiveInfo of the primary activity
// carries signOutId and signOutPublishTimeStamp. The sign-out activity itself
// is then fetched for its sign type/ext and signed with the same stuSignajax
// state machine as a normal sign-in.
func (s *SignService) SignOutOne(operatorUID int64, req SignOutRequest) (SignOutResult, error) {
	if req.TargetUID <= 0 {
		req.TargetUID = operatorUID
	}
	result := SignOutResult{UserID: req.TargetUID}
	if req.ActivityID <= 0 || req.CourseID <= 0 || req.ClassID <= 0 {
		return SignOutResult{}, ErrSignForbidden
	}
	if req.Special == nil {
		req.Special = map[string]interface{}{}
	}
	if err := s.AuthorizeTargets(operatorUID, req.ActivityID, req.CourseID, req.ClassID, []int64{req.TargetUID}); err != nil {
		return SignOutResult{}, err
	}

	// A stored sign-out is the idempotency boundary: repeated requests never
	// reach upstream and never duplicate the ledger row.
	var prior model.SignOutRecord
	err := s.db.Where("user_uid = ? AND activity_id = ?", req.TargetUID, req.ActivityID).Take(&prior).Error
	if err == nil {
		result.Success = true
		result.AlreadySignedOut = true
		result.SignOutID = prior.SignOutID
		result.SignOutTimeMS = prior.SignOutTimeMS
		result.Message = "该同学已签退"
		return result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return SignOutResult{}, err
	}

	target, err := LoadActiveUser(s.db, req.TargetUID)
	if err != nil {
		if errors.Is(err, ErrAccountInactive) {
			return SignOutResult{}, ErrSignForbidden
		}
		return SignOutResult{}, err
	}
	password, err := s.cc.Decrypt(target.CredentialCipher)
	if err != nil {
		result.ErrorCode = SignErrorSessionExpired
		result.Message = "该同学登录信息已过期，请先重新登录"
		return result, nil
	}

	detail, err := s.xxt.GetSignDetail(target.Mobile, password, req.ActivityID, req.CourseID, req.ClassID, req.TargetUID)
	if err != nil {
		return s.signOutUpstreamFailure(result, err), nil
	}
	// The publish timestamp is visible before signOutId is assigned, so it is
	// checked first to give a "not published yet" answer instead of "missing".
	if detail.SignOutPublishTime > 0 && detail.SignOutPublishTime > time.Now().UnixMilli() {
		result.ErrorCode = SignErrorSignOutUnpublished
		result.Message = fmt.Sprintf("签退将于 %s 发布", time.UnixMilli(detail.SignOutPublishTime).Format("2006-01-02 15:04"))
		return result, nil
	}
	signOutID := detail.SignOutID
	if signOutID <= 0 || signOutID == req.ActivityID {
		result.ErrorCode = SignErrorSignOutUnavailable
		result.Message = "该签到活动未设置签退"
		return result, nil
	}
	result.SignOutID = signOutID

	if ok, early := s.ensurePrimarySignedIn(&target, password, req, detail); !ok {
		return early, nil
	}

	outDetail, err := s.xxt.GetSignDetail(target.Mobile, password, signOutID, req.CourseID, req.ClassID, req.TargetUID)
	if err != nil {
		return s.signOutUpstreamFailure(result, err), nil
	}
	outFixed := xxt.FixedParams{
		ActiveID:      signOutID,
		UID:           req.TargetUID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		IfRefreshEWM:  req.IfRefreshEWM || outDetail.IfRefreshEWM,
		IfOpenAddress: outDetail.IfOpenAddress,
		Ext:           outDetail.Ext,
	}
	if outFixed.Ext == "" {
		outFixed.Ext = s.cachedActivityExt(signOutID)
	}

	enc, _ := req.Special["enc"].(string)
	code, _ := req.Special["c"].(string)
	preSign, err := s.xxt.PreSign(target.Mobile, password, outFixed, outDetail.SignType, code, enc)
	if err != nil {
		return s.signOutUpstreamFailure(result, err), nil
	}
	switch preSign.Status {
	case xxt.PreSignExpired:
		result.ErrorCode = SignErrorActivityExpired
		result.Message = signOutMessage(preSign.Message, "签退已截止")
		return result, nil
	case xxt.PreSignAlreadySigned:
		// Upstream already has the sign-out, so record it locally without
		// asking stuSignajax again.
		return s.recordSignedOut(req, signOutID, outDetail.SignType, -1, true)
	case xxt.PreSignNoPermission:
		result.ErrorCode = SignErrorUpstream
		result.Message = signOutMessage(preSign.Message, "无法签退该活动")
		return result, nil
	}

	signReq := ExecuteSignRequest{
		ActivityID:    signOutID,
		TargetUID:     req.TargetUID,
		SignType:      outDetail.SignType,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		IfRefreshEWM:  outFixed.IfRefreshEWM,
		IfOpenAddress: outFixed.IfOpenAddress,
		ActivityName:  req.ActivityName,
		CourseName:    req.CourseName,
		CourseTeacher: req.CourseTeacher,
		Special:       copySignSpecial(req.Special),
	}
	if term, ok := s.prepareSignLocation(&signReq, outDetail, true); !ok {
		result.ErrorCode = term.ErrorCode
		result.Message = term.Message
		return result, nil
	}
	if outDetail.IfFace {
		signReq.Special["if_face"] = true
		if err := s.applyFaceCredentials(&target, password, &signReq); err != nil {
			result.ErrorCode = faceCredentialErrorCode(err)
			result.Message = err.Error()
			return result, nil
		}
	}

	upstream, terminal, err := s.submitSign(&target, password, &signReq, outFixed, nil)
	if err != nil {
		result.ErrorCode = SignErrorUpstream
		result.Retryable = true
		result.Message = s.toUserSignMessage(err.Error())
		return result, nil
	}
	if terminal != nil {
		result.ErrorCode = terminal.ErrorCode
		result.Message = terminal.Message
		result.Retryable = terminal.Retryable
		return result, nil
	}
	if upstream.Kind == xxt.SignResultFaceRequired {
		MarkFacePhotoFailed(s.db, signReq.facePhotoID)
	}
	switch upstream.Kind {
	case xxt.SignResultSuccess:
		return s.recordSignedOut(req, signOutID, outDetail.SignType, operatorUID, false)
	case xxt.SignResultAlreadySigned:
		return s.recordSignedOut(req, signOutID, outDetail.SignType, -1, true)
	}
	code, retryable, message := s.signOutFailure(upstream, signReq.locationProvided)
	result.ErrorCode = code
	result.Retryable = retryable
	result.Message = message
	return result, nil
}

// ensurePrimarySignedIn requires a local sign record, or an upstream preSign
// that proves the user already signed. It writes the 学习通-source record in
// the latter case so the sign-out ledger always rests on a sign-in fact.
func (s *SignService) ensurePrimarySignedIn(target *model.User, password string, req SignOutRequest, detail xxt.SignDetail) (bool, SignOutResult) {
	early := SignOutResult{UserID: req.TargetUID}
	var rec model.SignRecord
	err := s.db.Where("user_uid = ? AND activity_id = ?", req.TargetUID, req.ActivityID).Take(&rec).Error
	if err == nil {
		return true, SignOutResult{}
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		early.ErrorCode = SignErrorUpstream
		early.Retryable = true
		early.Message = "查询主签到记录失败，请重试"
		return false, early
	}

	fixed := xxt.FixedParams{
		ActiveID: req.ActivityID,
		UID:      req.TargetUID,
		CourseID: req.CourseID,
		ClassID:  req.ClassID,
		Ext:      detail.Ext,
	}
	if fixed.Ext == "" {
		fixed.Ext = s.cachedActivityExt(req.ActivityID)
	}
	enc, _ := req.Special["enc"].(string)
	code, _ := req.Special["c"].(string)
	preSign, err := s.xxt.PreSign(target.Mobile, password, fixed, detail.SignType, code, enc)
	if err != nil {
		return false, s.signOutUpstreamFailure(early, err)
	}
	switch preSign.Status {
	case xxt.PreSignAlreadySigned:
		state := SignCheckItem{UserID: req.TargetUID}
		if _, err := s.recordUpstreamSigned(req.ActivityID, req.CourseID, req.ClassID, detail.SignType, state); err != nil {
			early.ErrorCode = SignErrorUpstream
			early.Retryable = true
			early.Message = "写入主签到记录失败，请重试"
			return false, early
		}
		return true, SignOutResult{}
	case xxt.PreSignNoPermission:
		early.ErrorCode = SignErrorUpstream
		early.Message = signOutMessage(preSign.Message, "无法确认主签到状态")
		return false, early
	default:
		early.ErrorCode = SignErrorSignInRequired
		early.Message = "请先完成主签到"
		return false, early
	}
}

func (s *SignService) recordSignedOut(req SignOutRequest, signOutID int64, signType int, sourceUID int64, already bool) (SignOutResult, error) {
	rec := model.SignOutRecord{
		UserUID:       req.TargetUID,
		ActivityID:    req.ActivityID,
		SignOutID:     signOutID,
		SourceUID:     sourceUID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		SignType:      signType,
		ActivityName:  strings.TrimSpace(req.ActivityName),
		CourseName:    strings.TrimSpace(req.CourseName),
		CourseTeacher: strings.TrimSpace(req.CourseTeacher),
		SignOutTimeMS: time.Now().UnixMilli(),
	}
	dbResult := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_uid"}, {Name: "activity_id"}},
		DoNothing: true,
	}).Create(&rec)
	if dbResult.Error != nil {
		return SignOutResult{}, dbResult.Error
	}
	// Read back so a concurrent writer's timestamp and source are authoritative.
	if err := s.db.Where("user_uid = ? AND activity_id = ?", req.TargetUID, req.ActivityID).Take(&rec).Error; err != nil {
		return SignOutResult{}, err
	}
	result := SignOutResult{
		UserID:           req.TargetUID,
		Success:          true,
		AlreadySignedOut: already || dbResult.RowsAffected == 0,
		SignOutID:        rec.SignOutID,
		SignOutTimeMS:    rec.SignOutTimeMS,
		Message:          "签退成功",
	}
	if result.AlreadySignedOut {
		result.Message = "该同学已签退"
	}
	return result, nil
}

func (s *SignService) signOutUpstreamFailure(result SignOutResult, err error) SignOutResult {
	if errors.Is(err, xxt.ErrSessionExpired) {
		result.ErrorCode = SignErrorSessionExpired
		result.Message = "学习通登录已过期，请重新登录"
		return result
	}
	log.Printf("sign-out upstream failure: activity=%d uid=%d err=%v", result.SignOutID, result.UserID, err)
	result.ErrorCode = SignErrorUpstream
	result.Retryable = true
	result.Message = "获取签退信息失败，请重试"
	return result
}

func (s *SignService) signOutFailure(result xxt.SignResult, locationProvided bool) (code string, retryable bool, message string) {
	code, retryable, distance, message := s.signFailure(result, locationProvided)
	if code == SignErrorLocationOutRange && distance > 0 {
		message = fmt.Sprintf("签退位置超出范围，距签退点%.0f米，请重新选择位置", math.Round(distance))
	}
	if code == SignErrorActivityEnded {
		message = "签退已结束（迟到或活动已关闭）"
	}
	return code, retryable, message
}

func signOutMessage(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return strings.TrimSpace(message)
}

func copySignSpecial(source map[string]interface{}) map[string]interface{} {
	copied := make(map[string]interface{}, len(source)+4)
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

// prepareSignLocation validates a caller-selected point against the activity
// metadata and chooses the submitted coordinates. An explicit selection is
// submitted with a bounded random offset (location_jitter_meters, XBT-S26), so
// the user's choice stays meaningful while each execution differs. Without a
// selection, the served point is uniformly sampled inside the activity circle
// and the metadata center becomes both the sampling origin and the retry
// point. An out-of-range preset is rejected before any preSign/stuSignajax
// request.
//
// A location check-in without a caller-selected point can only be signed when
// metadata supplies a usable center and radius. Otherwise the request returns
// location_point_required instead of letting the parameter builder submit
// -1/-1 (which upstream reports as a ~12,000 km distance, XBT-S19).
func (s *SignService) prepareSignLocation(req *ExecuteSignRequest, detail xxt.SignDetail, metadataOK bool) (SignExecuteResult, bool) {
	locationSign := req.SignType == xxt.SignLocation || (metadataOK && detail.SignType == xxt.SignLocation)
	selected := xxt.PositionFromSpecial(req.Special)
	if selected != nil && !xxt.ValidGeoPoint(xxt.GeoPoint{Latitude: selected.Latitude, Longitude: selected.Longitude}) {
		selected = nil
	}

	if !metadataOK {
		if selected == nil && locationSign {
			return locationPointRequired(req.TargetUID), false
		}
		if selected != nil {
			req.locationBase = selected
			req.locationProvided = true
		}
		return SignExecuteResult{}, true
	}
	if !detail.IfOpenAddress {
		// The caller still supplied a point; the builder only forwards it when
		// the activity opens address, but an upstream errorLocation_ then
		// describes that point, not a missing one.
		req.locationProvided = selected != nil
		return SignExecuteResult{}, true
	}
	center := xxt.GeoPoint{Latitude: detail.LocationLatitude, Longitude: detail.LocationLongitude}
	radius := float64(detail.LocationRange)
	if !xxt.ValidGeoPoint(center) || radius <= 0 {
		if selected == nil && locationSign {
			return locationPointRequired(req.TargetUID), false
		}
		if selected != nil {
			// A caller-selected point stays authoritative when metadata has no
			// usable circle; this preserves the pre-S19 behavior.
			req.locationBase = selected
			req.locationProvided = true
		}
		return SignExecuteResult{}, true
	}

	base := xxt.SignPosition{Latitude: center.Latitude, Longitude: center.Longitude}
	if selected != nil {
		selectedPoint := xxt.GeoPoint{Latitude: selected.Latitude, Longitude: selected.Longitude}
		distance := xxt.DistanceMeters(selectedPoint, center)
		if distance > radius {
			return SignExecuteResult{
				UserID:    req.TargetUID,
				Success:   false,
				ErrorCode: SignErrorLocationOutRange,
				DistanceM: distance,
				Message:   locationOutOfRangeMessage(distance),
			}, false
		}
		base = *selected
		// An explicit selection stays authoritative: only a small offset is
		// applied instead of resampling the whole activity circle (XBT-S26).
		jittered := s.sampleJitter(selectedPoint, s.locationJitterMeters)
		point := xxt.GeoPoint{Latitude: jittered.Latitude, Longitude: jittered.Longitude}
		fallback := false
		if xxt.DistanceMeters(point, center) > radius {
			// A selection near the edge can leave the circle after the
			// offset. Submit the exact selection instead of an invalid point.
			point = selectedPoint
			fallback = true
		}
		position := xxt.SignPosition{Latitude: point.Latitude, Longitude: point.Longitude, Address: base.Address}
		applySignPosition(req.Special, &position)
		req.locationBase = &base
		req.locationProvided = true
		log.Printf("sign location location_mode=selected activity=%d uid=%d jitter_m=%g jitter_fallback=%t",
			req.ActivityID, req.TargetUID, s.locationJitterMeters, fallback)
		return SignExecuteResult{}, true
	}

	sampled := s.sampleLocation(center, radius)
	position := xxt.SignPosition{Latitude: sampled.Latitude, Longitude: sampled.Longitude, Address: base.Address}
	applySignPosition(req.Special, &position)
	req.locationBase = &base
	req.locationProvided = true
	log.Printf("sign location location_mode=auto activity=%d uid=%d radius_m=%g", req.ActivityID, req.TargetUID, radius)
	return SignExecuteResult{}, true
}

// locationPointRequiredMessage explains a missing point without pretending the
// submission was out of range. The distance in that case is meaningless
// (upstream computes it from -1/-1, about 12,000 km).
const locationPointRequiredMessage = "该签到要求定位，请先在地图选点或允许定位"

func locationPointRequired(userID int64) SignExecuteResult {
	return SignExecuteResult{
		UserID:    userID,
		Success:   false,
		ErrorCode: SignErrorLocationPointRequired,
		Message:   locationPointRequiredMessage,
	}
}

// applySignPosition writes the protocol fields read by PositionFromSpecial.
// Coordinates always use six decimals so sampled and preset points share one
// format.
func applySignPosition(special map[string]interface{}, position *xxt.SignPosition) {
	special["latitude"] = strconv.FormatFloat(position.Latitude, 'f', 6, 64)
	special["longitude"] = strconv.FormatFloat(position.Longitude, 'f', 6, 64)
	if position.Address != "" {
		special["description"] = position.Address
	}
}

func locationOutOfRangeMessage(distance float64) string {
	return fmt.Sprintf("签到位置超出范围，距签到点%.0f米，请重新选择位置", math.Round(distance))
}

// faceUploadTTL bounds the in-memory objectId cache. Signing windows are far
// shorter; the TTL only keeps a long-lived process from accumulating entries
// for activities nobody will touch again.
const faceUploadTTL = 6 * time.Hour

// facePhotoImportTimeout bounds the on-demand official photo fetch used when
// an account has no pool entry. It mirrors the clientId backfill budget so a
// slow upstream cannot stall the sign window.
const facePhotoImportTimeout = 5 * time.Second

const (
	// faceBaseRefreshTimeout bounds the legacy official base re-download used
	// by OMP-59. It mirrors the import budget so a slow upstream cannot stall
	// the sign window, and it is not the sign request timeout.
	faceBaseRefreshTimeout = 5 * time.Second
	// faceBaseRefreshCooldown is the per-account bound on legacy base
	// refreshes, including failed attempts, so a rejection cannot trigger an
	// official download on every execution.
	faceBaseRefreshCooldown = time.Hour
)

type faceUploadKey struct {
	UserUID    int64
	ActivityID int64
	PhotoHash  [sha256.Size]byte
}

type faceUploadEntry struct {
	ObjectID   string
	UploadedAt time.Time
}

// faceCredentialErrorCode separates "no usable photo" from an upstream face
// rejection so the client can tell the user to upload a photo.
func faceCredentialErrorCode(err error) string {
	if errors.Is(err, ErrFacePhotoUnavailable) {
		return SignErrorFacePhotoUnavailable
	}
	return SignErrorFaceRequired
}

func (s *SignService) applyFaceCredentials(target *model.User, password string, req *ExecuteSignRequest) error {
	photo, err := s.facePhotoForSign(req)
	if err != nil {
		return err
	}
	source := req.facePhotoSource
	if source == "" {
		source = "pool"
	}
	faceResult, objectID, uploadMode, err := s.checkFaceWithPhoto(target, password, req, photo, source, false)
	if err != nil {
		// One rejection-only retry with the mild preset (OMP-59). It is spent
		// at most once per execution and never applied before the first
		// transform, so a successful path keeps its original request count.
		if code, retryable := faceCheckRetryCode(err); retryable && !req.faceMildRetried {
			req.faceMildRetried = true
			faceResult, objectID, uploadMode, err = s.checkFaceWithPhoto(target, password, req, photo, source, true)
			log.Printf("face_check_retry=mild activity_id=%d uid=%d photo_id=%d base_version=%d code=%s result=%s",
				req.ActivityID, req.TargetUID, photo.ID, photo.BaseVersion, code, faceCheckRetryResult(err))
		}
	}
	if err != nil {
		// Only an explicit upstream rejection poisons the photo; transport and
		// session errors must not blacklist an otherwise valid image. The use
		// counter is written once per execution, after the retry outcome.
		rejected := errors.Is(err, xxt.ErrFaceRejected)
		s.recordFaceUse(req, objectID, rejected)
		s.logFaceCheckFailed(req, photo, objectID, req.faceUsedClientID, uploadMode, err)
		return fmt.Errorf("人脸验证失败：%v", err)
	}
	s.recordFaceUse(req, objectID, false)
	req.Special["current_face_id"] = faceResult.CurrentFaceID
	req.Special["face_enc"] = faceResult.FaceEnc
	// faceCode is fixed to an empty value by the protocol builder (XBT-S7a).
	return nil
}

// checkFaceWithPhoto transforms (unless this execution already has bytes),
// uploads and checks one base image. A forced retry recomputes the transform
// with the mild preset; it never reuses the rejected bytes.
func (s *SignService) checkFaceWithPhoto(target *model.User, password string, req *ExecuteSignRequest, photo model.FacePhoto, source string, retryMild bool) (xxt.FaceCheckResult, string, string, error) {
	mild := retryMild || s.faceTransformMild
	if req.faceTransform == nil || retryMild {
		transformed, err := facephoto.Transform(photo.Data, facephoto.Options{Rand: s.facePhotoRand, Mild: mild})
		if err != nil {
			log.Printf("face photo transform failed: uid=%d photo_id=%d face_photo_source=%s mild=%t err=%v", req.TargetUID, photo.ID, source, mild, err)
			return xxt.FaceCheckResult{}, "", "", facePhotoUnavailableError{message: "人脸照片处理失败，请更新人脸照片后重试"}
		}
		req.faceTransform = transformed.JPEG
		req.faceTransformInfo = &faceTransformInfo{
			CropWidthRatio:  transformed.CropWidthRatio,
			CropHeightRatio: transformed.CropHeightRatio,
			AngleDegrees:    transformed.AngleDegrees,
			Quality:         transformed.Quality,
		}
		log.Printf("face photo transform: uid=%d photo_id=%d face_photo_source=%s mild=%t crop_w=%.4f crop_h=%.4f angle=%.2f quality=%d bytes=%d",
			req.TargetUID, photo.ID, source, mild,
			transformed.CropWidthRatio, transformed.CropHeightRatio, transformed.AngleDegrees, transformed.Quality, len(req.faceTransform))
	}
	key := faceUploadKey{
		UserUID:    req.TargetUID,
		ActivityID: req.ActivityID,
		PhotoHash:  sha256.Sum256(req.faceTransform),
	}
	upload := s.xxt.UploadFaceTemp
	uploadMode := faceUploadModeFaceTemp
	if s.faceUploadMode == faceUploadModeGeneric {
		upload = s.xxt.UploadPanFile
		uploadMode = faceUploadModeGeneric
	}
	objectID, err := s.faceObjectID(key, func() (string, error) {
		return upload(target.Mobile, password, "face.jpg", "image/jpeg", req.faceTransform)
	})
	if err != nil {
		log.Printf("face photo upload failed: uid=%d photo_id=%d face_photo_source=%s upload_mode=%s err=%v", req.TargetUID, photo.ID, source, uploadMode, err)
		return xxt.FaceCheckResult{}, "", uploadMode, fmt.Errorf("人脸照片上传失败：%v", err)
	}
	clientID := s.faceClientID(req.Context, target, password)
	if clientID != "" {
		req.faceUsedClientID = true
	}
	faceResult, err := s.xxt.CheckFaceResult(target.Mobile, password, req.ActivityID, objectID, clientID)
	return faceResult, objectID, uploadMode, err
}

// faceCheckRetryCode names the upstream rejection codes a single mild retry
// may recover from. Other rejections are terminal for this execution.
func faceCheckRetryCode(err error) (string, bool) {
	code, ok := faceRejectionCode(err)
	if !ok {
		return "", false
	}
	switch code {
	case "CFR002", "CFR004":
		return code, true
	}
	return code, false
}

func faceCheckRetryResult(err error) string {
	if err == nil {
		return "ok"
	}
	if code, ok := faceRejectionCode(err); ok && code != "" {
		return code
	}
	return "error"
}

func faceRejectionCode(err error) (string, bool) {
	var rejection *xxt.FaceRejectedError
	if !errors.As(err, &rejection) {
		return "", false
	}
	return strings.ToUpper(strings.TrimSpace(rejection.Code)), true
}

// logFaceCheckFailed emits one attribution line for a rejected (or failed)
// face check. It never logs photo bytes, passwords, sc, clientId or the full
// upstream body: the response tail is rune-truncated to 200 characters and the
// base image is represented only by its SHA-256 prefix.
func (s *SignService) logFaceCheckFailed(req *ExecuteSignRequest, photo model.FacePhoto, objectID string, hasCxcid bool, uploadMode string, err error) {
	code := "-"
	response := ""
	var rejection *xxt.FaceRejectedError
	if errors.As(err, &rejection) {
		if trimmed := strings.TrimSpace(rejection.Code); trimmed != "" {
			code = trimmed
		}
		response = truncateLogRunes(strings.TrimSpace(rejection.Response), 200)
	}
	transform := "unknown"
	if info := req.faceTransformInfo; info != nil {
		transform = fmt.Sprintf("{crop_w:%.4f,crop_h:%.4f,angle:%.2f,quality:%d}",
			info.CropWidthRatio, info.CropHeightRatio, info.AngleDegrees, info.Quality)
	}
	baseHash := sha256.Sum256(photo.Data)
	log.Printf("face_check_failed activity_id=%d uid=%d photo_id=%d photo_source=%s base_sha256=%s transform=%s object_id=%s has_cxcid=%t code=%s upload_mode=%s response=%q",
		req.ActivityID, req.TargetUID, photo.ID, facePhotoSourceLabel(photo),
		hex.EncodeToString(baseHash[:])[:12], transform, objectID, hasCxcid, code, uploadMode, response)
}

func facePhotoSourceLabel(photo model.FacePhoto) string {
	if photo.Source == "official" {
		return "official"
	}
	return "upload"
}

// truncateLogRunes bounds a log field to n characters without splitting UTF-8.
// The ellipsis is included in n so the field never exceeds the requested size.
func truncateLogRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

// facePhotoForSign keeps one pool entry for the whole execution. A retry
// inside the state machine reuses the photo instead of selecting another one.
// An account with no pool entry imports the official photo once and uses it
// as this execution's base.
func (s *SignService) facePhotoForSign(req *ExecuteSignRequest) (model.FacePhoto, error) {
	if req.facePhotoID != 0 {
		var photo model.FacePhoto
		err := s.db.Where("id = ? AND user_uid = ?", req.facePhotoID, req.TargetUID).Take(&photo).Error
		if err == nil {
			return s.refreshLegacyOfficialBase(req, photo), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return model.FacePhoto{}, fmt.Errorf("读取人脸照片失败：%v", err)
		}
		req.facePhotoID = 0
	}
	photo, err := PickForSign(s.db, req.TargetUID, s.facePhotoRand)
	if errors.Is(err, facephoto.ErrNoPhotos) {
		photo, err = s.importOfficialForSign(req)
		if err != nil {
			return model.FacePhoto{}, err
		}
		req.facePhotoID = photo.ID
		req.facePhotoSource = "official_auto"
		return photo, nil
	}
	if err != nil {
		return model.FacePhoto{}, fmt.Errorf("读取人脸照片失败：%v", err)
	}
	photo = s.refreshLegacyOfficialBase(req, photo)
	req.facePhotoID = photo.ID
	req.facePhotoSource = "pool"
	return photo, nil
}

// refreshLegacyOfficialBase upgrades one pre-S21 official base to an untouched
// original (OMP-59). Only source=official rows at base_version 1 are eligible;
// uploads are never replaced. The account-wide cooldown bounds attempts, and a
// failed attempt falls back to the existing base without failing the sign.
func (s *SignService) refreshLegacyOfficialBase(req *ExecuteSignRequest, photo model.FacePhoto) model.FacePhoto {
	if photo.ID == 0 || photo.Source != "official" || photo.BaseVersion >= facephoto.BaseVersionOriginal {
		return photo
	}
	if s.facePhotos == nil || !s.beginFaceBaseRefresh(req.TargetUID) {
		return photo
	}
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, faceBaseRefreshTimeout)
	defer cancel()
	value, err, _ := s.faceBaseRefreshGroup.Do(strconv.FormatInt(req.TargetUID, 10), func() (interface{}, error) {
		return s.facePhotos.RefreshOfficialBase(ctx, req.TargetUID, photo.ID)
	})
	if err != nil {
		log.Printf("face_base_refresh activity_id=%d uid=%d photo_id=%d base_version=%d result=failed err=%v",
			req.ActivityID, req.TargetUID, photo.ID, photo.BaseVersion, err)
		return photo
	}
	refreshed := value.(model.FacePhoto)
	log.Printf("face_base_refresh activity_id=%d uid=%d photo_id=%d base_version=%d->%d result=ok",
		req.ActivityID, req.TargetUID, refreshed.ID, photo.BaseVersion, refreshed.BaseVersion)
	return refreshed
}

// beginFaceBaseRefresh claims the one attempt allowed per account cooldown.
// It records the attempt even when it later fails so signing cannot loop on a
// broken upstream download.
func (s *SignService) beginFaceBaseRefresh(uid int64) bool {
	s.faceBaseRefreshMu.Lock()
	defer s.faceBaseRefreshMu.Unlock()
	if s.faceBaseRefreshes == nil {
		s.faceBaseRefreshes = make(map[int64]time.Time)
	}
	now := time.Now()
	if last, ok := s.faceBaseRefreshes[uid]; ok && now.Sub(last) < faceBaseRefreshCooldown {
		return false
	}
	s.faceBaseRefreshes[uid] = now
	return true
}

// importOfficialForSign performs the one on-demand import allowed for this
// execution, bounded by the request context and facePhotoImportTimeout.
func (s *SignService) importOfficialForSign(req *ExecuteSignRequest) (model.FacePhoto, error) {
	if req.facePhotoImportDone {
		if req.facePhotoImportErr != nil {
			return model.FacePhoto{}, req.facePhotoImportErr
		}
		return model.FacePhoto{}, facePhotoUnavailableError{message: "该同学未设置人脸照片，请先在主页上传"}
	}
	req.facePhotoImportDone = true
	if s.facePhotos == nil {
		req.facePhotoImportErr = facePhotoUnavailableError{message: "该同学未设置人脸照片，请先在主页上传"}
		return model.FacePhoto{}, req.facePhotoImportErr
	}

	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, facePhotoImportTimeout)
	defer cancel()
	value, err, _ := s.faceImportGroup.Do(strconv.FormatInt(req.TargetUID, 10), func() (interface{}, error) {
		return s.facePhotos.ImportOfficial(ctx, req.TargetUID, req.TargetUID)
	})
	if err != nil {
		log.Printf("face photo official import failed: uid=%d err=%v", req.TargetUID, err)
		req.facePhotoImportErr = facePhotoUnavailableError{message: "该同学未设置人脸照片，且无法获取官方照片，请先在主页上传"}
		return model.FacePhoto{}, req.facePhotoImportErr
	}
	photo := value.(model.FacePhoto)
	log.Printf("face photo official import succeeded: uid=%d photo_id=%d", req.TargetUID, photo.ID)
	return photo, nil
}

func (s *SignService) recordFaceUse(req *ExecuteSignRequest, objectID string, failed bool) {
	if req.facePhotoCounted {
		if failed {
			MarkFacePhotoFailed(s.db, req.facePhotoID)
		}
		return
	}
	if err := RecordFacePhotoUse(s.db, req.facePhotoID, objectID, failed); err != nil {
		log.Printf("record face photo use: id=%d uid=%d err=%v", req.facePhotoID, req.TargetUID, err)
	}
	req.facePhotoCounted = true
}

// faceObjectID returns the uploaded objectId for one photo in one activity.
// The cache is keyed by the photo digest as well, so a replaced photo is
// uploaded again while repeated executions in the same activity reuse the
// existing objectId. Concurrent callers share a single upload.
func (s *SignService) faceObjectID(key faceUploadKey, upload func() (string, error)) (string, error) {
	if objectID, ok := s.cachedFaceObjectID(key); ok {
		return objectID, nil
	}
	flightKey := fmt.Sprintf("%d:%d:%x", key.UserUID, key.ActivityID, key.PhotoHash)
	value, err, _ := s.faceUploadGroup.Do(flightKey, func() (interface{}, error) {
		if objectID, ok := s.cachedFaceObjectID(key); ok {
			return objectID, nil
		}
		objectID, err := upload()
		if err != nil {
			return "", err
		}
		objectID = strings.TrimSpace(objectID)
		if objectID == "" {
			return "", errors.New("上传接口未返回 objectId")
		}
		s.storeFaceObjectID(key, objectID)
		return objectID, nil
	})
	if err != nil {
		return "", err
	}
	objectID, _ := value.(string)
	return objectID, nil
}

func (s *SignService) cachedFaceObjectID(key faceUploadKey) (string, bool) {
	s.faceUploadMu.Lock()
	defer s.faceUploadMu.Unlock()
	entry, ok := s.faceUploads[key]
	if !ok {
		return "", false
	}
	if time.Since(entry.UploadedAt) > faceUploadTTL {
		delete(s.faceUploads, key)
		return "", false
	}
	return entry.ObjectID, true
}

func (s *SignService) storeFaceObjectID(key faceUploadKey, objectID string) {
	s.faceUploadMu.Lock()
	defer s.faceUploadMu.Unlock()
	if s.faceUploads == nil {
		s.faceUploads = map[faceUploadKey]faceUploadEntry{}
	}
	now := time.Now()
	for cachedKey, entry := range s.faceUploads {
		if now.Sub(entry.UploadedAt) > faceUploadTTL {
			delete(s.faceUploads, cachedKey)
		}
	}
	s.faceUploads[key] = faceUploadEntry{ObjectID: objectID, UploadedAt: now}
}

func (s *SignService) recordSigned(req ExecuteSignRequest, operatorUID int64) (SignExecuteResult, error) {
	rec := s.signRecordFromRequest(req, operatorUID)
	dbResult := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_uid"}, {Name: "activity_id"}}, DoNothing: true}).Create(&rec)
	if dbResult.Error != nil {
		return SignExecuteResult{}, dbResult.Error
	}

	sourceName := s.getSourceName(operatorUID)
	if strings.TrimSpace(sourceName) == "" {
		sourceName = "未知用户"
	}
	if dbResult.RowsAffected > 0 {
		s.enqueueCourseSignSuccess(rec)
	}
	return SignExecuteResult{
		UserID:           req.TargetUID,
		Success:          true,
		AlreadySigned:    false,
		RecordSource:     operatorUID,
		RecordSourceName: sourceName,
		Message:          "签到成功",
	}, nil
}

// recordAlreadySigned stores 学习通's own sign as source_uid=-1 so later checks
// stay local and never ask upstream again.
func (s *SignService) recordAlreadySigned(req ExecuteSignRequest) (SignExecuteResult, error) {
	rec := s.signRecordFromRequest(req, -1)
	dbResult := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_uid"}, {Name: "activity_id"}}, DoNothing: true}).Create(&rec)
	if dbResult.Error != nil {
		return SignExecuteResult{}, dbResult.Error
	}
	return SignExecuteResult{
		UserID:           req.TargetUID,
		Success:          true,
		AlreadySigned:    true,
		RecordSource:     -1,
		RecordSourceName: "学习通",
		Message:          "该同学已在学习通签到",
	}, nil
}

func (s *SignService) enqueueCourseSignSuccess(rec model.SignRecord) {
	if !s.courseWebhook.Enabled() {
		return
	}
	key := courseSignNotificationKey{
		ActivityID: rec.ActivityID,
		CourseID:   rec.CourseID,
		ClassID:    rec.ClassID,
		SignType:   rec.SignType,
	}

	s.courseNotifyMu.Lock()
	batch := s.courseNotifyBatches[key]
	if batch == nil {
		batch = &courseSignNotificationBatch{}
		s.courseNotifyBatches[key] = batch
	}
	batch.Records = append(batch.Records, rec)
	batch.Version++
	version := batch.Version
	if batch.Timer != nil {
		batch.Timer.Stop()
	}
	batch.Timer = time.AfterFunc(courseSignNotificationDebounce, func() {
		s.flushCourseSignNotification(key, version)
	})
	s.courseNotifyMu.Unlock()
}

func (s *SignService) flushCourseSignNotification(key courseSignNotificationKey, version int) {
	s.courseNotifyMu.Lock()
	batch := s.courseNotifyBatches[key]
	if batch == nil || batch.Version != version {
		s.courseNotifyMu.Unlock()
		return
	}
	records := append([]model.SignRecord(nil), batch.Records...)
	delete(s.courseNotifyBatches, key)
	s.courseNotifyMu.Unlock()

	if len(records) == 0 || !s.courseWebhook.Enabled() {
		return
	}
	content, err := s.courseSignSummaryMarkdown(records)
	if err != nil {
		log.Printf("course sign webhook summary failed: %v", err)
		return
	}
	s.courseWebhook.SendMarkdownAsync("course sign", content)
}

func (s *SignService) courseSignSummaryMarkdown(records []model.SignRecord) (string, error) {
	users, err := s.courseSignUserMap(records)
	if err != nil {
		return "", err
	}

	first := records[0]
	courseName := webhookText(first.CourseName, "未知课程")
	activityName := webhookText(first.ActivityName, "未知活动")
	targetNames := make([]string, 0, len(records))
	sourceNames := make([]string, 0, len(records))
	firstTime := records[0].SignTimeMS
	lastTime := records[0].SignTimeMS
	for _, rec := range records {
		targetNames = append(targetNames, courseSignUserName(rec.UserUID, users))
		sourceNames = append(sourceNames, courseSignUserName(rec.SourceUID, users))
		if rec.SignTimeMS < firstTime {
			firstTime = rec.SignTimeMS
		}
		if rec.SignTimeMS > lastTime {
			lastTime = rec.SignTimeMS
		}
		if strings.TrimSpace(courseName) == "" || courseName == "未知课程" {
			courseName = webhookText(rec.CourseName, "未知课程")
		}
		if strings.TrimSpace(activityName) == "" || activityName == "未知活动" {
			activityName = webhookText(rec.ActivityName, "未知活动")
		}
	}

	timeText := time.UnixMilli(lastTime).Format("2006-01-02 15:04:05")
	if firstTime != lastTime {
		timeText = fmt.Sprintf("%s - %s", time.UnixMilli(firstTime).Format("2006-01-02 15:04:05"), time.UnixMilli(lastTime).Format("15:04:05"))
	}

	return strings.Join([]string{
		"### 课程签到成功",
		fmt.Sprintf(">课程：%s", courseName),
		fmt.Sprintf(">活动：%s", activityName),
		fmt.Sprintf(">结果：成功 %d 人", len(records)),
		fmt.Sprintf(">签到用户：%s", joinWebhookValues(targetNames, 20)),
		fmt.Sprintf(">执行人：%s", joinWebhookValues(sourceNames, 8)),
		fmt.Sprintf(">签到类型：%s", signTypeLabel(first.SignType)),
		fmt.Sprintf(">时间：%s", timeText),
		fmt.Sprintf(">活动 ID：%d", first.ActivityID),
		fmt.Sprintf(">课程/班级：%d / %d", first.CourseID, first.ClassID),
	}, "\n"), nil
}

func (s *SignService) courseSignUserMap(records []model.SignRecord) (map[int64]model.User, error) {
	seen := map[int64]struct{}{}
	uids := make([]int64, 0, len(records)*2)
	for _, rec := range records {
		for _, uid := range []int64{rec.UserUID, rec.SourceUID} {
			if uid <= 0 {
				continue
			}
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			uids = append(uids, uid)
		}
	}
	if len(uids) == 0 {
		return map[int64]model.User{}, nil
	}
	var users []model.User
	if err := s.db.Where("uid IN ?", uids).Find(&users).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]model.User, len(users))
	for _, user := range users {
		out[user.UID] = user
	}
	return out, nil
}

func courseSignUserName(uid int64, users map[int64]model.User) string {
	if user, ok := users[uid]; ok {
		return webhookText(user.Name, fmt.Sprintf("UID %d", uid))
	}
	return fmt.Sprintf("UID %d", uid)
}

func signTypeLabel(signType int) string {
	switch signType {
	case xxt.SignNormal:
		return "普通签到"
	case xxt.SignQRCode:
		return "二维码签到"
	case xxt.SignGesture:
		return "手势签到"
	case xxt.SignLocation:
		return "位置签到"
	case xxt.SignCode:
		return "签到码签到"
	default:
		return fmt.Sprintf("未知类型 %d", signType)
	}
}

func (s *SignService) signRecordFromRequest(req ExecuteSignRequest, sourceUID int64) model.SignRecord {
	return model.SignRecord{
		UserUID:       req.TargetUID,
		ActivityID:    req.ActivityID,
		SourceUID:     sourceUID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		SignType:      req.SignType,
		ActivityName:  strings.TrimSpace(req.ActivityName),
		CourseName:    strings.TrimSpace(req.CourseName),
		CourseTeacher: strings.TrimSpace(req.CourseTeacher),
		SignTimeMS:    time.Now().UnixMilli(),
	}
}

func (s *SignService) ExecutePhoto(operatorUID int64, req ExecutePhotoSignRequest) (SignExecuteResult, error) {
	if req.ActivityID <= 0 || req.CourseID <= 0 || req.ClassID <= 0 {
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "签到参数不完整"}, nil
	}
	if req.TargetUID <= 0 {
		req.TargetUID = operatorUID
	}
	if err := s.AuthorizeTargets(operatorUID, req.ActivityID, req.CourseID, req.ClassID, []int64{req.TargetUID}); err != nil {
		return SignExecuteResult{}, err
	}
	state, err := s.resolveSignState(req.ActivityID, req.TargetUID)
	if err != nil {
		return SignExecuteResult{}, err
	}
	if state.Signed {
		return SignExecuteResult{
			UserID:           req.TargetUID,
			Success:          true,
			AlreadySigned:    true,
			RecordSource:     state.RecordSource,
			RecordSourceName: state.RecordSourceName,
			Message:          state.Message,
		}, nil
	}

	objectID := strings.TrimSpace(req.ObjectID)
	if objectID == "" {
		if len(req.Photo) == 0 {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "请上传照片或传入 object_id"}, nil
		}
		var target model.User
		if err := s.db.Where("uid = ?", req.TargetUID).First(&target).Error; err != nil {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "该同学未登录或账号不可用"}, nil
		}
		password, err := s.cc.Decrypt(target.CredentialCipher)
		if err != nil {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "该同学登录信息已过期，请先重新登录"}, nil
		}
		objectID, err = s.xxt.UploadPanFile(target.Mobile, password, req.Filename, req.ContentType, req.Photo)
		if err != nil {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "照片上传失败：" + err.Error()}, nil
		}
	}

	special := map[string]interface{}{"object_id": objectID}
	if req.IfFace {
		special["if_face"] = true
	}
	return s.ExecuteOne(operatorUID, ExecuteSignRequest{
		ActivityID:    req.ActivityID,
		TargetUID:     req.TargetUID,
		SignType:      xxt.SignNormal,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		IfRefreshEWM:  req.IfRefreshEWM,
		ActivityName:  req.ActivityName,
		CourseName:    req.CourseName,
		CourseTeacher: req.CourseTeacher,
		Special:       special,
		Context:       req.Context,
	})
}

func (s *SignService) toUserSignMessage(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "签到失败，请稍后重试"
	}

	lower := strings.ToLower(msg)
	switch {
	case msg == "validate" || strings.Contains(lower, "validate"):
		return "签到校验未通过，请重试"
	case strings.Contains(msg, "验证码识别失败") || strings.Contains(lower, "captcha"):
		return "验证码校验失败，请重试"
	case strings.Contains(msg, "缺少二维码 enc 参数"):
		return "二维码参数缺失，请刷新活动后重试"
	case strings.Contains(msg, "缺少 sign_code 参数"):
		return "签到码缺失，请输入后重试"
	case strings.Contains(msg, "请求过于频繁"):
		return "操作太频繁，请稍后再试"
	case strings.Contains(msg, "活动已结束"):
		return "该签到已结束"
	case strings.Contains(msg, "签到成功"):
		return "签到成功"
	case strings.Contains(msg, "您已签到过了"):
		return "该同学已在学习通签到"
	default:
		return msg
	}
}

func (s *SignService) resolveSignState(activityID, uid int64) (SignCheckItem, error) {
	state := SignCheckItem{UserID: uid, Signed: false, RecordSource: 0, RecordSourceName: "", Message: "未签到"}
	if activityID <= 0 || uid <= 0 {
		return state, nil
	}

	var rec model.SignRecord
	if err := s.db.Where("user_uid = ? AND activity_id = ?", uid, activityID).Take(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, nil
		}
		return SignCheckItem{}, err
	}

	state.Signed = true
	state.RecordSource = rec.SourceUID
	if rec.SourceUID == -1 {
		state.RecordSourceName = "学习通"
		state.Message = "该同学已在学习通签到"
		return state, nil
	}
	if rec.SourceUID == uid {
		state.RecordSourceName = s.getSourceName(uid)
		if state.RecordSourceName == "" {
			state.RecordSourceName = "本人"
		}
		state.Message = "该同学已本人签到"
		return state, nil
	}
	state.RecordSourceName = s.getSourceName(rec.SourceUID)
	if state.RecordSourceName == "" {
		state.RecordSourceName = "未知用户"
	}
	state.Message = fmt.Sprintf("该同学已被%s代签", state.RecordSourceName)
	return state, nil
}

func (s *SignService) getSourceName(sourceUID int64) string {
	if sourceUID <= 0 {
		return ""
	}
	var user model.User
	if err := s.db.Where("uid = ?", sourceUID).Take(&user).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(user.Name)
}

func dedupeUIDs(userIDs []int64) []int64 {
	set := make(map[int64]struct{}, len(userIDs))
	out := make([]int64, 0, len(userIDs))
	for _, uid := range userIDs {
		if uid <= 0 {
			continue
		}
		if _, ok := set[uid]; ok {
			continue
		}
		set[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}

type ContributionGroup struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type ContributionTarget struct {
	TargetUID  int64  `json:"target_uid"`
	TargetName string `json:"target_name"`
	Count      int64  `json:"count"`
}

type ContributionSource struct {
	SourceUID    int64                `json:"source_uid"`
	SourceName   string               `json:"source_name"`
	SourceAvatar string               `json:"source_avatar"`
	Total        int64                `json:"total"`
	Details      []ContributionTarget `json:"details"`
}

type ContributionBoard struct {
	Group *ContributionGroup   `json:"group"`
	Items []ContributionSource `json:"items"`
}

// ClassContributions builds an in-class proxy-sign leaderboard for the viewer's
// own class group. Only records where BOTH the proxy signer (source_uid) and the
// target (user_uid) belong to the viewer's group are counted; self signs
// (source_uid == user_uid) and 学习通自签 (source_uid == -1) are excluded.
func (s *SignService) ClassContributions(operatorUID int64) (ContributionBoard, error) {
	if _, err := LoadActiveUser(s.db, operatorUID); err != nil {
		return ContributionBoard{}, err
	}

	var member model.ClassGroupMember
	if err := s.db.Where("user_uid = ?", operatorUID).Take(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ContributionBoard{Group: nil, Items: []ContributionSource{}}, nil
		}
		return ContributionBoard{}, err
	}

	var group model.ClassGroup
	if err := s.db.First(&group, member.GroupID).Error; err != nil {
		return ContributionBoard{}, err
	}

	var rows []struct {
		SourceUID    int64
		SourceName   string
		SourceAvatar string
		TargetUID    int64
		TargetName   string
		Count        int64
	}
	if err := s.db.Table("sign_records sr").
		Select(`sr.source_uid AS source_uid,
			COALESCE(NULLIF(su.name, ''), 'UID ' || sr.source_uid::text) AS source_name,
			COALESCE(su.avatar, '') AS source_avatar,
			sr.user_uid AS target_uid,
			COALESCE(NULLIF(tu.name, ''), 'UID ' || sr.user_uid::text) AS target_name,
			COUNT(*) AS count`).
		Joins("JOIN class_group_members sm ON sm.user_uid = sr.source_uid AND sm.group_id = ?", member.GroupID).
		Joins("JOIN class_group_members tm ON tm.user_uid = sr.user_uid AND tm.group_id = ?", member.GroupID).
		Joins("LEFT JOIN users su ON su.uid = sr.source_uid").
		Joins("LEFT JOIN users tu ON tu.uid = sr.user_uid").
		Where("sr.source_uid <> sr.user_uid AND sr.source_uid > 0").
		Group("sr.source_uid, su.name, su.avatar, sr.user_uid, tu.name").
		Scan(&rows).Error; err != nil {
		return ContributionBoard{}, err
	}

	sourceMap := make(map[int64]*ContributionSource, len(rows))
	for _, row := range rows {
		src, ok := sourceMap[row.SourceUID]
		if !ok {
			src = &ContributionSource{
				SourceUID:    row.SourceUID,
				SourceName:   row.SourceName,
				SourceAvatar: row.SourceAvatar,
				Details:      []ContributionTarget{},
			}
			sourceMap[row.SourceUID] = src
		}
		src.Details = append(src.Details, ContributionTarget{
			TargetUID:  row.TargetUID,
			TargetName: row.TargetName,
			Count:      row.Count,
		})
		src.Total += row.Count
	}

	items := make([]ContributionSource, 0, len(sourceMap))
	for _, src := range sourceMap {
		details := src.Details
		sort.Slice(details, func(i, j int) bool {
			if details[i].Count != details[j].Count {
				return details[i].Count > details[j].Count
			}
			return details[i].TargetUID < details[j].TargetUID
		})
		items = append(items, *src)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Total != items[j].Total {
			return items[i].Total > items[j].Total
		}
		return items[i].SourceUID < items[j].SourceUID
	})

	return ContributionBoard{Group: &ContributionGroup{ID: group.ID, Name: group.Name}, Items: items}, nil
}
