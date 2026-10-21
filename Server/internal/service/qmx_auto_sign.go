package service

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
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
	"gorm.io/gorm/clause"
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
	qmxAutoSignJitterSeconds  = 300
	qmxJitterDateLayout       = "2006-01-02"

	qmxManualWindowStartMinute = 22 * 60
	qmxManualWindowEndMinute   = qmxManualWindowStartMinute + 30
)

// ErrQMXManualWindowClosed rejects manual dorm check-in outside 22:00–22:30 Asia/Shanghai.
var ErrQMXManualWindowClosed = errors.New("当前不在签到时间（22:00–22:30）")

type qmxAutoSignClient interface {
	Preview(input qmx.CredentialInput) (qmx.Preview, error)
	Execute(input qmx.ExecuteInput) (qmx.ExecuteResult, error)
}

type qmxCookieClient interface {
	CookieHeader(mobile, password, rawURL string) (string, error)
}

type QMXAutoSignService struct {
	db              *gorm.DB
	client          qmxAutoSignClient
	xxt             qmxCookieClient
	cc              *CredentialCrypto
	loc             *time.Location
	cfgPresets      []config.QMXLocationPreset
	presetsProvider func() []config.QMXLocationPreset
	qmxWebhook      *EnterpriseWechatWebhookNotifier
	now             func() time.Time
	jitter          func(uid int64, runDate string) int
}

func NewQMXAutoSignService(db *gorm.DB, client *qmx.Client, xxtClient *xxt.Client, cc *CredentialCrypto, presets []config.QMXLocationPreset, qmxWebhook *EnterpriseWechatWebhookNotifier) *QMXAutoSignService {
	loc, err := time.LoadLocation(qmxAutoSignTimezone)
	if err != nil {
		loc = time.FixedZone(qmxAutoSignTimezone, 8*60*60)
	}
	return &QMXAutoSignService{db: db, client: client, xxt: xxtClient, cc: cc, loc: loc, cfgPresets: presets, qmxWebhook: qmxWebhook, now: time.Now, jitter: jitterSeconds}
}

// SetNowFunc replaces the service clock; nil restores time.Now.
func (s *QMXAutoSignService) SetNowFunc(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.now = now
}

// Now returns the instant the service considers current for scheduling decisions.
func (s *QMXAutoSignService) Now() time.Time { return s.now() }

// SetJitterFunc replaces the per-account deterministic delay function; nil restores jitterSeconds.
func (s *QMXAutoSignService) SetJitterFunc(jitter func(uid int64, runDate string) int) {
	if jitter == nil {
		jitter = jitterSeconds
	}
	s.jitter = jitter
}

func (s *QMXAutoSignService) Presets() []config.QMXLocationPreset {
	if s.presetsProvider != nil {
		return s.presetsProvider()
	}
	return s.cfgPresets
}

func (s *QMXAutoSignService) SetPresetsProvider(provider func() []config.QMXLocationPreset) {
	s.presetsProvider = provider
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

// jitterSeconds derives a stable per-account delay in [0, 300) seconds for one run date.
func jitterSeconds(uid int64, runDate string) int {
	sum := sha256.Sum256([]byte(fmt.Sprintf("qmx-jitter:v1:%d:%s", uid, runDate)))
	return int(binary.BigEndian.Uint32(sum[:4]) % qmxAutoSignJitterSeconds)
}

// NextRunAt is the batch base time: today's 22:00, or tomorrow's once 22:00 has passed.
func (s *QMXAutoSignService) NextRunAt(now time.Time) time.Time {
	localNow := now.In(s.loc)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, s.loc)
	if !localNow.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// NextRunAtFor is the instant this account actually runs: the batch base plus its deterministic jitter.
func (s *QMXAutoSignService) NextRunAtFor(uid int64, now time.Time) time.Time {
	localNow := now.In(s.loc)
	base := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, s.loc)
	if at := s.scheduledRunAt(uid, base); at.After(localNow) {
		return at
	}
	return s.scheduledRunAt(uid, base.AddDate(0, 0, 1))
}

// NextRunWindow returns the earliest and latest actual execution instants of the
// next batch date among enabled accounts. With no enabled accounts both values
// are the batch base time.
func (s *QMXAutoSignService) NextRunWindow(now time.Time) (time.Time, time.Time, error) {
	var uids []int64
	if err := qmxAutoSignEnabledAccounts(s.db).Order("user_uid asc").Pluck("user_uid", &uids).Error; err != nil {
		return time.Time{}, time.Time{}, err
	}
	base := s.NextRunAt(now)
	if len(uids) == 0 {
		return base, base, nil
	}
	earliest := s.NextRunAtFor(uids[0], now)
	for _, uid := range uids[1:] {
		if at := s.NextRunAtFor(uid, now); at.Before(earliest) {
			earliest = at
		}
	}
	latest := earliest
	year, month, day := earliest.In(s.loc).Date()
	for _, uid := range uids {
		at := s.NextRunAtFor(uid, now)
		y, m, d := at.In(s.loc).Date()
		if y == year && m == month && d == day && at.After(latest) {
			latest = at
		}
	}
	return earliest, latest, nil
}

