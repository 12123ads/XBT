package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/im"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const maxPhotoUploadBytes int64 = 20 << 20

type selectedCourseInfo struct {
	CourseID int64
	ClassID  int64
	Name     string
	Teacher  string
	Icon     string
}

type signActivityClient interface {
	GetActives(mobile, password string, courseID, classID int64) ([]xxt.Active, error)
	GetSignDetail(mobile, password string, activityID, courseID, classID, uid int64) (xxt.SignDetail, error)
	GetFaceFlag(mobile, password string, activityID, courseID, classID, uid int64) bool
}

// imActivitySource exposes the last polling snapshot. SignHandler treats IM
// discovery as additive: a nil source or a failed poll never fails the normal
// taskactivelist response.
type imActivitySource interface {
	Snapshot(uid int64) service.IMSnapshot
	RefreshIfStale(ctx context.Context, uid int64) service.IMSnapshot
}

type SignHandler struct {
	db          *gorm.DB
	xxt         signActivityClient
	cc          *service.CredentialCrypto
	signService *service.SignService
	listLimit   int
	settings    *service.RuntimeSettingsService
	imSource    imActivitySource
}

func NewSignHandler(db *gorm.DB, xxtClient signActivityClient, cc *service.CredentialCrypto, signService *service.SignService, listLimit int, settings *service.RuntimeSettingsService) *SignHandler {
	if listLimit <= 0 {
		listLimit = 5
	}
	return &SignHandler{db: db, xxt: xxtClient, cc: cc, signService: signService, listLimit: listLimit, settings: settings}
}

func (h *SignHandler) SetIMSignSource(source imActivitySource) {
	h.imSource = source
}

func (h *SignHandler) LocationPresets(c *gin.Context) {
	settings, err := h.settings.Settings()
	if err != nil {
		common.Fail(c, 500, "query location presets failed")
		return
	}
	common.Success(c, gin.H{"items": settings.CourseLocationPresets})
}

