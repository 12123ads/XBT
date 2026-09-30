package handler

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/image/draw"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const maxFaceUploadBytes int64 = 20 << 20

type AuthHandler struct {
	db     *gorm.DB
	jwt    *service.JWTService
	cc     *service.CredentialCrypto
	xxtCli *xxt.Client
}

func NewAuthHandler(db *gorm.DB, jwt *service.JWTService, cc *service.CredentialCrypto, xxtCli *xxt.Client) *AuthHandler {
	return &AuthHandler{db: db, jwt: jwt, cc: cc, xxtCli: xxtCli}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	wl, allowed, err := h.resolveWhitelist(req.Mobile)
	if err != nil {
		common.Fail(c, 500, err.Error())
		return
	}
	if !allowed {
		common.Fail(c, 403, "账号未授权")
		return
	}

	loginResult, err := h.xxtCli.PreLogin(req.Mobile, req.Password)
	if err != nil {
		common.Fail(c, 401, err.Error())
		return
	}

	cipher, err := h.cc.Encrypt(req.Password)
	if err != nil {
		common.Fail(c, 500, "credential encrypt failed")
		return
	}

	user := model.User{
		UID:              loginResult.UID,
		Mobile:           req.Mobile,
		Name:             loginResult.Name,
		Avatar:           loginResult.Avatar,
		CredentialCipher: cipher,
		Permission:       wl.Permission,
		LastLoginAt:      time.Now(),
	}
	if err := h.db.Where("uid = ?", user.UID).Assign(user).FirstOrCreate(&user).Error; err != nil {
		common.Fail(c, 500, "save user failed")
		return
	}

	token, err := h.jwt.Sign(user.UID, user.Mobile, user.Permission)
	if err != nil {
		common.Fail(c, 500, "token generate failed")
		return
	}

	common.Success(c, dto.LoginResponse{
		Token: token,
		User: gin.H{
			"uid":        user.UID,
			"name":       user.Name,
			"mobile":     common.MaskMobile(user.Mobile),
			"avatar":     user.Avatar,
			"permission": user.Permission,
		},
	})
}

func (h *AuthHandler) GetFace(c *gin.Context) {
	uid := common.GetUserUID(c)
	if c.Query("status_only") == "1" {
		var count int64
		h.db.Model(&model.User{}).Where("uid = ? AND length(face_photo) > 0", uid).Count(&count)
		common.Success(c, gin.H{"has_face": count > 0})
		return
	}
	var user model.User
	if err := h.db.Select("face_photo").Where("uid = ?", uid).First(&user).Error; err != nil {
		common.Fail(c, 404, "user not found")
		return
	}
	resp := gin.H{"has_face": len(user.FacePhoto) > 0}
	if len(user.FacePhoto) > 0 {
		resp["face_data"] = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(user.FacePhoto)
	}
	common.Success(c, resp)
}

func (h *AuthHandler) UploadFace(c *gin.Context) {
	uid := common.GetUserUID(c)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			common.Fail(c, 400, "file is required")
			return
		}
		common.Fail(c, 400, "invalid file upload")
		return
	}
	if fileHeader.Size > maxFaceUploadBytes {
		common.Fail(c, 400, "file is too large")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		common.Fail(c, 400, "open file failed")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxFaceUploadBytes+1))
	if err != nil || int64(len(raw)) > maxFaceUploadBytes {
		common.Fail(c, 400, "read file failed")
		return
	}

	compressed, err := resizeFace(raw)
	if err != nil {
		common.Fail(c, 400, "图片处理失败："+err.Error())
		return
	}

	if err := h.db.Model(&model.User{}).Where("uid = ?", uid).Update("face_photo", compressed).Error; err != nil {
		common.Fail(c, 500, "save face photo failed")
		return
	}
	common.Success(c, gin.H{
		"has_face":  true,
		"face_data": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(compressed),
	})
}

func (h *AuthHandler) DeleteFace(c *gin.Context) {
	uid := common.GetUserUID(c)
	if err := h.db.Model(&model.User{}).Where("uid = ?", uid).Update("face_photo", nil).Error; err != nil {
		common.Fail(c, 500, "clear face photo failed")
		return
	}
	common.Success(c, nil)
}

func resizeFace(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	const maxW, maxH = 720, 1280
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	if w > maxW || h > maxH {
		scale := min(float64(maxW)/float64(w), float64(maxH)/float64(h))
		w = int(float64(w) * scale)
		h = int(float64(h) * scale)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		src = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 75}); err != nil {
		return nil, err
	}

	const maxBytes = 100 * 1024
	if buf.Len() <= maxBytes {
		return buf.Bytes(), nil
	}

	for q := 60; q >= 30; q -= 10 {
		buf.Reset()
		if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if buf.Len() <= maxBytes {
			break
		}
	}
	return buf.Bytes(), nil
}

func (h *AuthHandler) resolveWhitelist(mobile string) (model.Whitelist, bool, error) {
	var cnt int64
	if err := h.db.Model(&model.Whitelist{}).Count(&cnt).Error; err != nil {
		return model.Whitelist{}, false, err
	}
	if cnt == 0 {
		bootstrap := model.Whitelist{Mobile: mobile, Permission: 2}
		if err := h.db.Create(&bootstrap).Error; err != nil {
			return model.Whitelist{}, false, err
		}
		return bootstrap, true, nil
	}
	var wl model.Whitelist
	if err := h.db.Where("mobile = ?", mobile).First(&wl).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return model.Whitelist{}, false, nil
		}
		return model.Whitelist{}, false, err
	}
	if wl.Permission <= 0 {
		return wl, false, nil
	}
	return wl, true, nil
}
