package handler

import (
	"errors"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/common"
	"xbt2/server/internal/im"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

func activityKey(courseID, classID int64) string {
	return strconv.FormatInt(courseID, 10) + ":" + strconv.FormatInt(classID, 10)
}

// mergeIMActivities adds the last successful IM snapshot to the per-course
// groups already built from taskactivelist. The same active_id appears once;
// its sources list records both origins.
func (h *SignHandler) mergeIMActivities(uid int64, user model.User, password string, selected []selectedCourseInfo, resp []gin.H, snapshot service.IMSnapshot) {
	if len(snapshot.Activities) == 0 {
		return
	}

	selectedByKey := make(map[string]selectedCourseInfo, len(selected))
	for _, course := range selected {
		selectedByKey[activityKey(course.CourseID, course.ClassID)] = course
	}
	groupsByKey := make(map[string]gin.H, len(resp))
	for _, group := range resp {
		courseID, _ := group["course_id"].(int64)
		classID, _ := group["class_id"].(int64)
		groupsByKey[activityKey(courseID, classID)] = group
	}

	touched := make(map[string]struct{})
	for _, activity := range snapshot.Activities {
		key := activityKey(activity.CourseID, activity.ClassID)
		course, selectedOK := selectedByKey[key]
		if !selectedOK {
			continue
		}
		group, groupOK := groupsByKey[key]
		if !groupOK {
			continue
		}
		items, _ := group["activities"].([]gin.H)
		var existing gin.H
		for _, item := range items {
			if activeID, _ := item["active_id"].(int64); activeID == activity.ActiveID {
				existing = item
				break
			}
		}
		if existing != nil {
			sources := appendActivitySource(activitySources(existing), im.SourceIMGroup)
			existing["sources"] = sources
			// Group chat is the more time-sensitive origin when both exist.
			existing["source"] = im.SourceIMGroup
			if _, ok := existing["im_atype"]; !ok {
				existing["im_atype"] = activity.AType
				existing["im_sub_title"] = activity.SubTitle
				existing["im_message_time"] = activity.MessageTimeMS
			}
			touched[key] = struct{}{}
			continue
		}

		detail, found := h.loadIMActivityDetail(user, password, activity, uid)
		group["activities"] = append(items, h.buildIMActivityItem(uid, user, course, activity, detail, found))
		h.ensureIMActivityScope(activity)
		touched[key] = struct{}{}
	}

	for key := range touched {
		finalizeActivityGroup(groupsByKey[key], h.listLimit)
	}
}

func (h *SignHandler) loadIMActivityDetail(user model.User, password string, activity im.SignActivity, uid int64) (xxt.SignDetail, bool) {
	var cache model.SignActivity
	cacheErr := h.db.Where("activity_id = ?", activity.ActiveID).First(&cache).Error
	if cacheErr == nil {
		return signDetailFromCache(cache), true
	}
	if !errors.Is(cacheErr, gorm.ErrRecordNotFound) {
		log.Printf("query im sign activity cache failed: uid=%d active_id=%d err=%v", uid, activity.ActiveID, cacheErr)
	}
	detail, err := h.xxt.GetSignDetail(user.Mobile, password, activity.ActiveID, activity.CourseID, activity.ClassID, uid)
	if err != nil {
		log.Printf("get im sign detail failed: uid=%d active_id=%d err=%v", uid, activity.ActiveID, err)
		return xxt.SignDetail{}, false
	}
	cacheRow := model.SignActivity{
		ActivityID:   activity.ActiveID,
		StartTime:    detail.StartTime,
		EndTime:      detail.EndTime,
		SignType:     detail.SignType,
		IfRefreshEWM: detail.IfRefreshEWM,
		IfPhoto:      detail.IfPhoto,
		IfFace:       detail.IfFace,
	}
	_ = h.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "activity_id"}},
		DoNothing: true,
	}).Create(&cacheRow).Error
	return detail, true
}

