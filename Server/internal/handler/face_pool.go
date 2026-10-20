package handler

import (
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/common"
	"xbt2/server/internal/facephoto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

// ListFaces returns the managed pool. target_uid selects a delegated account;
// omitting it reads the caller's own pool.
func (h *AuthHandler) ListFaces(c *gin.Context) {
	targetUID, ok := faceTargetUID(c)
	if !ok {
		return
	}
	photos, err := h.facePhotos.List(common.GetUserUID(c), targetUID)
	if err != nil {
		h.failFacePhoto(c, err)
		return
	}
	items := make([]gin.H, 0, len(photos))
	for _, photo := range photos {
		items = append(items, facePhotoPayload(photo, true))
	}
	common.Success(c, gin.H{
		"user_uid": targetUID,
		"max":      facephoto.MaxPhotos,
		"photos":   items,
	})
}

func (h *AuthHandler) AddFace(c *gin.Context) {
	targetUID, ok := faceTargetUID(c)
	if !ok {
		return
	}
	raw, ok := readFaceUpload(c)
	if !ok {
		return
	}
	compressed, err := resizeFace(raw)
	if err != nil {
		common.Fail(c, 400, "图片处理失败："+err.Error())
		return
	}
	photo, err := h.facePhotos.Add(common.GetUserUID(c), targetUID, compressed, "upload")
	if err != nil {
		h.failFacePhoto(c, err)
		return
	}
	common.Success(c, facePhotoPayload(photo, true))
}

func (h *AuthHandler) DeleteFaceByID(c *gin.Context) {
	photoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || photoID == 0 {
		common.Fail(c, 400, "invalid face photo id")
		return
	}
	if err := h.facePhotos.Delete(common.GetUserUID(c), uint(photoID)); err != nil {
		h.failFacePhoto(c, err)
		return
	}
	common.Success(c, nil)
}

// ImportFace pulls the account's official photo, transforms it in memory and
// stores only the transform output.
func (h *AuthHandler) ImportFace(c *gin.Context) {
	targetUID, ok := faceTargetUID(c)
	if !ok {
		return
	}
	photo, transformed, err := h.facePhotos.ImportOfficial(common.GetUserUID(c), targetUID)
	if err != nil {
		switch {
		case errors.Is(err, xxt.ErrNoProfileFacePhoto):
			common.Fail(c, http.StatusUnprocessableEntity, "该账号未设置学习通官方人脸照片，无法导入")
		case errors.Is(err, service.ErrFacePhotoForbidden), errors.Is(err, service.ErrFacePhotoLimit),
			errors.Is(err, service.ErrFacePhotoTooLarge), errors.Is(err, service.ErrFacePhotoNotFound):
			h.failFacePhoto(c, err)
		case strings.Contains(err.Error(), "transform official face photo"):
			common.Fail(c, http.StatusUnprocessableEntity, "官方人脸照片处理失败，请稍后重试")
		default:
			log.Printf("import official face photo: target_uid=%d err=%v", targetUID, err)
			common.Fail(c, http.StatusBadGateway, "导入官方人脸照片失败，请稍后重试")
		}
		return
	}
	payload := facePhotoPayload(photo, true)
	payload["transform"] = gin.H{
		"width":             transformed.Width,
		"height":            transformed.Height,
		"crop_width_ratio":  transformed.CropWidthRatio,
		"crop_height_ratio": transformed.CropHeightRatio,
		"angle_degrees":     transformed.AngleDegrees,
		"quality":           transformed.Quality,
	}
	common.Success(c, payload)
}

func faceTargetUID(c *gin.Context) (int64, bool) {
	uid := common.GetUserUID(c)
	raw := strings.TrimSpace(c.Query("target_uid"))
	if raw == "" {
		return uid, true
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		common.Fail(c, 400, "invalid target_uid")
		return 0, false
	}
	return parsed, true
}

func facePhotoPayload(photo model.FacePhoto, includeImage bool) gin.H {
	payload := gin.H{
		"id":                photo.ID,
		"user_uid":          photo.UserUID,
		"object_id":         photo.ObjectID,
		"use_count":         photo.UseCount,
		"is_failure_before": photo.IsFailureBefore,
		"source":            photo.Source,
		"created_at":        photo.CreatedAt,
	}
	if includeImage {
		payload["image"] = faceDataURL(photo.Data)
	}
	return payload
}

func faceDataURL(data []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
}

func (h *AuthHandler) failFacePhoto(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrFacePhotoForbidden):
		common.Fail(c, http.StatusForbidden, "无权管理该账号的人脸照片")
	case errors.Is(err, service.ErrFacePhotoLimit):
		common.Fail(c, http.StatusConflict, "每个账号最多保存 5 张人脸照片")
	case errors.Is(err, service.ErrFacePhotoNotFound):
		common.Fail(c, http.StatusNotFound, "人脸照片不存在")
	case errors.Is(err, service.ErrFacePhotoTooLarge):
		common.Fail(c, 400, "人脸照片超过 100KB 上限")
	case errors.Is(err, facephoto.ErrNoPhotos):
		common.Fail(c, http.StatusNotFound, "该账号还没有人脸照片")
	default:
		log.Printf("face photo request failed: uid=%d err=%v", common.GetUserUID(c), err)
		common.Fail(c, http.StatusInternalServerError, "人脸照片操作失败")
	}
}
