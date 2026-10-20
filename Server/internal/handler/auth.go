package handler

import (
	"bytes"
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

type loginClient interface {
	PreLogin(mobile, password string) (*xxt.LoginResult, error)
	FetchClientID(mobile, password string, info xxt.DeviceInfo) (string, error)
}

var errLoginNotAllowed = errors.New("login not allowed")

const maxFaceUploadBytes int64 = 20 << 20

type AuthHandler struct {
	db         *gorm.DB
	jwt        *service.JWTService
	cc         *service.CredentialCrypto
	xxtCli     loginClient
	facePhotos *service.FacePhotoService
}

func NewAuthHandler(db *gorm.DB, jwt *service.JWTService, cc *service.CredentialCrypto, xxtCli loginClient) *AuthHandler {
	return &AuthHandler{db: db, jwt: jwt, cc: cc, xxtCli: xxtCli}
}

// SetFacePhotoService wires the managed pool. Legacy /user/face endpoints and
// the S7b endpoints share it.
func (h *AuthHandler) SetFacePhotoService(svc *service.FacePhotoService) {
	h.facePhotos = svc
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	database := h.db.WithContext(c.Request.Context())
	wlOuter, allowed, err := h.resolveWhitelist(database, req.Mobile)
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
	deviceCipher, clientIDCipher := ensureFaceDevice(database, h.cc, h.xxtCli, loginResult.UID, req.Mobile, req.Password)

	var user model.User
	var token string
	needsLock := wlOuter.ID == 0
	err = database.Transaction(func(tx *gorm.DB) error {
		if needsLock {
			if err := tx.Exec("LOCK TABLE whitelists IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
				return err
			}
		}
		wl, allowed, err := h.resolveWhitelist(tx, req.Mobile)
		if err != nil {
			return err
		}
		if !allowed {
			return errLoginNotAllowed
		}
		if wl.ID == 0 {
			if err := tx.Create(&wl).Error; err != nil {
				return err
			}
		}
		user = model.User{
			UID:              loginResult.UID,
			Mobile:           req.Mobile,
			Name:             loginResult.Name,
			Avatar:           loginResult.Avatar,
			CredentialCipher: cipher,
			DeviceInfoCipher: deviceCipher,
			ClientIDCipher:   clientIDCipher,
			Permission:       wl.Permission,
			LastLoginAt:      time.Now(),
		}
		if err := tx.Where("uid = ?", user.UID).Assign(user).FirstOrCreate(&user).Error; err != nil {
			return err
		}
		token, err = h.jwt.Sign(user.UID, user.Mobile, user.Permission)
		return err
	})
	if err != nil {
		if errors.Is(err, errLoginNotAllowed) {
			common.Fail(c, 403, "账号未授权")
		} else {
			common.Fail(c, 500, "save login failed")
		}
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
	if h.facePhotos == nil {
		common.Fail(c, http.StatusInternalServerError, "face photo service unavailable")
		return
	}
	photos, err := h.facePhotos.List(uid, uid)
	if err != nil {
		h.failFacePhoto(c, err)
		return
	}
	if c.Query("status_only") == "1" {
		common.Success(c, gin.H{"has_face": len(photos) > 0, "count": len(photos)})
		return
	}
	resp := gin.H{"has_face": len(photos) > 0, "count": len(photos)}
	if len(photos) > 0 {
		latest := photos[len(photos)-1]
		resp["face_data"] = faceDataURL(latest.Data)
		resp["photo"] = facePhotoPayload(latest, true)
	}
	common.Success(c, resp)
}

func (h *AuthHandler) UploadFace(c *gin.Context) {
	uid := common.GetUserUID(c)
	if h.facePhotos == nil {
		common.Fail(c, http.StatusInternalServerError, "face photo service unavailable")
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
	photo, err := h.facePhotos.Add(uid, uid, compressed, "upload")
	if err != nil {
		h.failFacePhoto(c, err)
		return
	}
	common.Success(c, gin.H{
		"has_face":  true,
		"face_data": faceDataURL(photo.Data),
		"photo":     facePhotoPayload(photo, true),
	})
}

// DeleteFace is the legacy single-photo clear action: it empties the whole
// pool. New clients delete individual entries through DELETE /user/faces/:id.
func (h *AuthHandler) DeleteFace(c *gin.Context) {
	uid := common.GetUserUID(c)
	if h.facePhotos == nil {
		common.Fail(c, http.StatusInternalServerError, "face photo service unavailable")
		return
	}
	photos, err := h.facePhotos.List(uid, uid)
	if err != nil {
		h.failFacePhoto(c, err)
		return
	}
	for _, photo := range photos {
		if err := h.facePhotos.Delete(uid, photo.ID); err != nil {
			h.failFacePhoto(c, err)
			return
		}
	}
	common.Success(c, nil)
}

func readFaceUpload(c *gin.Context) ([]byte, bool) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			common.Fail(c, 400, "file is required")
			return nil, false
		}
		common.Fail(c, 400, "invalid file upload")
		return nil, false
	}
	if fileHeader.Size > maxFaceUploadBytes {
		common.Fail(c, 400, "file is too large")
		return nil, false
	}
	file, err := fileHeader.Open()
	if err != nil {
		common.Fail(c, 400, "open file failed")
		return nil, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxFaceUploadBytes+1))
	if err != nil || int64(len(raw)) > maxFaceUploadBytes {
		common.Fail(c, 400, "read file failed")
		return nil, false
	}
	return raw, true
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

func (h *AuthHandler) resolveWhitelist(database *gorm.DB, mobile string) (model.Whitelist, bool, error) {
	var cnt int64
	if err := database.Model(&model.Whitelist{}).Count(&cnt).Error; err != nil {
		return model.Whitelist{}, false, err
	}
	if cnt == 0 {
		return model.Whitelist{Mobile: mobile, Permission: 2}, true, nil
	}
	var wl model.Whitelist
	if err := database.Where("mobile = ?", mobile).First(&wl).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Whitelist{}, false, nil
		}
		return model.Whitelist{}, false, err
	}
	if wl.Permission <= 0 {
		return wl, false, nil
	}
	return wl, true, nil
}