func (h *SignHandler) Activities(c *gin.Context) {
	uid := common.GetUserUID(c)
	var user model.User
	if err := h.db.Where("uid = ?", uid).First(&user).Error; err != nil {
		common.Fail(c, 404, "user not found")
		return
	}
	password, err := h.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		common.Fail(c, 400, "credential expired, please login again")
		return
	}

	var selected []selectedCourseInfo
	err = h.db.Table("user_courses uc").
		Select("uc.course_id, uc.class_id, c.name, c.teacher, c.icon").
		Joins("join courses c on uc.course_id = c.course_id and uc.class_id = c.class_id").
		Where("uc.user_uid = ? and uc.is_selected = true", uid).
		Scan(&selected).Error
	if err != nil {
		common.Fail(c, 500, "query selected courses failed")
		return
	}
	log.Printf("activities query: uid=%d selected_courses=%d", uid, len(selected))

	resp := make([]gin.H, 0, len(selected))
	var respMu sync.Mutex
	var eg errgroup.Group
	for _, sc := range selected {
		sc := sc
		eg.Go(func() error {
			group, err := h.buildCourseActivityGroup(uid, user, password, sc)
			if err != nil {
				return err
			}
			if group == nil {
				return nil
			}
			respMu.Lock()
			resp = append(resp, group)
			respMu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		if isXXTAuthError(err) {
			common.Fail(c, 401, "学习通登录已失效，请使用新密码重新登录")
			return
		}
		common.Fail(c, 500, err.Error())
		return
	}

	if h.imSource != nil && len(selected) > 0 {
		snapshot := h.imSource.RefreshIfStale(c.Request.Context(), uid)
		h.mergeIMActivities(uid, user, password, selected, resp, snapshot)
	}

	// 课程分组按“组内最新活动时间”倒序
	sort.Slice(resp, func(i, j int) bool {
		getLatestStart := func(group gin.H) int64 {
			activitiesAny, ok := group["activities"]
			if !ok {
				return 0
			}
			activities, ok := activitiesAny.([]gin.H)
			if !ok || len(activities) == 0 {
				return 0
			}
			startAny, ok := activities[0]["start_time"]
			if !ok {
				return 0
			}
			start, ok := startAny.(int64)
			if !ok {
				return 0
			}
			return start
		}
		return getLatestStart(resp[i]) > getLatestStart(resp[j])
	})
	log.Printf("activities response: uid=%d course_groups=%d", uid, len(resp))
	common.Success(c, resp)
}

func (h *SignHandler) buildCourseActivityGroup(uid int64, user model.User, password string, sc selectedCourseInfo) (gin.H, error) {
	actives, err := h.xxt.GetActives(user.Mobile, password, sc.CourseID, sc.ClassID)
	if err != nil {
		log.Printf("get actives failed: uid=%d course=%d class=%d err=%v", uid, sc.CourseID, sc.ClassID, err)
		return nil, err
	}
	log.Printf("actives fetched: uid=%d course=%d class=%d count=%d", uid, sc.CourseID, sc.ClassID, len(actives))
	scopes := make([]model.SignActivityScope, 0, len(actives))
	for _, active := range actives {
		scopes = append(scopes, model.SignActivityScope{
			ActivityID: active.ActiveID,
			CourseID:   sc.CourseID,
			ClassID:    sc.ClassID,
		})
	}
	if len(scopes) > 0 {
		if err := h.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&scopes).Error; err != nil {
			return nil, err
		}
	}
	items := make([]gin.H, 0)
	for _, a := range actives {
		var cache model.SignActivity
		cacheErr := h.db.Where("activity_id = ?", a.ActiveID).First(&cache).Error
		cacheFound := cacheErr == nil
		if cacheErr != nil && !errors.Is(cacheErr, gorm.ErrRecordNotFound) {
			log.Printf("query sign activity cache failed: uid=%d activity=%d err=%v", uid, a.ActiveID, cacheErr)
		}

		detail, err := h.xxt.GetSignDetail(user.Mobile, password, a.ActiveID, sc.CourseID, sc.ClassID, uid)
		if err == nil {
			cacheRow := model.SignActivity{
				ActivityID:   a.ActiveID,
				StartTime:    detail.StartTime,
				EndTime:      detail.EndTime,
				SignType:     detail.SignType,
				IfRefreshEWM: detail.IfRefreshEWM,
				IfPhoto:      detail.IfPhoto,
				IfFace:       detail.IfFace,
				ActivityType: a.Type,
				Status:       a.Status,
				UserStatus:   a.UserStatus,
				ExtJSON:      activityExt(detail, a),
			}
			if createErr := h.db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "activity_id"}},
				DoNothing: true,
			}).Create(&cacheRow).Error; createErr != nil {
				log.Printf("save sign activity cache failed: uid=%d activity=%d err=%v", uid, a.ActiveID, createErr)
			}
		} else {
			log.Printf("get sign detail failed: uid=%d activity=%d err=%v", uid, a.ActiveID, err)
			if cacheFound {
				detail = signDetailFromCache(cache)
				if !detail.IfFace && h.xxt.GetFaceFlag(user.Mobile, password, a.ActiveID, sc.CourseID, sc.ClassID, uid) {
					detail.IfFace = true
					h.db.Model(&model.SignActivity{}).Where("activity_id = ?", a.ActiveID).Update("if_face", true)
				}
			}
		}

		// Keep the cache aligned with the discovery list. These fields are not
		// returned by the metadata endpoints and must survive into S4 preSign.
		h.syncActivityDiscoveryCache(a, cacheFound, cache, activityExt(detail, a))

		// An activity discovered without usable metadata is still listed and
		// marked (not_started/ongoing/expired); it is never silently dropped.
		if detail.StartTime == 0 {
			detail.StartTime = a.StartTime
		}
		if detail.EndTime == 0 {
			detail.EndTime = a.EndTime
		}
		if detail.SignType == 0 {
			detail.SignType = a.SignType
		}

		recordSource, recordSourceName, recordSignTime := h.signRecordView(uid, a.ActiveID, user)
		items = append(items, gin.H{
			"active_id":          a.ActiveID,
			"activity_name":      a.Name,
			"type":               a.Type,
			"status":             a.Status,
			"user_status":        a.UserStatus,
			"start_time":         detail.StartTime,
			"end_time":           detail.EndTime,
			"sign_type":          detail.SignType,
			"if_refresh_ewm":     detail.IfRefreshEWM,
			"if_photo":           detail.IfPhoto,
			"if_face":            detail.IfFace,
			"if_need_vcode":      detail.IfNeedVCode,
			"if_open_address":    detail.IfOpenAddress,
			"location_latitude":  detail.LocationLatitude,
			"location_longitude": detail.LocationLongitude,
			"location_range":     detail.LocationRange,
			"record_source":      recordSource,
			"record_source_name": recordSourceName,
			"record_sign_time":   recordSignTime,
			"course_name":        sc.Name,
			"course_id":          sc.CourseID,
			"class_id":           sc.ClassID,
			"course_teacher":     sc.Teacher,
			"source":             im.SourceTaskActiveList,
			"sources":            []string{im.SourceTaskActiveList},
		})
	}
	group := gin.H{
		"course_id":      sc.CourseID,
		"class_id":       sc.ClassID,
		"course_name":    sc.Name,
		"course_teacher": sc.Teacher,
		"icon":           sc.Icon,
		"activities":     items,
	}
	finalizeActivityGroup(group, h.listLimit)
	return group, nil
}

