package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/qmx"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

type QMXRoomCheckHandler struct {
	client *qmx.Client
	db     *gorm.DB
	xxt    *xxt.Client
	cc     *service.CredentialCrypto
}

func NewQMXRoomCheckHandler(client *qmx.Client, db *gorm.DB, xxtClient *xxt.Client, cc *service.CredentialCrypto) *QMXRoomCheckHandler {
	return &QMXRoomCheckHandler{client: client, db: db, xxt: xxtClient, cc: cc}
}

func (h *QMXRoomCheckHandler) Preview(c *gin.Context) {
	var req dto.QMXRoomCheckPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	input, err := h.credentialInput(c, req.QMXRoomCheckCredentialRequest)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	preview, err := h.client.Preview(input)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	common.Success(c, preview)
}

func (h *QMXRoomCheckHandler) Execute(c *gin.Context) {
	var req dto.QMXRoomCheckExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}
	input, err := h.credentialInput(c, req.QMXRoomCheckCredentialRequest)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	result, err := h.client.Execute(qmx.ExecuteInput{
		CredentialInput: input,
		LocationIndex:   req.LocationIndex,
		Longitude:       req.Longitude,
		Latitude:        req.Latitude,
		LocationName:    strings.TrimSpace(req.LocationName),
	})
	if err != nil {
		if len(result.Unsupported) > 0 {
			common.Fail(c, 400, "unsupported QMX room check requirements: "+strings.Join(result.Unsupported, ", "))
			return
		}
		common.Fail(c, 400, err.Error())
		return
	}
	common.Success(c, result)
}

func (h *QMXRoomCheckHandler) credentialInput(c *gin.Context, req dto.QMXRoomCheckCredentialRequest) (qmx.CredentialInput, error) {
	input := qmx.CredentialInput{
		QMXURL: strings.TrimSpace(req.QMXURL),
		XToken: strings.TrimSpace(req.XToken),
		Cookie: strings.TrimSpace(req.Cookie),
		Raw:    strings.TrimSpace(req.Raw),
	}
	if hasProvidedQMXCredential(input) {
		return input, nil
	}

	uid := common.GetUserUID(c)
	var user model.User
	if err := h.db.Where("uid = ?", uid).First(&user).Error; err != nil {
		return input, err
	}
	password, err := h.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		return input, err
	}
	cookie, err := h.xxt.CookieHeader(user.Mobile, password, "https://sw.qmx.chaoxing.com/")
	if err != nil {
		return input, err
	}
	input.Cookie = cookie
	return input, nil
}

func hasProvidedQMXCredential(input qmx.CredentialInput) bool {
	combined := strings.Join([]string{input.XToken, input.Cookie, input.Raw}, "\n")
	if strings.Contains(combined, "X-Token:") || strings.Contains(combined, "Cookie:") || strings.Contains(combined, "cx_qmx_token=") {
		return true
	}
	raw := strings.TrimSpace(input.Raw)
	if strings.HasPrefix(raw, "eyJ") && strings.Count(raw, ".") == 2 {
		return true
	}
	if strings.Contains(raw, ";") && strings.Contains(raw, "=") {
		return true
	}
	return false
}