func (h *SignHandler) buildIMActivityItem(uid int64, user model.User, course selectedCourseInfo, activity im.SignActivity, detail xxt.SignDetail, detailFound bool) gin.H {
	startTime := detail.StartTime
	if startTime == 0 && !detailFound {
		startTime = activity.MessageTimeMS
	}
	recordSource, recordSourceName, recordSignTime := h.signRecordView(uid, activity.ActiveID, user)
	return gin.H{
		"active_id":          activity.ActiveID,
		"activity_name":      activity.Title,
		"start_time":         startTime,
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
		"course_name":        course.Name,
		"course_id":          course.CourseID,
		"class_id":           course.ClassID,
		"course_teacher":     course.Teacher,
		"source":             im.SourceIMGroup,
		"sources":            []string{im.SourceIMGroup},
		"im_atype":           activity.AType,
		"im_sub_title":       activity.SubTitle,
		"im_message_time":    activity.MessageTimeMS,
	}
}

func (h *SignHandler) ensureIMActivityScope(activity im.SignActivity) {
	scope := model.SignActivityScope{
		ActivityID: activity.ActiveID,
		CourseID:   activity.CourseID,
		ClassID:    activity.ClassID,
	}
	if err := h.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&scope).Error; err != nil {
		log.Printf("save im activity scope failed: active_id=%d err=%v", activity.ActiveID, err)
	}
}

func (h *SignHandler) signRecordView(uid, activityID int64, user model.User) (int64, string, int64) {
	var rec struct {
		SourceUID  int64
		SignTimeMS int64
		SourceName string
	}
	recErr := h.db.Table("sign_records sr").
		Select("sr.source_uid, sr.sign_time_ms, u.name as source_name").
		Joins("left join users u on sr.source_uid = u.uid").
		Where("sr.user_uid = ? and sr.activity_id = ?", uid, activityID).
		Take(&rec).Error
	if recErr == nil {
		switch {
		case rec.SourceUID == -1:
			return rec.SourceUID, "学习通", rec.SignTimeMS
		case rec.SourceName != "":
			return rec.SourceUID, rec.SourceName, rec.SignTimeMS
		case rec.SourceUID == uid:
			return rec.SourceUID, user.Name, rec.SignTimeMS
		default:
			return rec.SourceUID, "未知用户", rec.SignTimeMS
		}
	}
	if !errors.Is(recErr, gorm.ErrRecordNotFound) {
		log.Printf("query sign record failed: uid=%d activity=%d err=%v", uid, activityID, recErr)
	}
	return 0, "", 0
}

func activitySources(item gin.H) []string {
	switch values := item["sources"].(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return []string{im.SourceTaskActiveList}
	}
}

func appendActivitySource(sources []string, source string) []string {
	for _, existing := range sources {
		if existing == source {
			return sources
		}
	}
	return append(sources, source)
}

func finalizeActivityGroup(group gin.H, limit int) {
	items, _ := group["activities"].([]gin.H)
	sort.SliceStable(items, func(i, j int) bool {
		return activityStartTime(items[i]) > activityStartTime(items[j])
	})
	total := len(items)
	if limit > 0 && total > limit {
		items = items[:limit]
	}
	now := time.Now()
	for _, item := range items {
		decorateActivityItem(item, now)
	}
	group["activities"] = items
	group["has_more"] = total > limit
}

func activityStartTime(item gin.H) int64 {
	switch value := item["start_time"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func (h *SignHandler) IMStatus(c *gin.Context) {
	uid := common.GetUserUID(c)
	if h.imSource == nil {
		common.Success(c, gin.H{
			"enabled":               false,
			"interval_seconds":      0,
			"fetch_timeout_seconds": 0,
			"error_code":            im.CodeDisabled,
			"last_poll_at":          0,
			"last_success_at":       0,
			"group_count":           0,
			"activity_count":        0,
		})
		return
	}
	snapshot := h.imSource.Snapshot(uid)
	errorCode := snapshot.ErrorCode
	if !snapshot.Enabled && errorCode == "" {
		errorCode = im.CodeDisabled
	}
	common.Success(c, gin.H{
		"enabled":               snapshot.Enabled,
		"interval_seconds":      snapshot.IntervalSeconds,
		"fetch_timeout_seconds": snapshot.FetchTimeoutSeconds,
		"error_code":            errorCode,
		"last_poll_at":          unixMilli(snapshot.LastPollAt),
		"last_success_at":       unixMilli(snapshot.LastSuccessAt),
		"group_count":           snapshot.GroupCount,
		"activity_count":        snapshot.ActivityCount,
	})
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