func signDetailFromCache(cache model.SignActivity) xxt.SignDetail {
	return xxt.SignDetail{
		StartTime:    cache.StartTime,
		EndTime:      cache.EndTime,
		SignType:     cache.SignType,
		IfRefreshEWM: cache.IfRefreshEWM,
		IfPhoto:      cache.IfPhoto,
		IfFace:       cache.IfFace,
		Ext:          cache.ExtJSON,
	}
}

func (h *SignHandler) Classmates(c *gin.Context) {
	uid := common.GetUserUID(c)
	courseID, courseErr := strconv.ParseInt(c.Query("course_id"), 10, 64)
	classID, classErr := strconv.ParseInt(c.Query("class_id"), 10, 64)
	if courseErr != nil || classErr != nil || courseID <= 0 || classID <= 0 {
		common.Fail(c, 400, "positive course_id and class_id are required")
		return
	}
	database := h.db.WithContext(c.Request.Context())
	if _, err := service.LoadActiveUser(database, uid); err != nil {
		h.failSign(c, err)
		return
	}
	selected, err := service.UserSelectedCourse(database, uid, courseID, classID)
	if err != nil {
		h.failSign(c, err)
		return
	}
	if !selected {
		h.failSign(c, service.ErrSignForbidden)
		return
	}

	var mates []struct {
		UID    int64
		Name   string
		Mobile string
		Avatar string
	}
	err = database.Table("users u").
		Select("u.uid, u.name, u.mobile, u.avatar").
		Joins("join user_courses uc on u.uid = uc.user_uid").
		Joins("join whitelists w on w.mobile = u.mobile").
		Where("u.uid > 0 and u.permission > 0 and w.permission > 0").
		Where("uc.course_id = ? and uc.class_id = ? and uc.is_selected = true and u.uid <> ?", courseID, classID, uid).
		Order("u.name asc").
		Scan(&mates).Error
	if err != nil {
		common.Fail(c, 500, "query classmates failed")
		return
	}
	resp := make([]gin.H, 0, len(mates))
	for _, m := range mates {
		resp = append(resp, gin.H{
			"uid":           m.UID,
			"name":          m.Name,
			"mobile_masked": common.MaskMobile(m.Mobile),
			"avatar":        m.Avatar,
		})
	}
	common.Success(c, resp)
}

