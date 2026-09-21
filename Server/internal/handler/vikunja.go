package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

type VikunjaHandler struct {
	db      *gorm.DB
	cc      *service.CredentialCrypto
	sync    *service.VikunjaSyncService
	baseURL string
}

func NewVikunjaHandler(db *gorm.DB, cc *service.CredentialCrypto, syncSvc *service.VikunjaSyncService, baseURL string) *VikunjaHandler {
	return &VikunjaHandler{db: db, cc: cc, sync: syncSvc, baseURL: baseURL}
}

func (h *VikunjaHandler) loadSettings(ctx context.Context, uid int64) (*model.VikunjaSettings, error) {
	var settings model.VikunjaSettings
	err := h.db.WithContext(ctx).Where("user_uid = ?", uid).First(&settings).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.VikunjaSettings{UserUID: uid}, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

func (h *VikunjaHandler) savedToken(settings *model.VikunjaSettings) (string, error) {
	if h.baseURL == "" {
		return "", service.ErrVikunjaNotConfigured
	}
	if settings.BoundInstanceURL != h.baseURL || settings.APITokenCipher == "" {
		return "", service.ErrVikunjaRebindRequired
	}
	token, err := h.cc.Decrypt(settings.APITokenCipher)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", service.ErrVikunjaRebindRequired
	}
	return strings.TrimSpace(token), nil
}

type vikunjaSettingsView struct {
	Enabled         bool       `json:"enabled"`
	BaseURL         string     `json:"base_url"`
	TokenConfigured bool       `json:"token_configured"`
	TokenMask       string     `json:"token_mask"`
	ProjectID       int64      `json:"project_id"`
	ProjectTitle    string     `json:"project_title"`
	LastSyncAt      *time.Time `json:"last_sync_at"`
	LastSyncMessage string     `json:"last_sync_message"`
}

func (h *VikunjaHandler) settingsView(settings *model.VikunjaSettings) vikunjaSettingsView {
	view := vikunjaSettingsView{
		BaseURL:         h.baseURL,
		LastSyncAt:      settings.LastSyncAt,
		LastSyncMessage: settings.LastSyncMessage,
	}
	if _, err := h.savedToken(settings); err == nil {
		view.Enabled = settings.Enabled
		view.TokenConfigured = true
		view.TokenMask = "已配置"
		view.ProjectID = settings.ProjectID
		view.ProjectTitle = settings.ProjectTitle
	}
	return view
}

// Settings GET /api/vikunja/settings
func (h *VikunjaHandler) Settings(c *gin.Context) {
	uid := common.GetUserUID(c)
	settings, err := h.loadSettings(c.Request.Context(), uid)
	if err != nil {
		common.Fail(c, 500, "load vikunja settings failed")
		return
	}
	common.Success(c, h.settingsView(settings))
}

func decodeVikunjaJSON(body io.Reader, out interface{}, allowEmpty bool) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected a single JSON object")
	}
	return nil
}

func (h *VikunjaHandler) saveSettings(ctx context.Context, snapshot, next *model.VikunjaSettings) error {
	if snapshot.ID == 0 {
		err := h.db.WithContext(ctx).Create(next).Error
		var sqlState interface{ SQLState() string }
		if errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &sqlState) && sqlState.SQLState() == "23505") {
			return service.ErrVikunjaSettingsChanged
		}
		return err
	}
	updated := h.db.WithContext(ctx).Model(&model.VikunjaSettings{}).Where(
		"user_uid = ? AND bound_instance_url = ? AND COALESCE(api_token_cipher, '') = ? AND COALESCE(project_id, 0) = ? AND enabled = ?",
		snapshot.UserUID, snapshot.BoundInstanceURL, snapshot.APITokenCipher, snapshot.ProjectID, snapshot.Enabled,
	).Updates(map[string]interface{}{
		"bound_instance_url": next.BoundInstanceURL,
		"api_token_cipher":   next.APITokenCipher,
		"project_id":         next.ProjectID,
		"project_title":      next.ProjectTitle,
		"enabled":            next.Enabled,
	})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return service.ErrVikunjaSettingsChanged
	}
	return nil
}

