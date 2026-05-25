package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
)

const qmxAutoSignRecordPageSizeMax = 100

type AdminQMXAutoSignHandler struct {
	db      *gorm.DB
	service *service.QMXAutoSignService
}

func NewAdminQMXAutoSignHandler(db *gorm.DB, svc *service.QMXAutoSignService) *AdminQMXAutoSignHandler {
	return &AdminQMXAutoSignHandler{db: db, service: svc}
}

func (h *AdminQMXAutoSignHandler) Overview(c *gin.Context) {
	settings, err := h.service.EnsureSettings()
	if err != nil {
		common.Fail(c, 500, "query QMX auto sign settings failed")
		return
	}

	var users []model.User
	if err := h.db.Order("permission desc, last_login_at desc, name asc").Find(&users).Error; err != nil {
		common.Fail(c, 500, "query accounts failed")
		return
	}

	configs, err := h.accountConfigMap()
	if err != nil {
		common.Fail(c, 500, "query QMX auto sign accounts failed")
		return
	}

	accounts := make([]gin.H, 0, len(users))
	for _, user := range users {
		lastRecord, err := h.lastRecord(user.UID)
		if err != nil {
			common.Fail(c, 500, "query QMX auto sign records failed")
			return
		}
		accounts = append(accounts, h.accountView(user, configs[user.UID], lastRecord))
	}

	common.Success(c, gin.H{
	"settings":             h.settingsView(settings),
	"accounts":             accounts,
	"qmx_location_presets": h.service.Presets(),
	})
}

func (h *AdminQMXAutoSignHandler) UpdateSettings(c *gin.Context) {
	var req dto.AdminQMXAutoSignSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	settings, err := h.service.UpdateSettingsEnabled(*req.Enabled)
	if err != nil {
		common.Fail(c, 500, "update QMX auto sign settings failed")
		return
	}
	common.Success(c, h.settingsView(settings))
}

func (h *AdminQMXAutoSignHandler) PreviewLocations(c *gin.Context) {
	uid, ok := parseQMXAutoSignUIDParam(c)
	if !ok {
		return
	}
	if !h.accountExists(uid) {
		common.Fail(c, 404, "account not found")
		return
	}

	preview, err := h.service.PreviewLocations(uid)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	common.Success(c, preview)
}

func (h *AdminQMXAutoSignHandler) UpdateAccount(c *gin.Context) {
	uid, ok := parseQMXAutoSignUIDParam(c)
	if !ok {
		return
	}
	if !h.accountExists(uid) {
		common.Fail(c, 404, "account not found")
		return
	}

	var req dto.AdminQMXAutoSignAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	account, err := h.loadOrInitAccount(uid)
	if err != nil {
		common.Fail(c, 500, "query QMX auto sign account failed")
		return
	}

	if req.Location != nil {
		name := strings.TrimSpace(req.Location.LocationName)
		if name == "" || req.Location.LocationIndex < 0 {
			common.Fail(c, 400, "valid location is required")
			return
		}
		account.LocationName = name
		account.LocationIndex = req.Location.LocationIndex
		account.Longitude = req.Location.Longitude
		account.Latitude = req.Location.Latitude
		account.Range = req.Location.Range
	}
	account.Enabled = *req.Enabled
	if account.Enabled && !hasConfiguredQMXLocation(account) {
		common.Fail(c, 400, "please choose a QMX location before enabling this account")
		return
	}

	if account.ID == 0 {
		if err := h.db.Create(&account).Error; err != nil {
			common.Fail(c, 500, "save QMX auto sign account failed")
			return
		}
	} else if err := h.db.Save(&account).Error; err != nil {
		common.Fail(c, 500, "save QMX auto sign account failed")
		return
	}
	common.Success(c, h.accountConfigView(account))
}

func (h *AdminQMXAutoSignHandler) RunAccount(c *gin.Context) {
	uid, ok := parseQMXAutoSignUIDParam(c)
	if !ok {
		return
	}
	if !h.accountExists(uid) {
		common.Fail(c, 404, "account not found")
		return
	}

	record, err := h.service.RunAccount(uid, service.QMXAutoSignTriggerManual)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	common.Success(c, h.recordView(record, nil))
}

