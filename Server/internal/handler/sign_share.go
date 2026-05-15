package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const signShareTokenBytes = 32

var (
	errSignShareInvalid          = errors.New("sign share invalid")
	errSignShareUsed             = errors.New("sign share used")
	errSignShareExpired          = errors.New("sign share expired")
	errSignShareCourseUnselected = errors.New("sign share course unselected")
)

func (h *SignHandler) CreateShare(c *gin.Context) {
	uid := common.GetUserUID(c)
	var req dto.SignShareCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if req.ActivityID <= 0 || req.CourseID <= 0 || req.ClassID <= 0 || req.EndTime <= 0 {
		common.Fail(c, 400, "invalid request params")
		return
	}
	if !isSupportedShareSignType(req.SignType) {
		common.Fail(c, 400, "unsupported sign_type")
		return
	}
	expiresAt := time.UnixMilli(req.EndTime)
	if !expiresAt.After(time.Now()) {
		common.Fail(c, 400, "该签到已结束，无法生成分享链接")
		return
	}
	ok, err := h.userSelectedCourse(uid, req.CourseID, req.ClassID)
	if err != nil {
		common.Fail(c, 500, "query selected course failed")
		return
	}
	if !ok {
		common.Fail(c, 403, "当前账号未选择该课程，无法生成分享链接")
		return
	}

	token, tokenHash, err := generateSignShareToken()
	if err != nil {
		common.Fail(c, 500, "generate share token failed")
		return
	}

	share := model.SignShare{
		TokenHash:     tokenHash,
		CreatorUID:    uid,
		ActivityID:    req.ActivityID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		SignType:      req.SignType,
		IfRefreshEWM:  req.IfRefreshEWM,
		ActivityName:  fallbackName(req.ActivityName, "签到活动"),
		CourseName:    fallbackName(req.CourseName, "课程"),
		CourseTeacher: strings.TrimSpace(req.CourseTeacher),
		ExpiresAt:     expiresAt,
	}
	if err := h.db.Create(&share).Error; err != nil {
		common.Fail(c, 500, "create share failed")
		return
	}

	common.Success(c, gin.H{
		"token":      token,
		"expires_at": share.ExpiresAt.UnixMilli(),
	})
}

func (h *SignHandler) GetShare(c *gin.Context) {
	share, err := h.loadUsableShare(c.Param("token"))
	if err != nil {
		h.failShare(c, err)
		return
	}
	common.Success(c, sharePublicView(share))
}

func (h *SignHandler) ExecuteShare(c *gin.Context) {
	share, err := h.loadUsableShare(c.Param("token"))
	if err != nil {
		h.failShare(c, err)
		return
	}

	var req dto.SignShareExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if req.Special == nil {
		req.Special = map[string]interface{}{}
	}
	if err := validateShareSpecialParams(share.SignType, req.Special); err != nil {
		common.Fail(c, 400, err.Error())
		return
	}

	targetUIDs, err := h.signShareTargetUIDs(share)
	if err != nil {
		if errors.Is(err, errSignShareCourseUnselected) {
			h.failShare(c, err)
			return
		}
		common.Fail(c, 500, "query sign targets failed")
		return
	}

	summary := gin.H{
		"target_count":         len(targetUIDs),
		"success_count":        0,
		"already_signed_count": 0,
		"failed_count":         0,
		"used":                 false,
		"message":              "",
		"failures":             []string{},
	}

	failures := make([]string, 0)
	successCount := 0
	alreadySignedCount := 0
	failedCount := 0
	for _, targetUID := range targetUIDs {
		res := h.signService.ExecuteOne(share.CreatorUID, service.ExecuteSignRequest{
			ActivityID:   share.ActivityID,
			TargetUID:    targetUID,
			SignType:     share.SignType,
			CourseID:     share.CourseID,
			ClassID:      share.ClassID,
			IfRefreshEWM: share.IfRefreshEWM,
			Special:      req.Special,
		})
		if res.Success || res.AlreadySigned {
			if res.AlreadySigned {
				alreadySignedCount++
			} else {
				successCount++
			}
			continue
		}
		failedCount++
		if res.Message != "" {
			failures = append(failures, res.Message)
		}
	}

	allDone := failedCount == 0
	if allDone {
		now := time.Now()
		if err := h.db.Model(&model.SignShare{}).
			Where("id = ? AND used_at IS NULL", share.ID).
			Update("used_at", now).Error; err != nil {
			common.Fail(c, 500, "mark share used failed")
			return
		}
		summary["used"] = true
		summary["message"] = "签到完成，分享链接已失效"
	} else {
		summary["message"] = "部分账号签到失败，链接仍可在活动结束前重试"
	}

	summary["success_count"] = successCount
	summary["already_signed_count"] = alreadySignedCount
	summary["failed_count"] = failedCount
	summary["failures"] = dedupeStrings(failures)
	common.Success(c, summary)
}