// scheduledRunAt applies the account's deterministic jitter to one batch base time.
func (s *QMXAutoSignService) scheduledRunAt(uid int64, batchAt time.Time) time.Time {
	jitter := s.jitter
	if jitter == nil {
		jitter = jitterSeconds
	}
	runDate := batchAt.In(s.loc).Format(qmxJitterDateLayout)
	return batchAt.Add(time.Duration(jitter(uid, runDate)) * time.Second)
}

func qmxAutoSignEnabledAccounts(db *gorm.DB) *gorm.DB {
	return db.Model(&model.QMXAutoSignAccount{}).
		Where("enabled = ? AND location_name <> ? AND (location_index >= ? OR (longitude <> ? AND latitude <> ?))", true, "", 0, 0.0, 0.0)
}

func (s *QMXAutoSignService) StartScheduler(ctx context.Context) {
	go func() {
		for {
			now := s.now()
			next := s.NextRunAt(now)
			wait := next.Sub(now)
			if wait < 0 {
				wait = 0
			}
			log.Printf("dorm check-in next run at %s", next.Format(time.RFC3339))
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				if err := s.runScheduled(ctx, next); err != nil {
					log.Printf("dorm check-in scheduled run failed: %v", err)
				}
			}
		}
	}()
}

// runScheduled runs the batch whose base time is batchAt; every enabled account
// waits until its deterministic execution instant before signing in.
func (s *QMXAutoSignService) runScheduled(ctx context.Context, batchAt time.Time) error {
	setting, err := s.EnsureSettings()
	if err != nil {
		return err
	}
	if !setting.Enabled {
		log.Printf("dorm check-in skipped: global switch is disabled")
		return nil
	}

	var accounts []model.QMXAutoSignAccount
	if err := qmxAutoSignEnabledAccounts(s.db).Order("user_uid asc").Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) == 0 {
		log.Printf("dorm check-in skipped: no enabled accounts")
		return nil
	}

	runID := s.scheduledQMXAutoSignRunID(batchAt)
	sem := make(chan struct{}, qmxAutoSignMaxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	savedRecords := 0
	for _, account := range accounts {
		wg.Add(1)
		go func(uid int64) {
			defer wg.Done()
			scheduledAt := s.scheduledRunAt(uid, batchAt)
			log.Printf("dorm check-in account %d scheduled at %s", uid, scheduledAt.Format(time.RFC3339))
			delay := scheduledAt.Sub(s.now())
			if delay < 0 {
				delay = 0
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			exists, err := s.qmxAutoSignRecordExists(runID, uid)
			if err != nil {
				log.Printf("dorm check-in account %d skipped: query scheduled record failed: %v", uid, err)
				return
			}
			if exists {
				log.Printf("dorm check-in account %d skipped: scheduled record already exists for %s", uid, runID)
				return
			}
			record, err := s.runAccount(uid, QMXAutoSignTriggerScheduled, true, runID)
			if record.ID > 0 {
				mu.Lock()
				savedRecords++
				mu.Unlock()
			}
			if err != nil {
				log.Printf("dorm check-in account %d failed: %v", uid, err)
			}
		}(account.UserUID)
	}
	wg.Wait()
	if savedRecords == 0 {
		log.Printf("dorm check-in scheduled run %s saved no new records", runID)
	}
	s.notifyQMXAutoSignScheduledRun(runID)
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

// manualWindowOpen reports whether now falls in [22:00:00, 22:30:00) Asia/Shanghai.
func (s *QMXAutoSignService) manualWindowOpen(now time.Time) bool {
	local := now.In(s.loc)
	minute := local.Hour()*60 + local.Minute()
	return minute >= qmxManualWindowStartMinute && minute < qmxManualWindowEndMinute
}

// manualWindowError blocks manual runs outside the sign-in window before any
// upstream request or failure record is made.
func (s *QMXAutoSignService) manualWindowError(trigger string) error {
	if normalizeQMXAutoSignTrigger(trigger) != QMXAutoSignTriggerManual {
		return nil
	}
	if !s.manualWindowOpen(s.now()) {
		return ErrQMXManualWindowClosed
	}
	return nil
}

func (s *QMXAutoSignService) runAccount(uid int64, trigger string, requireEnabled bool, runID string) (model.QMXAutoSignRecord, error) {
	if err := s.manualWindowError(trigger); err != nil {
		return model.QMXAutoSignRecord{}, err
	}
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
			return account, errors.New("该账号未配置宿舍在寝签到")
		}
		return account, err
	}
	if requireEnabled && !account.Enabled {
		return account, errors.New("该账号未开启宿舍在寝签到")
	}
	if !hasQMXAutoSignLocation(account) {
		return account, errors.New("未配置宿舍在寝签到定位点")
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
	user, err := LoadActiveUser(s.db, uid)
	if err != nil {
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
		message = "宿舍在寝签到要求暂不支持: " + strings.Join(result.Unsupported, ", ")
	}
	if message == "" {
		if result.Success {
			message = "success"
		} else {
			message = "宿舍在寝签到返回未成功"
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

func (s *QMXAutoSignService) scheduledQMXAutoSignRunID(now time.Time) string {
	return QMXAutoSignTriggerScheduled + "-" + now.In(s.loc).Format("20060102")
}

func newQMXAutoSignRunID(trigger string) string {
	var randomBytes [6]byte
	if _, err := crand.Read(randomBytes[:]); err == nil {
		return fmt.Sprintf("%s-%d-%s", normalizeQMXAutoSignTrigger(trigger), time.Now().UnixMilli(), hex.EncodeToString(randomBytes[:]))
	}
	return fmt.Sprintf("%s-%d", normalizeQMXAutoSignTrigger(trigger), time.Now().UnixNano())
}

func (s *QMXAutoSignService) qmxAutoSignRecordExists(runID string, uid int64) (bool, error) {
	var count int64
	if err := s.db.Model(&model.QMXAutoSignRecord{}).
		Where("run_id = ? AND user_uid = ?", strings.TrimSpace(runID), uid).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *QMXAutoSignService) notifyQMXAutoSignScheduledRun(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" || !s.qmxWebhook.Enabled() {
		return
	}

	go func() {
		notified, err := s.qmxAutoSignRunAlreadyNotified(runID)
		if err != nil {
			log.Printf("dorm check-in webhook state failed: %v", err)
			return
		}
		if notified {
			log.Printf("dorm check-in webhook skipped: %s has already been notified", runID)
			return
		}

		content, err := s.qmxAutoSignRunMarkdown(runID)
		if err != nil {
			log.Printf("dorm check-in webhook summary failed: %v", err)
			return
		}
		if strings.TrimSpace(content) == "" {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), enterpriseWechatWebhookTimeout)
		defer cancel()
		if err := s.qmxWebhook.SendMarkdown(ctx, content); err != nil {
			log.Printf("dorm check-in webhook failed: %v", err)
			return
		}
		if err := s.markQMXAutoSignRunNotified(runID, QMXAutoSignTriggerScheduled); err != nil {
			log.Printf("dorm check-in webhook state save failed: %v", err)
		}
	}()
}

func (s *QMXAutoSignService) notifyQMXAutoSignRun(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" || !s.qmxWebhook.Enabled() {
		return
	}

	go func() {
		content, err := s.qmxAutoSignRunMarkdown(runID)
		if err != nil {
			log.Printf("dorm check-in webhook summary failed: %v", err)
			return
		}
		if strings.TrimSpace(content) == "" {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), enterpriseWechatWebhookTimeout)
		defer cancel()
		if err := s.qmxWebhook.SendMarkdown(ctx, content); err != nil {
			log.Printf("dorm check-in webhook failed: %v", err)
		}
	}()
}