func (h *AdminQMXAutoSignHandler) Records(c *gin.Context) {
	page := parsePositiveQuery(c, "page", 1)
	pageSize := parsePositiveQuery(c, "page_size", 20)
	if pageSize > qmxAutoSignRecordPageSizeMax {
		pageSize = qmxAutoSignRecordPageSizeMax
	}

	query := h.db.Model(&model.QMXAutoSignRecord{})
	if trigger := strings.TrimSpace(c.Query("trigger")); trigger != "" {
		query = query.Where("trigger = ?", trigger)
	}
	if uid := strings.TrimSpace(c.Query("user_uid")); uid != "" {
		if parsed, err := strconv.ParseInt(uid, 10, 64); err == nil && parsed > 0 {
			query = query.Where("user_uid = ?", parsed)
		}
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.Fail(c, 500, "query QMX auto sign records failed")
		return
	}

	var records []model.QMXAutoSignRecord
	if err := query.Order("executed_at desc, id desc").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error; err != nil {
		common.Fail(c, 500, "query QMX auto sign records failed")
		return
	}

	users, err := h.userMap(records)
	if err != nil {
		common.Fail(c, 500, "query accounts failed")
		return
	}

	items := make([]gin.H, 0, len(records))
	for _, record := range records {
		items = append(items, h.recordView(record, users))
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	common.Success(c, gin.H{
		"items":       items,
		"page":        page,
		"page_size":   pageSize,
		"total":       total,
		"total_pages": totalPages,
	})
}

func (h *AdminQMXAutoSignHandler) settingsView(settings model.QMXAutoSignSetting) gin.H {
	timezone := strings.TrimSpace(settings.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	runAt := strings.TrimSpace(settings.RunAt)
	if runAt == "" {
		runAt = "22:00"
	}
	return gin.H{
		"enabled":     settings.Enabled,
		"timezone":    timezone,
		"run_at":      runAt,
		"next_run_at": h.service.NextRunAt(time.Now()).UnixMilli(),
	}
}

func (h *AdminQMXAutoSignHandler) accountConfigView(account model.QMXAutoSignAccount) gin.H {
	return gin.H{
		"user_uid":       account.UserUID,
		"enabled":        account.Enabled,
		"location_name":  account.LocationName,
		"location_index": account.LocationIndex,
		"longitude":      account.Longitude,
		"latitude":       account.Latitude,
		"range":          account.Range,
	}
}

func (h *AdminQMXAutoSignHandler) accountView(user model.User, account *model.QMXAutoSignAccount, lastRecord *model.QMXAutoSignRecord) gin.H {
	config := model.QMXAutoSignAccount{UserUID: user.UID, LocationIndex: -1}
	if account != nil {
		config = *account
	}
	var last gin.H
	if lastRecord != nil {
		last = h.recordView(*lastRecord, map[int64]model.User{user.UID: user})
	}
	return gin.H{
		"uid":           user.UID,
		"name":          user.Name,
		"mobile_masked": common.MaskMobile(user.Mobile),
		"avatar":        user.Avatar,
		"permission":    user.Permission,
		"config":        h.accountConfigView(config),
		"last_record":   last,
	}
}

func (h *AdminQMXAutoSignHandler) recordView(record model.QMXAutoSignRecord, users map[int64]model.User) gin.H {
	name := ""
	mobileMasked := ""
	if users != nil {
		if user, ok := users[record.UserUID]; ok {
			name = user.Name
			mobileMasked = common.MaskMobile(user.Mobile)
		}
	}
	return gin.H{
		"id":            record.ID,
		"user_uid":      record.UserUID,
		"name":          name,
		"mobile_masked": mobileMasked,
		"trigger":       record.Trigger,
		"success":       record.Success,
		"code":          record.Code,
		"message":       record.Message,
		"batch_name":    record.BatchName,
		"check_date":    record.CheckDate,
		"check_time":    record.CheckTime,
		"location_name": record.LocationName,
		"longitude":     record.Longitude,
		"latitude":      record.Latitude,
		"executed_at":   record.ExecutedAt.UnixMilli(),
	}
}

func (h *AdminQMXAutoSignHandler) accountConfigMap() (map[int64]*model.QMXAutoSignAccount, error) {
	var accounts []model.QMXAutoSignAccount
	if err := h.db.Find(&accounts).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]*model.QMXAutoSignAccount, len(accounts))
	for i := range accounts {
		out[accounts[i].UserUID] = &accounts[i]
	}
	return out, nil
}

func (h *AdminQMXAutoSignHandler) lastRecord(uid int64) (*model.QMXAutoSignRecord, error) {
	var record model.QMXAutoSignRecord
	err := h.db.Where("user_uid = ?", uid).Order("executed_at desc, id desc").Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (h *AdminQMXAutoSignHandler) userMap(records []model.QMXAutoSignRecord) (map[int64]model.User, error) {
	seen := map[int64]struct{}{}
	uids := make([]int64, 0, len(records))
	for _, record := range records {
		if _, ok := seen[record.UserUID]; ok {
			continue
		}
		seen[record.UserUID] = struct{}{}
		uids = append(uids, record.UserUID)
	}
	if len(uids) == 0 {
		return map[int64]model.User{}, nil
	}
	var users []model.User
	if err := h.db.Where("uid IN ?", uids).Find(&users).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]model.User, len(users))
	for _, user := range users {
		out[user.UID] = user
	}
	return out, nil
}

func (h *AdminQMXAutoSignHandler) loadOrInitAccount(uid int64) (model.QMXAutoSignAccount, error) {
	var account model.QMXAutoSignAccount
	err := h.db.Where("user_uid = ?", uid).Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.QMXAutoSignAccount{UserUID: uid, LocationIndex: -1}, nil
	}
	return account, err
}

func (h *AdminQMXAutoSignHandler) accountExists(uid int64) bool {
	var count int64
	return h.db.Model(&model.User{}).Where("uid = ?", uid).Count(&count).Error == nil && count > 0
}

func parseQMXAutoSignUIDParam(c *gin.Context) (int64, bool) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || uid <= 0 {
		common.Fail(c, 400, "invalid uid")
		return 0, false
	}
	return uid, true
}

func parsePositiveQuery(c *gin.Context, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(c.Query(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func hasConfiguredQMXLocation(account model.QMXAutoSignAccount) bool {
	return strings.TrimSpace(account.LocationName) != "" && account.LocationIndex >= 0
}
