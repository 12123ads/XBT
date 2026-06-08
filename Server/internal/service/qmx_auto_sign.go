package service

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"xbt2/server/internal/config"
	"xbt2/server/internal/model"
	"xbt2/server/internal/qmx"
	"xbt2/server/internal/xxt"
)

const (
	QMXAutoSignTriggerScheduled = "scheduled"
	QMXAutoSignTriggerManual    = "manual"

	qmxAutoSignTimezone       = "Asia/Shanghai"
	qmxAutoSignRunAt          = "22:00"
	qmxAutoSignMaxConcurrency = 3
)

type QMXAutoSignService struct {
	db         *gorm.DB
	client     *qmx.Client
	xxt        *xxt.Client
	cc         *CredentialCrypto
	loc        *time.Location
	cfgPresets []config.QMXLocationPreset
	qmxWebhook *EnterpriseWechatWebhookNotifier
}

func NewQMXAutoSignService(db *gorm.DB, client *qmx.Client, xxtClient *xxt.Client, cc *CredentialCrypto, presets []config.QMXLocationPreset, qmxWebhook *EnterpriseWechatWebhookNotifier) *QMXAutoSignService {
	loc, err := time.LoadLocation(qmxAutoSignTimezone)
	if err != nil {
		loc = time.FixedZone(qmxAutoSignTimezone, 8*60*60)
	}
	return &QMXAutoSignService{db: db, client: client, xxt: xxtClient, cc: cc, loc: loc, cfgPresets: presets, qmxWebhook: qmxWebhook}
}

func (s *QMXAutoSignService) Presets() []config.QMXLocationPreset {
	return s.cfgPresets
}

func (s *QMXAutoSignService) EnsureSettings() (model.QMXAutoSignSetting, error) {
	var setting model.QMXAutoSignSetting
	err := s.db.First(&setting, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		setting = model.QMXAutoSignSetting{
			ID:       1,
			Enabled:  false,
			Timezone: qmxAutoSignTimezone,
			RunAt:    qmxAutoSignRunAt,
		}
		return setting, s.db.Create(&setting).Error
	}
	if err != nil {
		return setting, err
	}

	updates := map[string]interface{}{}
	if strings.TrimSpace(setting.Timezone) == "" {
		setting.Timezone = qmxAutoSignTimezone
		updates["timezone"] = setting.Timezone
	}
	if strings.TrimSpace(setting.RunAt) == "" {
		setting.RunAt = qmxAutoSignRunAt
		updates["run_at"] = setting.RunAt
	}
	if len(updates) > 0 {
		err = s.db.Model(&setting).Updates(updates).Error
	}
	return setting, err
}

func (s *QMXAutoSignService) UpdateSettingsEnabled(enabled bool) (model.QMXAutoSignSetting, error) {
	setting, err := s.EnsureSettings()
	if err != nil {
		return setting, err
	}
	if err := s.db.Model(&setting).Update("enabled", enabled).Error; err != nil {
		return setting, err
	}
	setting.Enabled = enabled
	return setting, nil
}

func (s *QMXAutoSignService) NextRunAt(now time.Time) time.Time {
	localNow := now.In(s.loc)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, s.loc)
	if !localNow.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s *QMXAutoSignService) StartScheduler(ctx context.Context) {
	go func() {
		for {
			next := s.NextRunAt(time.Now())
			wait := time.Until(next)
			if wait < 0 {
				wait = 0
			}
			log.Printf("QMX auto sign next run at %s", next.Format(time.RFC3339))
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				if err := s.RunScheduled(ctx); err != nil {
					log.Printf("QMX auto sign scheduled run failed: %v", err)
				}
			}
		}
	}()
}

