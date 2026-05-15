package handler

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
)

func (h *AdminAccountHandler) ListClassGroups(c *gin.Context) {
	var groups []model.ClassGroup
	if err := h.db.Order("name asc, id asc").Find(&groups).Error; err != nil {
		common.Fail(c, 500, "query class groups failed")
		return
	}

	groupIDs := make([]uint, 0, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
	}

	membersByGroup := map[uint][]int64{}
	if len(groupIDs) > 0 {
		var members []model.ClassGroupMember
		if err := h.db.Where("group_id IN ?", groupIDs).Order("created_at asc, id asc").Find(&members).Error; err != nil {
			common.Fail(c, 500, "query class group members failed")
			return
		}
		for _, member := range members {
			membersByGroup[member.GroupID] = append(membersByGroup[member.GroupID], member.UserUID)
		}
	}

	resp := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		resp = append(resp, classGroupView(group, membersByGroup[group.ID]))
	}
	common.Success(c, resp)
}

func (h *AdminAccountHandler) CreateClassGroup(c *gin.Context) {
	var req dto.AdminClassGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	group := model.ClassGroup{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
	}
	if group.Name == "" {
		common.Fail(c, 400, "name is required")
		return
	}

	if err := h.db.Create(&group).Error; err != nil {
		common.Fail(c, 500, "create class group failed")
		return
	}
	common.Success(c, classGroupView(group, nil))
}

func (h *AdminAccountHandler) UpdateClassGroup(c *gin.Context) {
	groupID, ok := parseGroupIDParam(c)
	if !ok {
		return
	}

	var req dto.AdminClassGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		common.Fail(c, 400, "name is required")
		return
	}

	var group model.ClassGroup
	if err := h.db.First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 404, "class group not found")
			return
		}
		common.Fail(c, 500, "query class group failed")
		return
	}

	group.Name = name
	group.Description = strings.TrimSpace(req.Description)
	if err := h.db.Save(&group).Error; err != nil {
		common.Fail(c, 500, "update class group failed")
		return
	}

	members, err := h.classGroupMemberUIDs(group.ID)
	if err != nil {
		common.Fail(c, 500, "query class group members failed")
		return
	}
	common.Success(c, classGroupView(group, members))
}

func (h *AdminAccountHandler) DeleteClassGroup(c *gin.Context) {
	groupID, ok := parseGroupIDParam(c)
	if !ok {
		return
	}

	var group model.ClassGroup
	if err := h.db.First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 404, "class group not found")
			return
		}
		common.Fail(c, 500, "query class group failed")
		return
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", group.ID).Delete(&model.ClassGroupMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&group).Error
	})
	if err != nil {
		common.Fail(c, 500, "delete class group failed")
		return
	}
	common.Success(c, gin.H{"id": group.ID})
}

func (h *AdminAccountHandler) UpdateClassGroupMembers(c *gin.Context) {
	groupID, ok := parseGroupIDParam(c)
	if !ok {
		return
	}

	var group model.ClassGroup
	if err := h.db.First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 404, "class group not found")
			return
		}
		common.Fail(c, 500, "query class group failed")
		return
	}

	var req dto.AdminClassGroupMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	requestedUIDs := dedupeUIDs(req.UserUIDs)
	validUIDs := make([]int64, 0, len(requestedUIDs))
	if len(requestedUIDs) > 0 {
		if err := h.db.Model(&model.User{}).Where("uid IN ?", requestedUIDs).Pluck("uid", &validUIDs).Error; err != nil {
			common.Fail(c, 500, "query accounts failed")
			return
		}
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", group.ID).Delete(&model.ClassGroupMember{}).Error; err != nil {
			return err
		}
		if len(validUIDs) == 0 {
			return nil
		}
		if err := tx.Where("user_uid IN ?", validUIDs).Delete(&model.ClassGroupMember{}).Error; err != nil {
			return err
		}
		for _, uid := range validUIDs {
			member := model.ClassGroupMember{GroupID: group.ID, UserUID: uid}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		common.Fail(c, 500, "update class group members failed")
		return
	}

	common.Success(c, classGroupView(group, validUIDs))
}

func (h *AdminAccountHandler) CopyClassGroupSelectedCourses(c *gin.Context) {
	groupID, ok := parseGroupIDParam(c)
	if !ok {
		return
	}

	var group model.ClassGroup
	if err := h.db.First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.Fail(c, 404, "class group not found")
			return
		}
		common.Fail(c, 500, "query class group failed")
		return
	}

	var req dto.AdminClassGroupCopySelectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if mode != courseCopyModeAppend && mode != courseCopyModeReplace {
		common.Fail(c, 400, "mode must be append or replace")
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

	memberUIDs, err := h.classGroupMemberUIDs(group.ID)
	if err != nil {
		common.Fail(c, 500, "query class group members failed")
		return
	}

	result, err := h.copySelectedCoursesToTargets(req.SourceUID, memberUIDs, mode, false)
	if err != nil {
		common.Fail(c, 500, err.Error())
		return
	}
	common.Success(c, result)
}

func (h *AdminAccountHandler) classGroupMemberUIDs(groupID uint) ([]int64, error) {
	var uids []int64
	err := h.db.Model(&model.ClassGroupMember{}).
		Where("group_id = ?", groupID).
		Order("created_at asc, id asc").
		Pluck("user_uid", &uids).Error
	return uids, err
}

func classGroupView(group model.ClassGroup, memberUIDs []int64) gin.H {
	if memberUIDs == nil {
		memberUIDs = []int64{}
	}
	return gin.H{
		"id":           group.ID,
		"name":         group.Name,
		"description":  group.Description,
		"member_count": len(memberUIDs),
		"member_uids":  memberUIDs,
	}
}

func parseGroupIDParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		common.Fail(c, 400, "invalid class group id")
		return 0, false
	}
	return uint(id), true
}

func dedupeUIDs(uids []int64) []int64 {
	seen := make(map[int64]struct{}, len(uids))
	out := make([]int64, 0, len(uids))
	for _, uid := range uids {
		if uid <= 0 {
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
