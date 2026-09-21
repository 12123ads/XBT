package handler

import (
	"errors"
	"regexp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

type LearningHandler struct {
	db  *gorm.DB
	xxt *xxt.Client
	cc  *service.CredentialCrypto
}

func NewLearningHandler(db *gorm.DB, xxtClient *xxt.Client, cc *service.CredentialCrypto) *LearningHandler {
	return &LearningHandler{db: db, xxt: xxtClient, cc: cc}
}

// code 4301：学习通反爬验证被触发，前端应弹出验证码弹窗。
const learningCaptchaRequired = 4301

func (h *LearningHandler) Dashboard(c *gin.Context) {
	uid := common.GetUserUID(c)
	var user model.User
	if err := h.db.Where("uid = ?", uid).First(&user).Error; err != nil {
		common.Fail(c, 404, "user not found")
		return
	}
	password, err := h.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		common.Fail(c, 400, "credential expired, please login again")
		return
	}
	dashboard, err := h.xxt.GetLearningDashboard(user.Mobile, password)
	if err != nil {
		if errors.Is(err, xxt.ErrCaptchaRequired) {
			c.JSON(200, common.APIResponse{Code: learningCaptchaRequired, Message: "需要完成学习通验证码校验", Data: gin.H{"captcha_required": true}})
			return
		}
		if isXXTAuthError(err) {
			common.Fail(c, 401, "学习通登录已失效，请使用新密码重新登录")
			return
		}
		common.Fail(c, 500, "query learning dashboard failed: "+err.Error())
		return
	}
	common.Success(c, dashboard)
}

func (h *LearningHandler) resolveUserCredential(c *gin.Context) (mobile, password string, ok bool) {
	uid := common.GetUserUID(c)
	var user model.User
	if err := h.db.Where("uid = ?", uid).First(&user).Error; err != nil {
		common.Fail(c, 404, "user not found")
		return "", "", false
	}
	password, err := h.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		common.Fail(c, 400, "credential expired, please login again")
		return "", "", false
	}
	return user.Mobile, password, true
}

// CaptchaImage 返回反爬验证码图片（data URL）。
func (h *LearningHandler) CaptchaImage(c *gin.Context) {
	mobile, password, ok := h.resolveUserCredential(c)
	if !ok {
		return
	}
	image, err := h.xxt.GetLearningCaptcha(mobile, password)
	if err != nil {
		if errors.Is(err, xxt.ErrNotBlocked) {
			common.Success(c, gin.H{"image": "", "blocked": false})
			return
		}
		if isXXTAuthError(err) {
			common.Fail(c, 401, "学习通登录已失效，请使用新密码重新登录")
			return
		}
		common.Fail(c, 500, "query captcha image failed: "+err.Error())
		return
	}
	common.Success(c, gin.H{"image": image, "blocked": true})
}

// CaptchaSubmit 提交用户输入的验证码，成功即解除反爬封拦。
func (h *LearningHandler) CaptchaSubmit(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	if !regexp.MustCompile(`^[0-9a-zA-Z]{4}$`).MatchString(req.Code) {
		common.Fail(c, 400, "验证码格式不正确")
		return
	}
	mobile, password, ok := h.resolveUserCredential(c)
	if !ok {
		return
	}
	if err := h.xxt.SubmitLearningCaptcha(mobile, password, req.Code); err != nil {
		if errors.Is(err, xxt.ErrNotBlocked) {
			common.Success(c, gin.H{"verified": true, "blocked": false})
			return
		}
		if isXXTAuthError(err) {
			common.Fail(c, 401, "学习通登录已失效，请使用新密码重新登录")
			return
		}
		common.Fail(c, 400, err.Error())
		return
	}
	common.Success(c, gin.H{"verified": true})
}