func (s *QMXAutoSignService) RunScheduled(ctx context.Context) error {
	setting, err := s.EnsureSettings()
	if err != nil {
		return err
	}
	if !setting.Enabled {
		log.Printf("QMX auto sign skipped: global switch is disabled")
		return nil
	}

	var accounts []model.QMXAutoSignAccount
	if err := s.db.
		Where("enabled = ? AND location_name <> ? AND (location_index >= ? OR (longitude <> ? AND latitude <> ?))", true, "", 0, 0.0, 0.0).
		Order("user_uid asc").
		Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) == 0 {
		log.Printf("QMX auto sign skipped: no enabled accounts")
		return nil
	}

	runID := newQMXAutoSignRunID(QMXAutoSignTriggerScheduled)
	sem := make(chan struct{}, qmxAutoSignMaxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	savedRecords := 0
	for _, account := range accounts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(uid int64) {
			defer wg.Done()
			defer func() { <-sem }()
			delay := time.Duration(rand.Intn(300)) * time.Second
			log.Printf("QMX auto sign account %d waiting %v", uid, delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			record, err := s.runAccount(uid, QMXAutoSignTriggerScheduled, true, runID)
			if record.ID > 0 {
				mu.Lock()
				savedRecords++
				mu.Unlock()
			}
			if err != nil {
				log.Printf("QMX auto sign account %d failed: %v", uid, err)
			}
		}(account.UserUID)
	}
	wg.Wait()
	if savedRecords > 0 {
		s.notifyQMXAutoSignRun(runID)
	}
	return nil
}

func (s *QMXAutoSignService) PreviewLocations(uid int64) (qmx.Preview, error) {
	input, err := s.credentialInput(uid)
	if err != nil {
		return qmx.Preview{}, err
	}
	return s.client.Preview(input)
}

func (s *QMXAutoSignService) RunAccount(uid int64, trigger string) (model.QMXAutoSignRecord, error) {
	runID := newQMXAutoSignRunID(trigger)
	record, err := s.runAccount(uid, trigger, true, runID)
	if record.ID > 0 {
		s.notifyQMXAutoSignRun(runID)
	}
	return record, err
}

func (s *QMXAutoSignService) RunSavedAccount(uid int64, trigger string) (model.QMXAutoSignRecord, error) {
	runID := newQMXAutoSignRunID(trigger)
	record, err := s.runAccount(uid, trigger, false, runID)
	if record.ID > 0 {
		s.notifyQMXAutoSignRun(runID)
	}
	return record, err
}

func (s *QMXAutoSignService) runAccount(uid int64, trigger string, requireEnabled bool, runID string) (model.QMXAutoSignRecord, error) {
	account, err := s.configuredAccount(uid, requireEnabled)
	if err != nil {
		record, saveErr := s.saveFailureRecord(uid, trigger, runID, err.Error())
		if saveErr != nil {
			return record, saveErr
		}
		return record, err
	}

	input, err := s.credentialInput(uid)
	if err != nil {
		record, saveErr := s.saveFailureRecord(uid, trigger, runID, err.Error())
		if saveErr != nil {
			return record, saveErr
		}
		return record, err
	}
	drLng, drLat := s.driftCoordinate(account)
	result, err := s.client.Execute(qmx.ExecuteInput{
		CredentialInput:      input,
		LocationIndex:        account.LocationIndex,
		LocationName:         account.LocationName,
		Longitude:            drLng,
		Latitude:             drLat,
		RequireLocationMatch: true,
		UseProvidedLocation:  account.LocationIndex < 0,
	})
	record, saveErr := s.saveResultRecord(uid, trigger, runID, result, err)
	if saveErr != nil {
		return record, saveErr
	}
	return record, err
}

func (s *QMXAutoSignService) configuredAccount(uid int64, requireEnabled bool) (model.QMXAutoSignAccount, error) {
	var account model.QMXAutoSignAccount
	if err := s.db.Where("user_uid = ?", uid).Take(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return account, errors.New("QMX auto sign is not configured for this account")
		}
		return account, err
	}
	if requireEnabled && !account.Enabled {
		return account, errors.New("QMX auto sign is disabled for this account")
	}
	if !hasQMXAutoSignLocation(account) {
		return account, errors.New("QMX auto sign location is not configured")
	}
	return account, nil
}

func hasQMXAutoSignLocation(account model.QMXAutoSignAccount) bool {
	if strings.TrimSpace(account.LocationName) == "" {
		return false
	}
	return account.LocationIndex >= 0 || (account.Longitude != 0 && account.Latitude != 0)
}

func (s *QMXAutoSignService) credentialInput(uid int64) (qmx.CredentialInput, error) {
	var user model.User
	if err := s.db.Where("uid = ?", uid).Take(&user).Error; err != nil {
		return qmx.CredentialInput{}, err
	}
	password, err := s.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		return qmx.CredentialInput{}, errors.New("credential expired, please add account again")
	}
	cookie, err := s.xxt.CookieHeader(user.Mobile, password, "https://sw.qmx.chaoxing.com/")
	if err != nil {
		return qmx.CredentialInput{}, err
	}
	return qmx.CredentialInput{Cookie: cookie}, nil
}