func (s *QMXAutoSignService) qmxAutoSignRunAlreadyNotified(runID string) (bool, error) {
	var state model.QMXAutoSignRunState
	err := s.db.Where("run_id = ?", strings.TrimSpace(runID)).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.NotifiedAt != nil, nil
}

func (s *QMXAutoSignService) markQMXAutoSignRunNotified(runID, trigger string) error {
	now := time.Now()
	normalizedTrigger := normalizeQMXAutoSignTrigger(trigger)
	state := model.QMXAutoSignRunState{
		RunID:      strings.TrimSpace(runID),
		Trigger:    normalizedTrigger,
		NotifiedAt: &now,
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "run_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"trigger":     normalizedTrigger,
			"notified_at": &now,
		}),
	}).Create(&state).Error
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
		qmxAutoSignRunTitle(runID, trigger, status),
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

func qmxAutoSignRunTitle(runID, trigger, status string) string {
	if normalizeQMXAutoSignTrigger(trigger) == QMXAutoSignTriggerScheduled {
		rawDate := strings.TrimPrefix(strings.TrimSpace(runID), QMXAutoSignTriggerScheduled+"-")
		if t, err := time.Parse("20060102", rawDate); err == nil {
			return fmt.Sprintf("### 宿舍在寝签到 %s（%s）", t.Format("2006-01-02"), status)
		}
	}
	return fmt.Sprintf("### 宿舍在寝签到%s", status)
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
	presets := s.Presets()
	for i := range presets {
		p := &presets[i]
		if p.Name == account.LocationName {
			return p
		}
	}
	for i := range presets {
		p := &presets[i]
		if p.Lng == account.Longitude && p.Lat == account.Latitude {
			return p
		}
	}
	return nil
}
