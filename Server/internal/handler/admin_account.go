package handler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const (
	courseCopyModeAppend  = "append"
	courseCopyModeReplace = "replace"
)

var (
	errNoTargetAccount      = errors.New("target_uids are required")
	errNoValidTargetAccount = errors.New("no valid target account")
)

type AdminAccountHandler struct {
	db  *gorm.DB
	xxt *xxt.Client
	cc  *service.CredentialCrypto
}

type courseCopyResult struct {
	TargetCount     int    `json:"target_count"`
	CourseCount     int    `json:"course_count"`
	CopiedRelations int    `json:"copied_relations"`
	Mode            string `json:"mode"`
}

func NewAdminAccountHandler(db *gorm.DB, xxtClient *xxt.Client, cc *service.CredentialCrypto) *AdminAccountHandler {
	return &AdminAccountHandler{db: db, xxt: xxtClient, cc: cc}
}

func (h *AdminAccountHandler) ListAccounts(c *gin.Context) {
	var users []model.User
	if err := h.db.Order("permission desc, last_login_at desc, name asc").Find(&users).Error; err != nil {
		common.Fail(c, 500, "query accounts failed")
		return
	}

	resp := make([]gin.H, 0, len(users))
	for _, user := range users {
		resp = append(resp, h.accountView(user))
	}
	common.Success(c, resp)
}

func (h *AdminAccountHandler) CreateAccount(c *gin.Context) {
	var req dto.AdminCreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	req.Mobile = strings.TrimSpace(req.Mobile)
	if req.Mobile == "" || req.Password == "" {
		common.Fail(c, 400, "mobile and password are required")
		return
	}

	loginResult, err := h.xxt.PreLogin(req.Mobile, req.Password)
	if err != nil {
		common.Fail(c, 401, err.Error())
		return
	}
	cipher, err := h.cc.Encrypt(req.Password)
	if err != nil {
		common.Fail(c, 500, "credential encrypt failed")
		return
	}

	permission := h.resolveAccountPermission(req.Mobile)
	wl := model.Whitelist{Mobile: req.Mobile, Permission: permission}
	if err := h.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mobile"}},
		DoUpdates: clause.AssignmentColumns([]string{"permission", "updated_at"}),
	}).Create(&wl).Error; err != nil {
		common.Fail(c, 500, "save whitelist failed")
		return
	}

	user := model.User{
		UID:              loginResult.UID,
		Mobile:           req.Mobile,
		Name:             loginResult.Name,
		Avatar:           loginResult.Avatar,
		CredentialCipher: cipher,
		Permission:       permission,
		LastLoginAt:      time.Now(),
	}
	if err := h.db.Where("uid = ?", user.UID).Assign(user).FirstOrCreate(&user).Error; err != nil {
		common.Fail(c, 500, "save account failed")
		return
	}

	syncCount := 0
	syncMessage := ""
	if count, err := h.syncCourses(user, req.Password); err != nil {
		syncMessage = err.Error()
	} else {
		syncCount = count
	}

	common.Success(c, gin.H{
		"account":      h.accountView(user),
		"sync_count":   syncCount,
		"sync_message": syncMessage,
	})
}

func (h *AdminAccountHandler) SyncUserCourses(c *gin.Context) {
	uid, ok := parseUIDParam(c)
	if !ok {
		return
	}

	var user model.User
	if err := h.db.Where("uid = ?", uid).First(&user).Error; err != nil {
		common.Fail(c, 404, "account not found")
		return
	}
	password, err := h.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		common.Fail(c, 400, "credential expired, please add account again")
		return
	}
	count, err := h.syncCourses(user, password)
	if err != nil {
		if isXXTAuthError(err) {
			common.Fail(c, 401, "学习通登录已失效，请重新添加该账号")
			return
		}
		common.Fail(c, 500, "sync courses failed: "+err.Error())
		return
	}
	common.Success(c, gin.H{"count": count})
}

func (h *AdminAccountHandler) ListUserCourses(c *gin.Context) {
	uid, ok := parseUIDParam(c)
	if !ok {
		return
	}
	if !h.accountExists(uid) {
		common.Fail(c, 404, "account not found")
		return
	}

	var rows []struct {
		ClassID    int64  `json:"class_id"`
		CourseID   int64  `json:"course_id"`
		Name       string `json:"name"`
		Teacher    string `json:"teacher"`
		Icon       string `json:"icon"`
		IsSelected bool   `json:"is_selected"`
	}
	err := h.db.Table("user_courses uc").
		Select("uc.class_id, uc.course_id, c.name, c.teacher, c.icon, uc.is_selected").
		Joins("join courses c on uc.course_id = c.course_id and uc.class_id = c.class_id").
		Where("uc.user_uid = ?", uid).
		Order("uc.is_selected desc, c.name asc, uc.course_id asc").
		Scan(&rows).Error
	if err != nil {
		common.Fail(c, 500, "query courses failed")
		return
	}
	common.Success(c, rows)
}

