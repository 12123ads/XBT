package handler

import (
	"errors"
	"log"
	"sort"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const manualEndTimeSentinel int64 = 64060559999000

type selectedCourseInfo struct {
	CourseID int64
	ClassID  int64
	Name     string
	Teacher  string
	Icon     string
}

type SignHandler struct {
	db          *gorm.DB
	xxt         *xxt.Client
	cc          *service.CredentialCrypto
	signService *service.SignService
	listLimit   int
	settings    *service.RuntimeSettingsService
}

func NewSignHandler(db *gorm.DB, xxtClient *xxt.Client, cc *service.CredentialCrypto, signService *service.SignService, listLimit int, settings *service.RuntimeSettingsService) *SignHandler {
	if listLimit <= 0 {
		listLimit = 5
	}
	return &SignHandler{db: db, xxt: xxtClient, cc: cc, signService: signService, listLimit: listLimit, settings: settings}
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
		var detail xxt.SignDetail
		var cache model.SignActivity
		cacheErr := h.db.Where("activity_id = ?", a.ActiveID).First(&cache).Error
		cacheFound := cacheErr == nil

		if cacheFound && cache.EndTime != manualEndTimeSentinel {
			detail = xxt.SignDetail{
				StartTime:    cache.StartTime,
				EndTime:      cache.EndTime,
				SignType:     cache.SignType,
				IfRefreshEWM: cache.IfRefreshEWM,
			}
		} else {
			remoteDetail, err := h.xxt.GetSignDetail(user.Mobile, password, a.ActiveID)
			if err == nil {
				detail = remoteDetail
				cacheRow := model.SignActivity{
					ActivityID:   a.ActiveID,
					StartTime:    detail.StartTime,
					EndTime:      detail.EndTime,
					SignType:     detail.SignType,
					IfRefreshEWM: detail.IfRefreshEWM,
				}
				_ = h.db.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "activity_id"}},
					DoNothing: true,
				}).Create(&cacheRow).Error
			} else {
				log.Printf("get sign detail failed: uid=%d activity=%d err=%v", uid, a.ActiveID, err)
				if cacheFound {
					detail = xxt.SignDetail{
						StartTime:    cache.StartTime,
						EndTime:      cache.EndTime,
						SignType:     cache.SignType,
						IfRefreshEWM: cache.IfRefreshEWM,
					}
				} else {
					continue
				}
			}
		}

		if detail.StartTime == 0 && detail.EndTime == 0 {
			if cacheFound {
				detail = xxt.SignDetail{
					StartTime:    cache.StartTime,
					EndTime:      cache.EndTime,
					SignType:     cache.SignType,
					IfRefreshEWM: cache.IfRefreshEWM,
				}
			} else {
				continue
			}
		}

		recordSourceName := ""
		recordSource := int64(0)
		recordSignTime := int64(0)
		var rec struct {
			SourceUID  int64
			SignTimeMS int64
			SourceName string
		}
		recErr := h.db.Table("sign_records sr").
			Select("sr.source_uid, sr.sign_time_ms, u.name as source_name").
			Joins("left join users u on sr.source_uid = u.uid").
			Where("sr.user_uid = ? and sr.activity_id = ?", uid, a.ActiveID).
			Take(&rec).Error

		if recErr == nil {
			recordSource = rec.SourceUID
			recordSignTime = rec.SignTimeMS
			if rec.SourceUID == -1 {
				recordSourceName = "学习通"
			} else if rec.SourceName != "" {
				recordSourceName = rec.SourceName
			} else if rec.SourceUID == uid {
				recordSourceName = user.Name
			} else {
				recordSourceName = "未知用户"
			}
		} else if recErr != nil && !errors.Is(recErr, gorm.ErrRecordNotFound) {
			log.Printf("query sign record failed: uid=%d activity=%d err=%v", uid, a.ActiveID, recErr)
		}
		items = append(items, gin.H{
			"active_id":          a.ActiveID,
			"activity_name":      a.Name,
			"start_time":         detail.StartTime,
			"end_time":           detail.EndTime,
			"sign_type":          detail.SignType,
			"if_refresh_ewm":     detail.IfRefreshEWM,
			"record_source":      recordSource,
			"record_source_name": recordSourceName,
			"record_sign_time":   recordSignTime,
			"course_name":        sc.Name,
			"course_id":          sc.CourseID,
			"class_id":           sc.ClassID,
			"course_teacher":     sc.Teacher,
		})
	}
	hasMore := len(items) > h.listLimit
	if len(items) > 1 {
		sort.Slice(items, func(i, j int) bool {
			return items[i]["start_time"].(int64) > items[j]["start_time"].(int64)
		})
	}
	if hasMore {
		items = items[:h.listLimit]
	}
	return gin.H{
		"course_id":      sc.CourseID,
		"class_id":       sc.ClassID,
		"course_name":    sc.Name,
		"course_teacher": sc.Teacher,
		"icon":           sc.Icon,
		"activities":     items,
		"has_more":       hasMore,
	}, nil
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