func (h *SignHandler) Execute(c *gin.Context) {
	uid := common.GetUserUID(c)
	var req dto.SignExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if req.ActivityID <= 0 || req.CourseID <= 0 || req.ClassID <= 0 {
		common.Fail(c, 400, "invalid request params")
		return
	}
	switch req.SignType {
	case xxt.SignNormal, xxt.SignQRCode, xxt.SignGesture, xxt.SignLocation, xxt.SignCode:
	default:
		common.Fail(c, 400, "unsupported sign_type")
		return
	}
	targetUID := req.TargetUID
	if targetUID <= 0 && len(req.UserIDs) > 0 {
		targetUID = req.UserIDs[0]
	}
	if targetUID <= 0 {
		targetUID = uid
	}
	if req.Special == nil {
		req.Special = map[string]interface{}{}
	}
	res, err := h.signService.ExecuteOne(uid, service.ExecuteSignRequest{
		ActivityID:    req.ActivityID,
		TargetUID:     targetUID,
		SignType:      req.SignType,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		IfRefreshEWM:  req.IfRefreshEWM,
		IfOpenAddress: req.IfOpenAddress,
		ActivityName:  req.ActivityName,
		CourseName:    req.CourseName,
		CourseTeacher: req.CourseTeacher,
		Special:       req.Special,
	})
	if err != nil {
		h.failSign(c, err)
		return
	}
	common.Success(c, res)
}

func (h *SignHandler) SignOut(c *gin.Context) {
	uid := common.GetUserUID(c)
	var req dto.SignOutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if req.ActivityID <= 0 || req.CourseID <= 0 || req.ClassID <= 0 {
		common.Fail(c, 400, "invalid request params")
		return
	}
	targetUID := req.TargetUID
	if targetUID <= 0 {
		targetUID = uid
	}
	if req.Special == nil {
		req.Special = map[string]interface{}{}
	}
	res, err := h.signService.SignOutOne(uid, service.SignOutRequest{
		ActivityID:    req.ActivityID,
		TargetUID:     targetUID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		IfRefreshEWM:  req.IfRefreshEWM,
		IfOpenAddress: req.IfOpenAddress,
		ActivityName:  req.ActivityName,
		CourseName:    req.CourseName,
		CourseTeacher: req.CourseTeacher,
		Special:       req.Special,
	})
	if err != nil {
		h.failSign(c, err)
		return
	}
	common.Success(c, res)
}

func (h *SignHandler) Photo(c *gin.Context) {
	uid := common.GetUserUID(c)
	activityID, err := requiredInt64Form(c, "activity_id")
	if err != nil {
		common.Fail(c, 400, "activity_id is required")
		return
	}
	courseID, err := requiredInt64Form(c, "course_id")
	if err != nil {
		common.Fail(c, 400, "course_id is required")
		return
	}
	classID, err := requiredInt64Form(c, "class_id")
	if err != nil {
		common.Fail(c, 400, "class_id is required")
		return
	}
	targetUID := optionalInt64Form(c, "target_uid")
	if targetUID <= 0 {
		targetUID = uid
	}

	objectID := strings.TrimSpace(c.PostForm("object_id"))
	if objectID == "" {
		objectID = strings.TrimSpace(c.PostForm("objectId"))
	}

	var photo []byte
	filename := ""
	contentType := ""
	fileHeader, fileErr := c.FormFile("file")
	if fileErr == nil {
		if fileHeader.Size > maxPhotoUploadBytes {
			common.Fail(c, 400, "photo file is too large")
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			common.Fail(c, 400, "open photo file failed")
			return
		}
		defer file.Close()
		photo, err = io.ReadAll(io.LimitReader(file, maxPhotoUploadBytes+1))
		if err != nil {
			common.Fail(c, 400, "read photo file failed")
			return
		}
		if int64(len(photo)) > maxPhotoUploadBytes {
			common.Fail(c, 400, "photo file is too large")
			return
		}
		filename = fileHeader.Filename
		contentType = fileHeader.Header.Get("Content-Type")
	} else if objectID == "" {
		if errors.Is(fileErr, http.ErrMissingFile) {
			common.Fail(c, 400, "file or object_id is required")
			return
		}
		common.Fail(c, 400, "invalid photo upload")
		return
	}

	res, err := h.signService.ExecutePhoto(uid, service.ExecutePhotoSignRequest{
		ActivityID:    activityID,
		TargetUID:     targetUID,
		CourseID:      courseID,
		ClassID:       classID,
		IfRefreshEWM:  optionalBoolForm(c, "if_refresh_ewm"),
		IfFace:        optionalBoolForm(c, "if_face"),
		ActivityName:  strings.TrimSpace(c.PostForm("activity_name")),
		CourseName:    strings.TrimSpace(c.PostForm("course_name")),
		CourseTeacher: strings.TrimSpace(c.PostForm("course_teacher")),
		ObjectID:      objectID,
		Filename:      filename,
		ContentType:   contentType,
		Photo:         photo,
	})
	if err != nil {
		h.failSign(c, err)
		return
	}
	common.Success(c, res)
}