func (s *QMXAutoSignService) saveFailureRecord(uid int64, trigger, runID, message string) (model.QMXAutoSignRecord, error) {
	return s.saveResultRecord(uid, trigger, runID, qmx.ExecuteResult{Message: message}, errors.New(message))
}

func (s *QMXAutoSignService) saveResultRecord(uid int64, trigger, runID string, result qmx.ExecuteResult, runErr error) (model.QMXAutoSignRecord, error) {
	message := strings.TrimSpace(result.Message)
	if runErr != nil {
		message = runErr.Error()
	}
	if len(result.Unsupported) > 0 {
		message = "unsupported QMX room check requirements: " + strings.Join(result.Unsupported, ", ")
	}
	if message == "" {
		if result.Success {
			message = "success"
		} else {
			message = "QMX returned unsuccessful response"
		}
	}

	record := model.QMXAutoSignRecord{
		RunID:        strings.TrimSpace(runID),
		UserUID:      uid,
		Trigger:      normalizeQMXAutoSignTrigger(trigger),
		Success:      runErr == nil && result.Success,
		Code:         stringifyQMXCode(result.Code),
		Message:      truncateForDB(message, 512),
		BatchName:    result.BatchName,
		CheckDate:    result.CheckDate,
		CheckTime:    result.CheckTime,
		LocationName: result.LocationName,
		Longitude:    result.Longitude,
		Latitude:     result.Latitude,
		ExecutedAt:   time.Now(),
	}
	return record, s.db.Create(&record).Error
}

func newQMXAutoSignRunID(trigger string) string {
	var randomBytes [6]byte
	if _, err := crand.Read(randomBytes[:]); err == nil {
		return fmt.Sprintf("%s-%d-%s", normalizeQMXAutoSignTrigger(trigger), time.Now().UnixMilli(), hex.EncodeToString(randomBytes[:]))
	}
	return fmt.Sprintf("%s-%d", normalizeQMXAutoSignTrigger(trigger), time.Now().UnixNano())
}

func (s *QMXAutoSignService) notifyQMXAutoSignRun(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" || !s.qmxWebhook.Enabled() {
		return
	}

	go func() {
		content, err := s.qmxAutoSignRunMarkdown(runID)
		if err != nil {
			log.Printf("QMX auto sign webhook summary failed: %v", err)
			return
		}
		if strings.TrimSpace(content) == "" {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), enterpriseWechatWebhookTimeout)
		defer cancel()
		if err := s.qmxWebhook.SendMarkdown(ctx, content); err != nil {
			log.Printf("QMX auto sign webhook failed: %v", err)
		}
	}()
}

func (s *QMXAutoSignService) qmxAutoSignRunMarkdown(runID string) (string, error) {
	var records []model.QMXAutoSignRecord
	if err := s.db.Where("run_id = ?", runID).Order("executed_at asc, id asc").Find(&records).Error; err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}

	users, err := s.qmxAutoSignUserMap(records)
	if err != nil {
		return "", err
	}

	trigger := records[0].Trigger
	successCount := 0
	failureLines := []string{}
	accountNames := make([]string, 0, len(records))
	successNames := []string{}
	failureNames := []string{}
	batchNames := []string{}
	locationNames := []string{}
	for _, record := range records {
		name := qmxAutoSignRecordName(record, users)
		accountNames = append(accountNames, name)
		batchNames = append(batchNames, record.BatchName)
		locationNames = append(locationNames, record.LocationName)
		if record.Success {
			successCount++
			successNames = append(successNames, name)
			continue
		}
		failureNames = append(failureNames, name)
		failureLines = append(failureLines, fmt.Sprintf("%s：%s", name, webhookText(record.Message, "失败")))
	}
	failureCount := len(records) - successCount
	status := "成功"
	if failureCount > 0 && successCount > 0 {
		status = "部分失败"
	} else if failureCount > 0 {
		status = "失败"
	}

	lines := []string{
		fmt.Sprintf("### QMX 自动签到%s", status),
		fmt.Sprintf(">触发：%s", qmxAutoSignTriggerLabel(trigger)),
		fmt.Sprintf(">结果：成功 %d / 失败 %d / 总计 %d", successCount, failureCount, len(records)),
		fmt.Sprintf(">账号：%s", joinWebhookValues(accountNames, 12)),
		fmt.Sprintf(">时间：%s", records[len(records)-1].ExecutedAt.Format("2006-01-02 15:04:05")),
		fmt.Sprintf(">Run ID：%s", runID),
	}
	if joined := joinWebhookValues(batchNames, 6); joined != "" {
		lines = append(lines, fmt.Sprintf(">批次：%s", joined))
	}
	if joined := joinWebhookValues(locationNames, 6); joined != "" {
		lines = append(lines, fmt.Sprintf(">地点：%s", joined))
	}
	if successCount > 0 {
		lines = append(lines, fmt.Sprintf(">成功账号：%s", joinWebhookValues(successNames, 12)))
	}
	if failureCount > 0 {
		lines = append(lines, fmt.Sprintf(">失败账号：%s", joinWebhookValues(failureNames, 12)))
		lines = append(lines, fmt.Sprintf(">失败摘要：%s", joinWebhookValues(failureLines, 6)))
	}
	return strings.Join(lines, "\n"), nil
}

