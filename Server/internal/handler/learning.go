package handler

import (
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
		if isXXTAuthError(err) {
			common.Fail(c, 401, "学习通登录已失效，请使用新密码重新登录")
			return
		}
		common.Fail(c, 500, "query learning dashboard failed: "+err.Error())
		return
	}
	common.Success(c, dashboard)
}