func (h *AdminAccountHandler) UpdateUserCourseSelection(c *gin.Context) {
	uid, ok := parseUIDParam(c)
	if !ok {
		return
	}
	if !h.accountExists(uid) {
		common.Fail(c, 404, "account not found")
		return
	}

	var req dto.AdminUpdateCourseSelectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	refs := dedupeCourseRefs(req.Courses)

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UserCourse{}).Where("user_uid = ?", uid).Update("is_selected", false).Error; err != nil {
			return err
		}
		for _, ref := range refs {
			uc := model.UserCourse{UserUID: uid, CourseID: ref.CourseID, ClassID: ref.ClassID, IsSelected: true}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_uid"}, {Name: "course_id"}, {Name: "class_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"is_selected", "updated_at"}),
			}).Create(&uc).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		common.Fail(c, 500, "update course selection failed")
		return
	}
	common.Success(c, gin.H{"selected_count": len(refs)})
}

func (h *AdminAccountHandler) AddUserCourse(c *gin.Context) {
	uid, ok := parseUIDParam(c)
	if !ok {
		return
	}
	if !h.accountExists(uid) {
		common.Fail(c, 404, "account not found")
		return
	}

	var req dto.AdminAddUserCourseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if req.CourseID <= 0 || req.ClassID <= 0 {
		common.Fail(c, 400, "course_id and class_id are required")
		return
	}

	var existingCourse model.Course
	hasExistingCourse := h.db.Where("course_id = ? AND class_id = ?", req.CourseID, req.ClassID).Take(&existingCourse).Error == nil

	name := strings.TrimSpace(req.Name)
	if name == "" {
		if hasExistingCourse && strings.TrimSpace(existingCourse.Name) != "" {
			name = existingCourse.Name
		} else {
			name = fmt.Sprintf("课程 %d", req.CourseID)
		}
	}
	teacher := strings.TrimSpace(req.Teacher)
	if teacher == "" && hasExistingCourse {
		teacher = existingCourse.Teacher
	}
	icon := strings.TrimSpace(req.Icon)
	if icon == "" && hasExistingCourse {
		icon = existingCourse.Icon
	}
	selected := true
	if req.IsSelected != nil {
		selected = *req.IsSelected
	}

	course := model.Course{
		CourseID: req.CourseID,
		ClassID:  req.ClassID,
		Name:     name,
		Teacher:  teacher,
		Icon:     icon,
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "course_id"}, {Name: "class_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "teacher", "icon", "updated_at"}),
		}).Create(&course).Error; err != nil {
			return err
		}
		uc := model.UserCourse{UserUID: uid, CourseID: req.CourseID, ClassID: req.ClassID, IsSelected: selected}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_uid"}, {Name: "course_id"}, {Name: "class_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"is_selected", "updated_at"}),
		}).Create(&uc).Error
	})
	if err != nil {
		common.Fail(c, 500, "add user course failed")
		return
	}
	common.Success(c, gin.H{
		"course_id":   req.CourseID,
		"class_id":    req.ClassID,
		"name":        course.Name,
		"teacher":     course.Teacher,
		"icon":        course.Icon,
		"is_selected": selected,
	})
}

func (h *AdminAccountHandler) CopySelectedCourses(c *gin.Context) {
	var req dto.AdminCopyCourseSelectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if req.SourceUID <= 0 {
		common.Fail(c, 400, "invalid source_uid")
		return
	}
	if !h.accountExists(req.SourceUID) {
		common.Fail(c, 404, "source account not found")
		return
	}
	targetUIDs := dedupeUIDTargets(req.TargetUIDs, req.SourceUID)
	if len(targetUIDs) == 0 {
		common.Fail(c, 400, "target_uids are required")
		return
	}

	result, err := h.copySelectedCoursesToTargets(req.SourceUID, targetUIDs, courseCopyModeAppend, true)
	if err != nil {
		status := 500
		if errors.Is(err, errNoValidTargetAccount) || errors.Is(err, errNoTargetAccount) {
			status = 400
		}
		common.Fail(c, status, err.Error())
		return
	}
	common.Success(c, result)
}

