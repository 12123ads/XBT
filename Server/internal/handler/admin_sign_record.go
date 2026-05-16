package handler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
)

const (
	defaultAdminSignRecordPageSize = 20
	maxAdminSignRecordPageSize     = 100
)

type adminSignRecordRow struct {
	ID            uint
	UserUID       int64
	UserName      string
	UserMobile    string
	SourceUID     int64
	SourceName    string
	SourceMobile  string
	ActivityID    int64
	CourseID      int64
	ClassID       int64
	SignType      int
	ActivityName  string
	CourseName    string
	CourseTeacher string
	SignTimeMS    int64
}

func (h *AdminAccountHandler) ListSignRecords(c *gin.Context) {
	page, pageSize, ok := parseAdminRecordPagination(c)
	if !ok {
		return
	}

	base := h.db.Table("sign_records sr").
		Joins("left join users tu on sr.user_uid = tu.uid").
		Joins("left join users su on sr.source_uid = su.uid").
		Joins("left join courses co on sr.course_id = co.course_id and sr.class_id = co.class_id")

	var err error
	base, err = applyAdminSignRecordFilters(c, base)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		common.Fail(c, 500, "count sign records failed")
		return
	}

	var rows []adminSignRecordRow
	err = base.Select(`
		sr.id,
		sr.user_uid,
		tu.name as user_name,
		tu.mobile as user_mobile,
		sr.source_uid,
		su.name as source_name,
		su.mobile as source_mobile,
		sr.activity_id,
		sr.course_id,
		sr.class_id,
		sr.sign_type,
		COALESCE(NULLIF(sr.activity_name, ''), '') as activity_name,
		COALESCE(NULLIF(sr.course_name, ''), co.name, '') as course_name,
		COALESCE(NULLIF(sr.course_teacher, ''), co.teacher, '') as course_teacher,
		sr.sign_time_ms
	`).
		Order("sr.sign_time_ms desc, sr.id desc").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Scan(&rows).Error
	if err != nil {
		common.Fail(c, 500, "query sign records failed")
		return
	}

	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, adminSignRecordView(row))
	}

	totalPages := int64(0)
	if total > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}
	common.Success(c, gin.H{
		"items":       items,
		"page":        page,
		"page_size":   pageSize,
		"total":       total,
		"total_pages": totalPages,
	})
}