func (h *SignHandler) loadUsableShare(token string) (model.SignShare, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return model.SignShare{}, errSignShareInvalid
	}
	tokenHash := hashSignShareToken(token)
	var share model.SignShare
	if err := h.db.Where("token_hash = ?", tokenHash).Take(&share).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.SignShare{}, errSignShareInvalid
		}
		return model.SignShare{}, err
	}
	if share.UsedAt != nil {
		return model.SignShare{}, errSignShareUsed
	}
	if !share.ExpiresAt.After(time.Now()) {
		return model.SignShare{}, errSignShareExpired
	}
	ok, err := h.userSelectedCourse(share.CreatorUID, share.CourseID, share.ClassID)
	if err != nil {
		return model.SignShare{}, err
	}
	if !ok {
		return model.SignShare{}, errSignShareCourseUnselected
	}
	return share, nil
}

func (h *SignHandler) signShareTargetUIDs(share model.SignShare) ([]int64, error) {
	var uids []int64
	if err := h.db.Model(&model.UserCourse{}).
		Where("course_id = ? AND class_id = ? AND is_selected = true", share.CourseID, share.ClassID).
		Order("CASE WHEN user_uid = "+fmt.Sprint(share.CreatorUID)+" THEN 0 ELSE 1 END").
		Order("user_uid ASC").
		Pluck("user_uid", &uids).Error; err != nil {
		return nil, err
	}
	uids = dedupeUIDTargets(uids, 0)
	for _, uid := range uids {
		if uid == share.CreatorUID {
			return uids, nil
		}
	}
	return nil, errSignShareCourseUnselected
}

func (h *SignHandler) userSelectedCourse(uid, courseID, classID int64) (bool, error) {
	var count int64
	if err := h.db.Model(&model.UserCourse{}).
		Where("user_uid = ? AND course_id = ? AND class_id = ? AND is_selected = true", uid, courseID, classID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (h *SignHandler) failShare(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errSignShareInvalid):
		common.Fail(c, 404, "分享链接不存在")
	case errors.Is(err, errSignShareUsed):
		common.Fail(c, 410, "分享链接已失效")
	case errors.Is(err, errSignShareExpired):
		common.Fail(c, 410, "分享链接已过期")
	case errors.Is(err, errSignShareCourseUnselected):
		common.Fail(c, 410, "分享者已取消该课程，链接已失效")
	default:
		common.Fail(c, 500, "query share failed")
	}
}

func generateSignShareToken() (string, string, error) {
	buf := make([]byte, signShareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, hashSignShareToken(token), nil
}

func hashSignShareToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func isSupportedShareSignType(signType int) bool {
	switch signType {
	case xxt.SignNormal, xxt.SignQRCode, xxt.SignGesture, xxt.SignLocation, xxt.SignCode:
		return true
	default:
		return false
	}
}

func validateShareSpecialParams(signType int, special map[string]interface{}) error {
	switch signType {
	case xxt.SignGesture, xxt.SignCode:
		if strings.TrimSpace(strFromSpecial(special, "sign_code")) == "" {
			return errors.New("请填写签到码或手势")
		}
	case xxt.SignLocation:
		if strings.TrimSpace(strFromSpecial(special, "latitude")) == "" || strings.TrimSpace(strFromSpecial(special, "longitude")) == "" {
			return errors.New("请选择签到位置")
		}
	case xxt.SignQRCode:
		if strings.TrimSpace(strFromSpecial(special, "enc")) == "" {
			return errors.New("请扫描有效的学习通二维码")
		}
	}
	return nil
}

func strFromSpecial(special map[string]interface{}, key string) string {
	value, ok := special[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func sharePublicView(share model.SignShare) gin.H {
	return gin.H{
		"activity_id":    share.ActivityID,
		"activity_name":  share.ActivityName,
		"course_id":      share.CourseID,
		"class_id":       share.ClassID,
		"course_name":    share.CourseName,
		"course_teacher": share.CourseTeacher,
		"sign_type":      share.SignType,
		"if_refresh_ewm": share.IfRefreshEWM,
		"expires_at":     share.ExpiresAt.UnixMilli(),
	}
}

func fallbackName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func dedupeStrings(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
