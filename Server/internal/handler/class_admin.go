package handler

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
)

func (h *AdminAccountHandler) classAdminGroup(c *gin.Context) (model.ClassGroup, bool) {
	uid := common.GetUserUID(c)
	var group model.ClassGroup
	if err := h.db.Where("admin_uid = ?", uid).Order("id asc").First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 403, "无权管理班级")
		} else {
			common.Fail(c, 500, "query class group failed")
		}
		return group, false
	}
	return group, true
}

func (h *AdminAccountHandler) ClassAdminOverview(c *gin.Context) {
	group, ok := h.classAdminGroup(c)
	if !ok {
		return
	}
	memberUIDs, err := h.classGroupMemberUIDs(group.ID)
	if err != nil {
		common.Fail(c, 500, "query class members failed")
		return
	}

	var users []model.User
	if len(memberUIDs) > 0 {
		if err := h.db.Where("uid IN ?", memberUIDs).Find(&users).Error; err != nil {
			common.Fail(c, 500, "query class members failed")
			return
		}
	}
	userByUID := make(map[int64]model.User, len(users))
	for _, user := range users {
		userByUID[user.UID] = user
	}
	members := make([]gin.H, 0, len(memberUIDs))
	for _, uid := range memberUIDs {
		if user, exists := userByUID[uid]; exists {
			members = append(members, h.accountView(user))
		}
	}

	common.Success(c, gin.H{
		"group":   classGroupView(group, memberUIDs),
		"members": members,
	})
}

func (h *AdminAccountHandler) CreateClassAccount(c *gin.Context) {
	group, ok := h.classAdminGroup(c)
	if !ok {
		return
	}

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
	if loginResult.UID == group.AdminUID {
		common.Fail(c, 400, "不能添加班级管理员本人")
		return
	}

	var existingUser model.User
	existingFound := true
	if err := h.db.Where("uid = ? OR mobile = ?", loginResult.UID, req.Mobile).Take(&existingUser).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 500, "query account failed")
			return
		}
		existingFound = false
	}
	if existingFound {
		if existingUser.UID != loginResult.UID || existingUser.Mobile != req.Mobile {
			common.Fail(c, 409, "账号信息与已有记录不一致")
			return
		}
		if existingUser.Permission >= 2 {
			common.Fail(c, 403, "不能添加管理员账号")
			return
		}
		if existingUser.Permission <= 0 {
			common.Fail(c, 403, "账号未授权")
			return
		}
		var member model.ClassGroupMember
		if err := h.db.Where("user_uid = ?", existingUser.UID).Take(&member).Error; err == nil {
			if member.GroupID != group.ID {
				common.Fail(c, 409, "该账号已属于其他班级")
				return
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 500, "query class member failed")
			return
		}
	}

	var whitelist model.Whitelist
	whitelistFound := true
	if err := h.db.Where("mobile = ?", req.Mobile).Take(&whitelist).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 500, "query whitelist failed")
			return
		}
		whitelistFound = false
	}
	if whitelistFound {
		if whitelist.Permission >= 2 {
			common.Fail(c, 403, "不能添加管理员账号")
			return
		}
		if whitelist.Permission <= 0 {
			common.Fail(c, 403, "账号未授权")
			return
		}
	}

	cipher, err := h.cc.Encrypt(req.Password)
	if err != nil {
		common.Fail(c, 500, "credential encrypt failed")
		return
	}
	deviceCipher, clientIDCipher, _ := service.EnsureFaceDevice(c.Request.Context(), h.db, h.cc, h.xxt, loginResult.UID, req.Mobile, req.Password)

	user := model.User{
		UID:              loginResult.UID,
		Mobile:           req.Mobile,
		Name:             loginResult.Name,
		Avatar:           loginResult.Avatar,
		CredentialCipher: cipher,
		DeviceInfoCipher: deviceCipher,
		ClientIDCipher:   clientIDCipher,
		Permission:       1,
		LastLoginAt:      time.Now(),
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if !whitelistFound {
			wl := model.Whitelist{Mobile: req.Mobile, Permission: 1}
			if err := tx.Create(&wl).Error; err != nil {
				return err
			}
		}
		if existingFound {
			user = existingUser
			user.Name = loginResult.Name
			user.Avatar = loginResult.Avatar
			user.CredentialCipher = cipher
			user.DeviceInfoCipher = deviceCipher
			user.ClientIDCipher = clientIDCipher
			user.Permission = 1
			user.LastLoginAt = time.Now()
			if err := tx.Model(&model.User{}).Where("uid = ?", user.UID).Updates(map[string]interface{}{
				"name":               user.Name,
				"avatar":             user.Avatar,
				"credential_cipher":  user.CredentialCipher,
				"device_info_cipher": user.DeviceInfoCipher,
				"client_id_cipher":   user.ClientIDCipher,
				"permission":         user.Permission,
				"last_login_at":      user.LastLoginAt,
			}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&user).Error; err != nil {
			return err
		}

		member := model.ClassGroupMember{GroupID: group.ID, UserUID: user.UID}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_uid"}},
			DoUpdates: clause.AssignmentColumns([]string{"group_id", "updated_at"}),
		}).Create(&member).Error
	})
	if err != nil {
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

	copyResult, copyErr := h.copySelectedCoursesToTargets(group.AdminUID, []int64{user.UID}, courseCopyModeAppend, true)
	copyMessage := ""
	if copyErr != nil {
		copyMessage = copyErr.Error()
	}

	common.Success(c, gin.H{
		"account":          h.accountView(user),
		"sync_count":       syncCount,
		"sync_message":     syncMessage,
		"copied_relations": copyResult.CopiedRelations,
		"course_count":     copyResult.CourseCount,
		"copy_message":     copyMessage,
	})
}