func (h *AdminAccountHandler) copySelectedCoursesToTargets(sourceUID int64, targetUIDs []int64, mode string, requireTargets bool) (courseCopyResult, error) {
	result := courseCopyResult{Mode: mode}
	targetUIDs = dedupeUIDTargets(targetUIDs, sourceUID)
	if len(targetUIDs) == 0 {
		if requireTargets {
			return result, errNoTargetAccount
		}
		return result, nil
	}

	var validTargets []int64
	if err := h.db.Model(&model.User{}).Where("uid IN ?", targetUIDs).Pluck("uid", &validTargets).Error; err != nil {
		return result, fmt.Errorf("query target accounts failed: %w", err)
	}
	if len(validTargets) == 0 {
		if requireTargets {
			return result, errNoValidTargetAccount
		}
		return result, nil
	}

	var selectedCourses []model.UserCourse
	if err := h.db.Where("user_uid = ? AND is_selected = ?", sourceUID, true).Find(&selectedCourses).Error; err != nil {
		return result, fmt.Errorf("query source courses failed: %w", err)
	}

	result.TargetCount = len(validTargets)
	result.CourseCount = len(selectedCourses)
	copied := 0
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if mode == courseCopyModeReplace {
			if err := tx.Model(&model.UserCourse{}).
				Where("user_uid IN ?", validTargets).
				Update("is_selected", false).Error; err != nil {
				return err
			}
		}
		for _, targetUID := range validTargets {
			for _, sourceCourse := range selectedCourses {
				uc := model.UserCourse{
					UserUID:    targetUID,
					CourseID:   sourceCourse.CourseID,
					ClassID:    sourceCourse.ClassID,
					IsSelected: true,
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "user_uid"}, {Name: "course_id"}, {Name: "class_id"}},
					DoUpdates: clause.AssignmentColumns([]string{"is_selected", "updated_at"}),
				}).Create(&uc).Error; err != nil {
					return err
				}
				copied++
			}
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("copy courses failed: %w", err)
	}

	result.CopiedRelations = copied
	return result, nil
}

func (h *AdminAccountHandler) resolveAccountPermission(mobile string) int {
	var wl model.Whitelist
	if err := h.db.Where("mobile = ?", mobile).Take(&wl).Error; err == nil && wl.Permission >= 2 {
		return wl.Permission
	}
	return 1
}

func (h *AdminAccountHandler) syncCourses(user model.User, password string) (int, error) {
	courses, err := h.xxt.GetCourses(user.Mobile, password)
	if err != nil {
		return 0, err
	}
	for _, course := range courses {
		co := model.Course{
			CourseID: course.CourseID,
			ClassID:  course.ClassID,
			Name:     course.Name,
			Teacher:  course.Teacher,
			Icon:     course.Icon,
		}
		_ = h.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "course_id"}, {Name: "class_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "teacher", "icon", "updated_at"}),
		}).Create(&co).Error

		uc := model.UserCourse{UserUID: user.UID, CourseID: course.CourseID, ClassID: course.ClassID, IsSelected: false}
		_ = h.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_uid"}, {Name: "course_id"}, {Name: "class_id"}},
			DoNothing: true,
		}).Create(&uc).Error
	}
	return len(courses), nil
}

func (h *AdminAccountHandler) accountView(user model.User) gin.H {
	var courseCount int64
	var selectedCount int64
	_ = h.db.Model(&model.UserCourse{}).Where("user_uid = ?", user.UID).Count(&courseCount).Error
	_ = h.db.Model(&model.UserCourse{}).Where("user_uid = ? AND is_selected = ?", user.UID, true).Count(&selectedCount).Error

	lastLoginAt := int64(0)
	if !user.LastLoginAt.IsZero() {
		lastLoginAt = user.LastLoginAt.UnixMilli()
	}
	return gin.H{
		"uid":            user.UID,
		"name":           user.Name,
		"mobile_masked":  common.MaskMobile(user.Mobile),
		"avatar":         user.Avatar,
		"permission":     user.Permission,
		"last_login_at":  lastLoginAt,
		"course_count":   courseCount,
		"selected_count": selectedCount,
	}
}

func (h *AdminAccountHandler) accountExists(uid int64) bool {
	var count int64
	if err := h.db.Model(&model.User{}).Where("uid = ?", uid).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

func parseUIDParam(c *gin.Context) (int64, bool) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || uid <= 0 {
		common.Fail(c, 400, "invalid uid")
		return 0, false
	}
	return uid, true
}

func dedupeCourseRefs(refs []dto.CourseRef) []dto.CourseRef {
	seen := make(map[string]struct{}, len(refs))
	out := make([]dto.CourseRef, 0, len(refs))
	for _, ref := range refs {
		if ref.CourseID <= 0 || ref.ClassID <= 0 {
			continue
		}
		key := fmt.Sprintf("%d:%d", ref.CourseID, ref.ClassID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func dedupeUIDTargets(uids []int64, excluded int64) []int64 {
	seen := make(map[int64]struct{}, len(uids))
	out := make([]int64, 0, len(uids))
	for _, uid := range uids {
		if uid <= 0 || uid == excluded {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}