func (s *QMXAutoSignService) qmxAutoSignUserMap(records []model.QMXAutoSignRecord) (map[int64]model.User, error) {
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
	if err := s.db.Where("uid IN ?", uids).Find(&users).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]model.User, len(users))
	for _, user := range users {
		out[user.UID] = user
	}
	return out, nil
}

func qmxAutoSignRecordName(record model.QMXAutoSignRecord, users map[int64]model.User) string {
	if user, ok := users[record.UserUID]; ok {
		return webhookText(user.Name, fmt.Sprintf("UID %d", record.UserUID))
	}
	return fmt.Sprintf("UID %d", record.UserUID)
}

func qmxAutoSignTriggerLabel(trigger string) string {
	switch normalizeQMXAutoSignTrigger(trigger) {
	case QMXAutoSignTriggerScheduled:
		return "定时"
	case QMXAutoSignTriggerManual:
		return "手动"
	default:
		return trigger
	}
}

func joinWebhookValues(values []string, limit int) string {
	seen := map[string]struct{}{}
	joined := []string{}
	uniqueTotal := 0
	for _, value := range values {
		value = webhookText(value, "")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		uniqueTotal++
		if limit <= 0 || len(joined) < limit {
			joined = append(joined, value)
		}
	}
	if len(joined) == 0 {
		return ""
	}
	if limit > 0 && uniqueTotal > len(joined) {
		joined = append(joined, fmt.Sprintf("等 %d 项", uniqueTotal))
	}
	return strings.Join(joined, "、")
}

func normalizeQMXAutoSignTrigger(trigger string) string {
	switch trigger {
	case QMXAutoSignTriggerScheduled, QMXAutoSignTriggerManual:
		return trigger
	default:
		return QMXAutoSignTriggerManual
	}
}

func stringifyQMXCode(code any) string {
	if code == nil {
		return ""
	}
	return truncateForDB(fmt.Sprint(code), 128)
}

func truncateForDB(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func (s *QMXAutoSignService) driftCoordinate(account model.QMXAutoSignAccount) (float64, float64) {
	preset := s.matchPreset(account)
	if preset == nil {
		return account.Longitude, account.Latitude
	}
	driftRange := preset.Range
	if driftRange <= 0 {
		driftRange = 400
	}
	angle := rand.Float64() * 2 * math.Pi
	radius := math.Sqrt(rand.Float64()) * float64(driftRange)
	dLat := radius * math.Cos(angle) / 111320.0
	dLng := radius * math.Sin(angle) / (111320.0 * math.Cos(preset.Lat*math.Pi/180.0))
	return preset.Lng + dLng, preset.Lat + dLat
}

func (s *QMXAutoSignService) matchPreset(account model.QMXAutoSignAccount) *config.QMXLocationPreset {
	if account.LocationIndex >= 0 {
		return nil
	}
	for i := range s.cfgPresets {
		p := &s.cfgPresets[i]
		if p.Name == account.LocationName {
			return p
		}
	}
	for i := range s.cfgPresets {
		p := &s.cfgPresets[i]
		if p.Lng == account.Longitude && p.Lat == account.Latitude {
			return p
		}
	}
	return nil
}
