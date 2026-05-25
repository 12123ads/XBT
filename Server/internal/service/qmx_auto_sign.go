package service

import (
	"context"
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
	db        *gorm.DB
	client    *qmx.Client
	xxt       *xxt.Client
	cc        *CredentialCrypto
	loc       *time.Location
	cfgPresets []config.QMXLocationPreset
}

func NewQMXAutoSignService(db *gorm.DB, client *qmx.Client, xxtClient *xxt.Client, cc *CredentialCrypto, presets []config.QMXLocationPreset) *QMXAutoSignService {
	loc, err := time.LoadLocation(qmxAutoSignTimezone)
	if err != nil {
	loc = time.FixedZone(qmxAutoSignTimezone, 8*60*60)
	}
	return &QMXAutoSignService{db: db, client: client, xxt: xxtClient, cc: cc, loc: loc, cfgPresets: presets}
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
		Where("enabled = ? AND location_name <> ? AND location_index >= ?", true, "", 0).
		Order("user_uid asc").
		Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) == 0 {
		log.Printf("QMX auto sign skipped: no enabled accounts")
		return nil
	}

	sem := make(chan struct{}, qmxAutoSignMaxConcurrency)
	var wg sync.WaitGroup
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
			if _, err := s.RunAccount(uid, QMXAutoSignTriggerScheduled); err != nil {
				log.Printf("QMX auto sign account %d failed: %v", uid, err)
		}
	}(account.UserUID)
	}
	wg.Wait()
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
	account, err := s.configuredAccount(uid)
	if err != nil {
		record, saveErr := s.saveFailureRecord(uid, trigger, err.Error())
		if saveErr != nil {
			return record, saveErr
		}
		return record, err
	}

	input, err := s.credentialInput(uid)
	if err != nil {
		record, saveErr := s.saveFailureRecord(uid, trigger, err.Error())
		if saveErr != nil {
			return record, saveErr
		}
		return record, err
	}
	drLng, drLat := s.driftCoordinate(account)
	result, err := s.client.Execute(qmx.ExecuteInput{
		CredentialInput:      input,
		LocationIndex:        account.LocationIndex,
		LocationName:         account.LocationName,	Longitude:            drLng,
	Latitude:             drLat,		RequireLocationMatch: true,
	})
	record, saveErr := s.saveResultRecord(uid, trigger, result, err)
	if saveErr != nil {
		return record, saveErr
	}
	return record, err
}

func (s *QMXAutoSignService) configuredAccount(uid int64) (model.QMXAutoSignAccount, error) {
	var account model.QMXAutoSignAccount
	if err := s.db.Where("user_uid = ?", uid).Take(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return account, errors.New("QMX auto sign is not configured for this account")
		}
		return account, err
	}
	if !account.Enabled {
		return account, errors.New("QMX auto sign is disabled for this account")
	}
	if !hasQMXAutoSignLocation(account) {
		return account, errors.New("QMX auto sign location is not configured")
	}
	return account, nil
}

func hasQMXAutoSignLocation(account model.QMXAutoSignAccount) bool {
	return strings.TrimSpace(account.LocationName) != "" && account.LocationIndex >= 0
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

func (s *QMXAutoSignService) saveFailureRecord(uid int64, trigger, message string) (model.QMXAutoSignRecord, error) {
	return s.saveResultRecord(uid, trigger, qmx.ExecuteResult{Message: message}, errors.New(message))
}

func (s *QMXAutoSignService) saveResultRecord(uid int64, trigger string, result qmx.ExecuteResult, runErr error) (model.QMXAutoSignRecord, error) {
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
	for i := range s.cfgPresets {
		p := &s.cfgPresets[i]
		if p.Name == account.LocationName {
			return p
	}
	}
	if account.LocationIndex >= 0 && account.LocationIndex < len(s.cfgPresets) {
		return &s.cfgPresets[account.LocationIndex]
	}
	return nil
}