func applyAdminSignRecordFilters(c *gin.Context, query *gorm.DB) (*gorm.DB, error) {
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		query = query.Where(`
			LOWER(COALESCE(NULLIF(sr.activity_name, ''), '')) LIKE ?
			OR LOWER(COALESCE(NULLIF(sr.course_name, ''), co.name, '')) LIKE ?
			OR LOWER(COALESCE(NULLIF(sr.course_teacher, ''), co.teacher, '')) LIKE ?
			OR LOWER(COALESCE(tu.name, '')) LIKE ?
			OR LOWER(COALESCE(su.name, '')) LIKE ?
			OR CAST(sr.activity_id AS TEXT) LIKE ?
			OR CAST(sr.course_id AS TEXT) LIKE ?
			OR CAST(sr.class_id AS TEXT) LIKE ?
		`, pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern)
	}

	if uid, present, err := parseOptionalInt64Query(c, "user_uid"); err != nil {
		return nil, err
	} else if present {
		if uid <= 0 {
			return nil, fmt.Errorf("invalid user_uid")
		}
		query = query.Where("sr.user_uid = ?", uid)
	}

	if sourceUID, present, err := parseOptionalInt64Query(c, "source_uid"); err != nil {
		return nil, err
	} else if present {
		query = query.Where("sr.source_uid = ?", sourceUID)
	}

	if activityID, present, err := parseOptionalInt64Query(c, "activity_id"); err != nil {
		return nil, err
	} else if present {
		if activityID <= 0 {
			return nil, fmt.Errorf("invalid activity_id")
		}
		query = query.Where("sr.activity_id = ?", activityID)
	}

	if courseID, present, err := parseOptionalInt64Query(c, "course_id"); err != nil {
		return nil, err
	} else if present {
		if courseID <= 0 {
			return nil, fmt.Errorf("invalid course_id")
		}
		query = query.Where("sr.course_id = ?", courseID)
	}

	if classID, present, err := parseOptionalInt64Query(c, "class_id"); err != nil {
		return nil, err
	} else if present {
		if classID <= 0 {
			return nil, fmt.Errorf("invalid class_id")
		}
		query = query.Where("sr.class_id = ?", classID)
	}

	if signType, present, err := parseOptionalIntQuery(c, "sign_type"); err != nil {
		return nil, err
	} else if present {
		if signType < 0 {
			return nil, fmt.Errorf("invalid sign_type")
		}
		query = query.Where("sr.sign_type = ?", signType)
	}

	startTime, hasStart, err := parseOptionalInt64Query(c, "start_time")
	if err != nil {
		return nil, err
	}
	endTime, hasEnd, err := parseOptionalInt64Query(c, "end_time")
	if err != nil {
		return nil, err
	}
	if hasStart {
		if startTime <= 0 {
			return nil, fmt.Errorf("invalid start_time")
		}
		query = query.Where("sr.sign_time_ms >= ?", startTime)
	}
	if hasEnd {
		if endTime <= 0 {
			return nil, fmt.Errorf("invalid end_time")
		}
		query = query.Where("sr.sign_time_ms <= ?", endTime)
	}
	if hasStart && hasEnd && startTime > endTime {
		return nil, fmt.Errorf("start_time cannot be greater than end_time")
	}

	return query, nil
}

func adminSignRecordView(row adminSignRecordRow) gin.H {
	userName := strings.TrimSpace(row.UserName)
	if userName == "" {
		userName = fmt.Sprintf("UID %d", row.UserUID)
	}

	sourceName := strings.TrimSpace(row.SourceName)
	if row.SourceUID == -1 {
		sourceName = "学习通"
	} else if sourceName == "" && row.SourceUID == row.UserUID {
		sourceName = userName
	} else if sourceName == "" {
		sourceName = "未知用户"
	}

	activityName := strings.TrimSpace(row.ActivityName)
	if activityName == "" {
		activityName = "未知活动"
	}
	courseName := strings.TrimSpace(row.CourseName)
	if courseName == "" {
		courseName = "未知课程"
	}

	return gin.H{
		"id":                   row.ID,
		"user_uid":             row.UserUID,
		"user_name":            userName,
		"user_mobile_masked":   common.MaskMobile(row.UserMobile),
		"source_uid":           row.SourceUID,
		"source_name":          sourceName,
		"source_mobile_masked": common.MaskMobile(row.SourceMobile),
		"activity_id":          row.ActivityID,
		"activity_name":        activityName,
		"course_id":            row.CourseID,
		"class_id":             row.ClassID,
		"course_name":          courseName,
		"course_teacher":       strings.TrimSpace(row.CourseTeacher),
		"sign_type":            row.SignType,
		"sign_time_ms":         row.SignTimeMS,
	}
}

func parseAdminRecordPagination(c *gin.Context) (int, int, bool) {
	page := 1
	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			common.Fail(c, 400, "invalid page")
			return 0, 0, false
		}
		page = value
	}

	pageSize := defaultAdminSignRecordPageSize
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			common.Fail(c, 400, "invalid page_size")
			return 0, 0, false
		}
		pageSize = value
	}
	if pageSize > maxAdminSignRecordPageSize {
		pageSize = maxAdminSignRecordPageSize
	}
	return page, pageSize, true
}

func parseOptionalInt64Query(c *gin.Context, name string) (int64, bool, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("invalid %s", name)
	}
	return value, true, nil
}

func parseOptionalIntQuery(c *gin.Context, name string) (int, bool, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true, fmt.Errorf("invalid %s", name)
	}
	return value, true, nil
}
