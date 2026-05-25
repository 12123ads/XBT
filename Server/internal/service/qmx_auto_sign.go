package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
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

type QMXLocationPreset struct {
	Name  string  `json:"name"`
	Lng   float64 `json:"lng"`
	Lat   float64 `json:"lat"`
	Range int     `json:"range"`
}

var QMXLocationPresets = []QMXLocationPreset{
	{Name: "山东外国语职业技术大学-一号学生宿舍楼", Lng: 119.54337851097671, Lat: 35.479155717966535, Range: 400},
	{Name: "山东外国语职业技术大学-二号学生公寓", Lng: 119.54321331259341, Lat: 35.478656259286545, Range: 400},
	{Name: "山东外国语职业技术大学-三号学生公寓", Lng: 119.54327843974289, Lat: 35.47820316518164, Range: 400},
	{Name: "山东外国语职业技术大学-四号学生公寓", Lng: 119.53942740402162, Lat: 35.47907452032158, Range: 400},
	{Name: "山东外国语职业技术大学-11号公寓楼", Lng: 119.53786605921472, Lat: 35.48033494828193, Range: 400},
	{Name: "山东外国语职业技术大学-17号公寓楼", Lng: 119.53774784220961, Lat: 35.47950174392571, Range: 400},
	{Name: "山东外国语职业技术大学-18号学生公寓楼", Lng: 119.53702255034085, Lat: 35.47997716894462, Range: 400},
	{Name: "山东外国语职业技术大学-22号公寓楼", Lng: 119.53683022313116, Lat: 35.47764535474211, Range: 400},
	{Name: "山东外国语职业技术大学25号宿舍楼", Lng: 119.53730659454311, Lat: 35.47780635668077, Range: 400},
	{Name: "山东外国语职业技术大学-26号公寓楼", Lng: 119.53419028289845, Lat: 35.47743702906706, Range: 400},
	{Name: "山东外国语职业技术大学-27号公寓楼", Lng: 119.53434595924332, Lat: 35.477928118314274, Range: 400},
	{Name: "山东外国语职业技术大学-28号公寓楼", Lng: 119.53519216303371, Lat: 35.47825754248891, Range: 400},
	{Name: "山东外国语职业技术大学-29号公寓楼", Lng: 119.53465668311922, Lat: 35.47837041202444, Range: 400},
	{Name: "学生公寓楼-9号楼", Lng: 119.53851131206254, Lat: 35.47978516224217, Range: 500},
	{Name: "山东外国语职业技术大学24栋", Lng: 119.5362745313427, Lat: 35.47778644272059, Range: 500},
	{Name: "山东外国语职业技术大学19栋", Lng: 119.53692131130988, Lat: 35.47966024374107, Range: 500},
	{Name: "山东外国语职业技术大学23栋", Lng: 119.5363194466182, Lat: 35.47818325132064, Range: 500},
	{Name: "山东外国语职业技术大学学生公寓10号楼", Lng: 119.5403079230825, Lat: 35.47966024374107, Range: 500},
	{Name: "山东外国语职业学院9号学生公寓", Lng: 119.53851131206254, Lat: 35.479799858523556, Range: 500},
	{Name: "山东外国语职业学院学生公寓楼-8栋", Lng: 119.53837656623604, Lat: 35.47931487980695, Range: 500},
	{Name: "学生公寓楼-16号楼", Lng: 119.53770283710355, Lat: 35.48004234677476, Range: 500},
	{Name: "山外21号宿舍楼", Lng: 119.53684944686908, Lat: 35.478080375206694, Range: 500},
	{Name: "山东外国语职业技术大学20栋", Lng: 119.53687522823721, Lat: 35.47914829628478, Range: 500},
	{Name: "山东外国语职业技术大学-七号学生公寓楼", Lng: 119.53823795769586, Lat: 35.47883592368604, Range: 500},
	{Name: "山东外国语职业技术大学-二号学生公寓", Lng: 119.54320944987973, Lat: 35.478653540435666, Range: 500},
	{Name: "山东外国语职业技术大学-30号公寓楼", Lng: 119.53524749865313, Lat: 35.47847556572519, Range: 500},
}

type QMXAutoSignService struct {
	db     *gorm.DB
	client *qmx.Client
	xxt    *xxt.Client
	cc     *CredentialCrypto
	loc    *time.Location
}

func NewQMXAutoSignService(db *gorm.DB, client *qmx.Client, xxtClient *xxt.Client, cc *CredentialCrypto) *QMXAutoSignService {
	loc, err := time.LoadLocation(qmxAutoSignTimezone)
	if err != nil {
		loc = time.FixedZone(qmxAutoSignTimezone, 8*60*60)
	}
	return &QMXAutoSignService{db: db, client: client, xxt: xxtClient, cc: cc, loc: loc}
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

	result, err := s.client.Execute(qmx.ExecuteInput{
		CredentialInput:      input,
		LocationIndex:        account.LocationIndex,
		LocationName:         account.LocationName,
		RequireLocationMatch: true,
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
