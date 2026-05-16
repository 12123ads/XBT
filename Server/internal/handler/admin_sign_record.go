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
	ID              uint
	ActivityID      int64
	CourseID        int64
	ClassID         int64
	SignType        int
	ActivityName    string
	CourseName      string
	CourseTeacher   string
	FirstSignTimeMS int64
	LastSignTimeMS  int64
	TargetCount     int64
	TargetNames     string
	SourceCount     int64
	SourceNames     string
}

func (h *AdminAccountHandler) ListSignRecords(c *gin.Context) {
	page, pageSize, ok := parseAdminRecordPagination(c)
	if !ok {
		return
	}

	base, err := h.adminSignRecordBase(c)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}

	groupQuery := base.Select(adminSignRecordGroupSelect()).
		Group("sr.activity_id, sr.course_id, sr.class_id, sr.sign_type")

	var total int64
	if err := h.db.Table("(?) AS grouped_sign_records", groupQuery).Count(&total).Error; err != nil {
		common.Fail(c, 500, "count sign records failed")
		return
	}

	base, err = h.adminSignRecordBase(c)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}

	var rows []adminSignRecordRow
	err = base.Select(adminSignRecordGroupSelect()).
		Group("sr.activity_id, sr.course_id, sr.class_id, sr.sign_type").
		Order("MAX(sr.sign_time_ms) desc, MIN(sr.id) desc").
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

func (h *AdminAccountHandler) adminSignRecordBase(c *gin.Context) (*gorm.DB, error) {
	query := h.db.Table("sign_records sr").
		Joins("left join users tu on sr.user_uid = tu.uid").
		Joins("left join users su on sr.source_uid = su.uid").
		Joins("left join courses co on sr.course_id = co.course_id and sr.class_id = co.class_id").
		Where("sr.source_uid <> ?", -1)
	return applyAdminSignRecordFilters(c, query)
}

func adminSignRecordGroupSelect() string {
	return `
		MIN(sr.id) as id,
		sr.activity_id,
		sr.course_id,
		sr.class_id,
		sr.sign_type,
		COALESCE(MAX(NULLIF(sr.activity_name, '')), '') as activity_name,
		COALESCE(MAX(COALESCE(NULLIF(sr.course_name, ''), co.name, '')), '') as course_name,
		COALESCE(MAX(COALESCE(NULLIF(sr.course_teacher, ''), co.teacher, '')), '') as course_teacher,
		MIN(sr.sign_time_ms) as first_sign_time_ms,
		MAX(sr.sign_time_ms) as last_sign_time_ms,
		COUNT(*) as target_count,
		COALESCE(STRING_AGG(DISTINCT COALESCE(NULLIF(tu.name, ''), 'UID ' || sr.user_uid::text), '、'), '') as target_names,
		COUNT(DISTINCT sr.source_uid) as source_count,
		COALESCE(STRING_AGG(DISTINCT COALESCE(NULLIF(su.name, ''), CASE WHEN sr.source_uid = sr.user_uid THEN COALESCE(NULLIF(tu.name, ''), 'UID ' || sr.source_uid::text) ELSE 'UID ' || sr.source_uid::text END), '、'), '') as source_names
	`
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
	activityName := strings.TrimSpace(row.ActivityName)
	if activityName == "" {
		activityName = "未知活动"
	}
	courseName := strings.TrimSpace(row.CourseName)
	if courseName == "" {
		courseName = "未知课程"
	}
	targetNames := strings.TrimSpace(row.TargetNames)
	if targetNames == "" {
		targetNames = fmt.Sprintf("%d 个账号", row.TargetCount)
	}
	sourceNames := strings.TrimSpace(row.SourceNames)
	if sourceNames == "" {
		sourceNames = "未知用户"
	}

	return gin.H{
		"id":                 row.ID,
		"activity_id":        row.ActivityID,
		"activity_name":      activityName,
		"course_id":          row.CourseID,
		"class_id":           row.ClassID,
		"course_name":        courseName,
		"course_teacher":     strings.TrimSpace(row.CourseTeacher),
		"sign_type":          row.SignType,
		"sign_time_ms":       row.LastSignTimeMS,
		"first_sign_time_ms": row.FirstSignTimeMS,
		"last_sign_time_ms":  row.LastSignTimeMS,
		"target_count":       row.TargetCount,
		"target_names":       targetNames,
		"source_count":       row.SourceCount,
		"source_names":       sourceNames,
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