func failVikunja(c *gin.Context, err error) {
	var httpErr *service.VikunjaHTTPError
	var requestErr *service.VikunjaRequestError
	switch {
	case errors.Is(err, service.ErrAccountInactive):
		common.Fail(c, http.StatusUnauthorized, "账号已停用，请重新登录")
	case errors.Is(err, service.ErrVikunjaNotConfigured):
		common.Fail(c, http.StatusServiceUnavailable, service.ErrVikunjaNotConfigured.Error())
	case errors.Is(err, service.ErrVikunjaRebindRequired):
		common.Fail(c, http.StatusConflict, service.ErrVikunjaRebindRequired.Error())
	case errors.Is(err, service.ErrVikunjaSettingsChanged):
		common.Fail(c, http.StatusConflict, service.ErrVikunjaSettingsChanged.Error())
	case errors.Is(err, service.ErrVikunjaSyncDisabled):
		common.Fail(c, http.StatusBadRequest, service.ErrVikunjaSyncDisabled.Error())
	case errors.Is(err, context.Canceled):
		common.Fail(c, http.StatusRequestTimeout, "Vikunja 请求已取消")
	case errors.Is(err, context.DeadlineExceeded):
		common.Fail(c, http.StatusGatewayTimeout, "Vikunja 请求超时")
	case errors.Is(err, xxt.ErrCaptchaRequired):
		c.JSON(http.StatusOK, common.APIResponse{Code: 4301, Message: "需要完成学习通验证码校验", Data: gin.H{"captcha_required": true}})
	case errors.As(err, &httpErr):
		common.Fail(c, http.StatusBadGateway, httpErr.Error())
	case errors.As(err, &requestErr):
		common.Fail(c, http.StatusBadGateway, requestErr.Error())
	default:
		common.Fail(c, http.StatusInternalServerError, "Vikunja 操作失败")
	}
}

// UpdateSettings PUT /api/vikunja/settings
func (h *VikunjaHandler) UpdateSettings(c *gin.Context) {
	uid := common.GetUserUID(c)
	req := &struct {
		Enabled   *bool  `json:"enabled"`
		APIToken  string `json:"api_token"`
		ProjectID *int64 `json:"project_id"`
	}{}
	if err := decodeVikunjaJSON(c.Request.Body, &req, false); err != nil || req == nil || req.Enabled == nil || req.ProjectID == nil || *req.ProjectID < 0 {
		common.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	settings, err := h.loadSettings(c.Request.Context(), uid)
	if err != nil {
		common.Fail(c, http.StatusInternalServerError, "load vikunja settings failed")
		return
	}
	next := *settings
	next.Enabled = *req.Enabled
	next.ProjectID = *req.ProjectID
	next.ProjectTitle = ""
	token := strings.TrimSpace(req.APIToken)
	if next.Enabled || next.ProjectID != 0 || token != "" {
		if h.baseURL == "" {
			failVikunja(c, service.ErrVikunjaNotConfigured)
			return
		}
		if next.ProjectID <= 0 {
			common.Fail(c, http.StatusBadRequest, "请选择 Vikunja 项目")
			return
		}
		if token == "" {
			token, err = h.savedToken(settings)
			if err != nil {
				failVikunja(c, err)
				return
			}
		}
		client, err := service.NewVikunjaClient(h.baseURL, token)
		if err != nil {
			failVikunja(c, err)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		project, err := client.GetProject(ctx, next.ProjectID)
		if err != nil {
			failVikunja(c, err)
			return
		}
		next.ProjectTitle = project.Title
		next.BoundInstanceURL = h.baseURL
		if strings.TrimSpace(req.APIToken) != "" {
			next.APITokenCipher, err = h.cc.Encrypt(token)
			if err != nil {
				common.Fail(c, http.StatusInternalServerError, "encrypt token failed")
				return
			}
		}
	}
	if err := h.saveSettings(c.Request.Context(), settings, &next); err != nil {
		failVikunja(c, err)
		return
	}
	saved, err := h.loadSettings(c.Request.Context(), uid)
	if err != nil {
		common.Fail(c, http.StatusInternalServerError, "load vikunja settings failed")
		return
	}
	common.Success(c, h.settingsView(saved))
}

// TestConnection POST /api/vikunja/test — 仅向服务端固定实例读取项目列表。
// 空 Token 只能复用已绑定当前实例的凭据；测试不会改变保存的设置。
func (h *VikunjaHandler) TestConnection(c *gin.Context) {
	req := &struct {
		APIToken string `json:"api_token"`
	}{}
	if err := decodeVikunjaJSON(c.Request.Body, &req, true); err != nil || req == nil {
		common.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	if h.baseURL == "" {
		failVikunja(c, service.ErrVikunjaNotConfigured)
		return
	}
	token := strings.TrimSpace(req.APIToken)
	if token == "" {
		settings, err := h.loadSettings(c.Request.Context(), common.GetUserUID(c))
		if err != nil {
			common.Fail(c, http.StatusInternalServerError, "load vikunja settings failed")
			return
		}
		token, err = h.savedToken(settings)
		if err != nil {
			failVikunja(c, err)
			return
		}
	}
	client, err := service.NewVikunjaClient(h.baseURL, token)
	if err != nil {
		failVikunja(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	projects, err := client.ListProjects(ctx)
	if err != nil {
		failVikunja(c, err)
		return
	}
	common.Success(c, gin.H{"projects": projects})
}

// SyncNow POST /api/vikunja/sync — 手动触发一次同步。
func (h *VikunjaHandler) SyncNow(c *gin.Context) {
	uid := common.GetUserUID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
	defer cancel()
	result, err := h.sync.SyncUser(ctx, uid)
	if err != nil {
		failVikunja(c, err)
		return
	}
	common.Success(c, result)
}
