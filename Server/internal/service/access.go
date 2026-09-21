package service

import (
	"errors"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
)

var (
	ErrAccountInactive          = errors.New("account inactive")
	ErrSignForbidden            = errors.New("sign operation forbidden")
	ErrActivityScopeUnavailable = errors.New("activity scope unavailable")
)

func LoadActiveUser(database *gorm.DB, uid int64) (model.User, error) {
	if uid <= 0 {
		return model.User{}, ErrAccountInactive
	}
	var row struct {
		model.User
		WhitelistPermission int
	}
	err := database.Table("users u").
		Select("u.*, w.permission AS whitelist_permission").
		Joins("JOIN whitelists w ON w.mobile = u.mobile").
		Where("u.uid = ? AND u.permission > 0 AND w.permission > 0", uid).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.User{}, ErrAccountInactive
	}
	if err != nil {
		return model.User{}, err
	}
	if row.WhitelistPermission < row.Permission {
		row.Permission = row.WhitelistPermission
	}
	return row.User, nil
}

func UserSelectedCourse(database *gorm.DB, uid, courseID, classID int64) (bool, error) {
	if uid <= 0 || courseID <= 0 || classID <= 0 {
		return false, nil
	}
	var count int64
	if err := database.Model(&model.UserCourse{}).
		Where("user_uid = ? AND course_id = ? AND class_id = ? AND is_selected = true", uid, courseID, classID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
