package service

import (
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/config"
	"xbt2/server/internal/model"
)

const (
	appSettingCourseSignWebhookURL  = "course_sign_webhook_url"
	appSettingQMXAutoSignWebhookURL = "qmx_auto_sign_webhook_url"
	appSettingQMXLocationPresets    = "qmx_location_presets"
)

type RuntimeSettings struct {
	CourseSignWebhookURL  string                     `json:"course_sign_webhook_url"`
	QMXAutoSignWebhookURL string                     `json:"qmx_auto_sign_webhook_url"`
	QMXLocationPresets    []config.QMXLocationPreset `json:"qmx_location_presets"`
}

type RuntimeSettingsService struct {
	db       *gorm.DB
	defaults RuntimeSettings
}

func NewRuntimeSettingsService(db *gorm.DB, cfg config.Config) *RuntimeSettingsService {
	return &RuntimeSettingsService{
		db: db,
		defaults: RuntimeSettings{
			CourseSignWebhookURL:  strings.TrimSpace(cfg.CourseSignWebhookURL),
			QMXAutoSignWebhookURL: strings.TrimSpace(cfg.QMXAutoSignWebhookURL),
			QMXLocationPresets:    copyQMXLocationPresets(cfg.QMXLocationPresets),
		},
	}
}

func (s *RuntimeSettingsService) Settings() (RuntimeSettings, error) {
	settings := RuntimeSettings{
		CourseSignWebhookURL:  s.defaults.CourseSignWebhookURL,
		QMXAutoSignWebhookURL: s.defaults.QMXAutoSignWebhookURL,
		QMXLocationPresets:    copyQMXLocationPresets(s.defaults.QMXLocationPresets),
	}

	if value, ok, err := s.settingValue(appSettingCourseSignWebhookURL); err != nil {
		return settings, err
	} else if ok {
		settings.CourseSignWebhookURL = strings.TrimSpace(value)
	}
	if value, ok, err := s.settingValue(appSettingQMXAutoSignWebhookURL); err != nil {
		return settings, err
	} else if ok {
		settings.QMXAutoSignWebhookURL = strings.TrimSpace(value)
	}
	if value, ok, err := s.settingValue(appSettingQMXLocationPresets); err != nil {
		return settings, err
	} else if ok {
		var presets []config.QMXLocationPreset
		if err := json.Unmarshal([]byte(value), &presets); err != nil {
			return settings, err
		}
		settings.QMXLocationPresets = copyQMXLocationPresets(presets)
	}
	return settings, nil
}

func (s *RuntimeSettingsService) Update(settings RuntimeSettings) (RuntimeSettings, error) {
	settings.CourseSignWebhookURL = strings.TrimSpace(settings.CourseSignWebhookURL)
	settings.QMXAutoSignWebhookURL = strings.TrimSpace(settings.QMXAutoSignWebhookURL)
	settings.QMXLocationPresets = copyQMXLocationPresets(settings.QMXLocationPresets)

	rawPresets, err := json.Marshal(settings.QMXLocationPresets)
	if err != nil {
		return settings, err
	}

	tx := s.db.Begin()
	if tx.Error != nil {
		return settings, tx.Error
	}
	if err := setRuntimeSettingValue(tx, appSettingCourseSignWebhookURL, settings.CourseSignWebhookURL); err != nil {
		tx.Rollback()
		return settings, err
	}
	if err := setRuntimeSettingValue(tx, appSettingQMXAutoSignWebhookURL, settings.QMXAutoSignWebhookURL); err != nil {
		tx.Rollback()
		return settings, err
	}
	if err := setRuntimeSettingValue(tx, appSettingQMXLocationPresets, string(rawPresets)); err != nil {
		tx.Rollback()
		return settings, err
	}
	if err := tx.Commit().Error; err != nil {
		return settings, err
	}
	return settings, nil
}

func (s *RuntimeSettingsService) CourseSignWebhookURL() string {
	settings, err := s.Settings()
	if err != nil {
		log.Printf("read course sign webhook setting failed: %v", err)
		return s.defaults.CourseSignWebhookURL
	}
	return settings.CourseSignWebhookURL
}

func (s *RuntimeSettingsService) QMXAutoSignWebhookURL() string {
	settings, err := s.Settings()
	if err != nil {
		log.Printf("read QMX auto sign webhook setting failed: %v", err)
		return s.defaults.QMXAutoSignWebhookURL
	}
	return settings.QMXAutoSignWebhookURL
}

func (s *RuntimeSettingsService) QMXLocationPresets() []config.QMXLocationPreset {
	settings, err := s.Settings()
	if err != nil {
		log.Printf("read QMX location presets setting failed: %v", err)
		return copyQMXLocationPresets(s.defaults.QMXLocationPresets)
	}
	return copyQMXLocationPresets(settings.QMXLocationPresets)
}

func (s *RuntimeSettingsService) settingValue(key string) (string, bool, error) {
	var setting model.AppSetting
	err := s.db.Where("setting_key = ?", key).Take(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return setting.Value, true, nil
}

func setRuntimeSettingValue(tx *gorm.DB, key, value string) error {
	now := time.Now()
	setting := model.AppSetting{SettingKey: key, Value: value, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "setting_key"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"value":      value,
			"updated_at": now,
		}),
	}).Create(&setting).Error
}

func copyQMXLocationPresets(in []config.QMXLocationPreset) []config.QMXLocationPreset {
	if len(in) == 0 {
		return []config.QMXLocationPreset{}
	}
	out := make([]config.QMXLocationPreset, len(in))
	copy(out, in)
	return out
}