func (h *SignHandler) Check(c *gin.Context) {
	uid := common.GetUserUID(c)
	var req dto.SignCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	items, err := h.signService.CheckSignStates(uid, req.ActivityID, req.CourseID, req.ClassID, req.UserIDs)
	if err != nil {
		h.failSign(c, err)
		return
	}
	common.Success(c, gin.H{"items": items})
}

func (h *SignHandler) Features(c *gin.Context) {
	settings, err := h.settings.Settings()
	if err != nil {
		common.Fail(c, 500, "query features failed")
		return
	}
	resp := gin.H{"contributions_enabled": settings.ContributionsEnabled}
	var group model.ClassGroup
	if err := h.db.Where("admin_uid = ?", common.GetUserUID(c)).Order("id asc").First(&group).Error; err == nil {
		resp["class_admin_group_id"] = group.ID
		resp["class_admin_group_name"] = group.Name
	}
	common.Success(c, resp)
}

func (h *SignHandler) Contributions(c *gin.Context) {
	settings, err := h.settings.Settings()
	if err != nil {
		common.Fail(c, 500, "query settings failed")
		return
	}
	if !settings.ContributionsEnabled {
		common.Fail(c, 403, "贡献榜未开启")
		return
	}
	uid := common.GetUserUID(c)
	board, err := h.signService.ClassContributions(uid)
	if err != nil {
		h.failSign(c, err)
		return
	}
	common.Success(c, board)
}

func (h *SignHandler) failSign(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAccountInactive):
		common.Fail(c, 401, "account inactive")
	case errors.Is(err, service.ErrSignForbidden):
		common.Fail(c, 403, "无权操作该课程活动或目标账号")
	case errors.Is(err, service.ErrActivityScopeUnavailable):
		common.Fail(c, 502, "暂时无法确认活动所属课程，请稍后重试")
	default:
		common.Fail(c, 500, "sign operation failed")
	}
}

func requiredInt64Form(c *gin.Context, key string) (int64, error) {
	raw := strings.TrimSpace(c.PostForm(key))
	if raw == "" {
		return 0, errors.New("missing form field")
	}
	return strconv.ParseInt(raw, 10, 64)
}

func optionalInt64Form(c *gin.Context, key string) int64 {
	raw := strings.TrimSpace(c.PostForm(key))
	if raw == "" {
		return 0
	}
	value, _ := strconv.ParseInt(raw, 10, 64)
	return value
}

func optionalBoolForm(c *gin.Context, key string) bool {
	raw := strings.TrimSpace(c.PostForm(key))
	if raw == "" {
		return false
	}
	value, err := strconv.ParseBool(raw)
	if err == nil {
		return value
	}
	return raw == "1"
}
