package service

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type signClient interface {
	PreSign(mobile, password string, fixed xxt.FixedParams, signType int, code, enc string) (xxt.PreSignResult, error)
	Sign(mobile, password string, fixed xxt.FixedParams, signType int, special map[string]interface{}) (xxt.SignResult, error)
	GetSignDetail(mobile, password string, activityID, courseID, classID, uid int64) (xxt.SignDetail, error)
	HasActivityInCourse(mobile, password string, courseID, classID, activityID int64) (bool, error)
	UploadPanFile(mobile, password, filename, contentType string, data []byte) (string, error)
	UploadFaceTemp(mobile, password, filename, contentType string, data []byte) (string, error)
	CheckFaceResult(mobile, password string, activeID int64, faceObjectID string) (xxt.FaceCheckResult, error)
}

type SignService struct {
	db                  *gorm.DB
	xxt                 signClient
	cc                  *CredentialCrypto
	courseWebhook       *EnterpriseWechatWebhookNotifier
	courseNotifyMu      sync.Mutex
	courseNotifyBatches map[courseSignNotificationKey]*courseSignNotificationBatch
}

func NewSignService(db *gorm.DB, xxtClient signClient, cc *CredentialCrypto, courseWebhook *EnterpriseWechatWebhookNotifier) *SignService {
	return &SignService{
		db:                  db,
		xxt:                 xxtClient,
		cc:                  cc,
		courseWebhook:       courseWebhook,
		courseNotifyBatches: map[courseSignNotificationKey]*courseSignNotificationBatch{},
	}
}

const courseSignNotificationDebounce = 8 * time.Second

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
	SignErrorNone             = ""
	SignErrorCaptchaRequired  = "captcha_required"
	SignErrorFaceRequired     = "face_required"
	SignErrorLocationOutRange = "location_out_of_range"
	SignErrorQRExpired        = "qr_expired"
	SignErrorActivityExpired  = "activity_expired"
	SignErrorSessionExpired   = "session_expired"
	SignErrorUpstream         = "upstream_error"
)

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

func (s *SignService) ExecuteOne(operatorUID int64, req ExecuteSignRequest) (SignExecuteResult, error) {
	if err := s.AuthorizeTargets(operatorUID, req.ActivityID, req.CourseID, req.ClassID, []int64{req.TargetUID}); err != nil {
		return SignExecuteResult{}, err
	}
	if req.Special == nil {
		req.Special = map[string]interface{}{}
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
	if detail, err := s.xxt.GetSignDetail(target.Mobile, password, req.ActivityID, req.CourseID, req.ClassID, req.TargetUID); err == nil {
		fixed.IfOpenAddress = fixed.IfOpenAddress || detail.IfOpenAddress
		fixed.Ext = detail.Ext
	} else {
		log.Printf("sign metadata lookup failed: activity=%d uid=%d err=%v", req.ActivityID, req.TargetUID, err)
	}

	enc, _ := req.Special["enc"].(string)
	code, _ := req.Special["c"].(string)
	preSign, err := s.xxt.PreSign(target.Mobile, password, fixed, req.SignType, code, enc)
	if err != nil {
		if errors.Is(err, xxt.ErrSessionExpired) {
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
			return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorFaceRequired, Message: err.Error()}, nil
		}
	}

	// Bounded state machine: captcha is resolved inside xxt.Sign (at most one
	// extra request), face and location recoveries happen here and each may
	// retry once. Every other outcome ends the attempt.
	faceRetried := false
	locationRetried := false
	var result xxt.SignResult
	for {
		result, err = s.xxt.Sign(target.Mobile, password, fixed, req.SignType, req.Special)
		if err != nil {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: s.toUserSignMessage(err.Error()), ErrorCode: SignErrorUpstream, Retryable: true}, nil
		}
		switch {
		case result.Kind == xxt.SignResultFaceRequired && !faceRetried:
			faceRetried = true
			if err := s.applyFaceCredentials(&target, password, &req); err != nil {
				return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorFaceRequired, Message: err.Error()}, nil
			}
			if result.Enc2 != "" {
				req.Special["enc2"] = result.Enc2
			}
			continue
		case result.Kind == xxt.SignResultLocationOutOfRange && !locationRetried:
			// XBT-S6 introduces point sampling; until then the retry follows
			// the protocol (one retry with randomization disabled) but sends
			// the same selected point.
			locationRetried = true
			continue
		}
		break
	}

	switch result.Kind {
	case xxt.SignResultSuccess, xxt.SignResultSuccess2:
		return s.recordSigned(req, operatorUID)
	case xxt.SignResultAlreadySigned:
		return s.recordAlreadySigned(req)
	case xxt.SignResultCaptchaRequired:
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorCaptchaRequired, Retryable: true, Message: "验证码校验未通过，请重试"}, nil
	case xxt.SignResultLocationOutOfRange:
		message := "签到位置超出范围，请重新选择位置"
		if result.DistanceM > 0 {
			message = fmt.Sprintf("签到位置超出范围，距签到点%.0f米，请重新选择位置", result.DistanceM)
		}
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorLocationOutRange, DistanceM: result.DistanceM, Message: message}, nil
	case xxt.SignResultQRExpired:
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorQRExpired, Message: "二维码已过期，请重新扫码"}, nil
	case xxt.SignResultSessionExpired:
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorSessionExpired, Message: "学习通登录已过期，请重新登录"}, nil
	case xxt.SignResultFaceRequired:
		message := "人脸验证未通过，请更新人脸照片后重试"
		if strings.TrimSpace(result.Message) != "" {
			message = "人脸验证未通过：" + strings.TrimSpace(result.Message)
		}
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorFaceRequired, Message: message}, nil
	default:
		return SignExecuteResult{UserID: req.TargetUID, Success: false, ErrorCode: SignErrorUpstream, Retryable: true, Message: s.toUserSignMessage(result.Message)}, nil
	}
}

// applyFaceCredentials uploads the stored face photo and obtains the face
// challenge fields required by stuSignajax. It is used both before the first
// attempt and for the single face recovery after [face]/checkFace_.
func (s *SignService) applyFaceCredentials(target *model.User, password string, req *ExecuteSignRequest) error {
	if len(target.FacePhoto) == 0 {
		var fresh model.User
		if err := s.db.Select("face_photo").Where("uid = ?", req.TargetUID).First(&fresh).Error; err == nil {
			target.FacePhoto = fresh.FacePhoto
		}
	}
	if len(target.FacePhoto) == 0 {
		return errors.New("该同学未设置人脸照片，请先在主页上传")
	}
	objectID, err := s.xxt.UploadFaceTemp(target.Mobile, password, "face.jpg", "image/jpeg", target.FacePhoto)
	if err != nil {
		return fmt.Errorf("人脸照片上传失败：%v", err)
	}
	faceResult, err := s.xxt.CheckFaceResult(target.Mobile, password, req.ActivityID, objectID)
	if err != nil {
		return fmt.Errorf("人脸验证失败：%v", err)
	}
	req.Special["current_face_id"] = faceResult.CurrentFaceID
	req.Special["face_enc"] = faceResult.FaceEnc
	req.Special["face_code"] = faceResult.FaceCode
	return nil
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
