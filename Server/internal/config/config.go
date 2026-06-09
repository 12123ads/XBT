package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type QMXLocationPreset struct {
	Name  string  `yaml:"name" json:"name"`
	Lng   float64 `yaml:"lng" json:"lng"`
	Lat   float64 `yaml:"lat" json:"lat"`
	Range int     `yaml:"range" json:"range"`
}

type CourseLocationPreset struct {
	Name        string `yaml:"name" json:"name"`
	Lng         string `yaml:"lng" json:"lng"`
	Lat         string `yaml:"lat" json:"lat"`
	Description string `yaml:"description" json:"description"`
}

type Config struct {
	AppEnv                string                 `yaml:"app_env"`
	HTTPAddr              string                 `yaml:"http_addr"`
	JWTSecret             string                 `yaml:"jwt_secret"`
	CredentialSecret      string                 `yaml:"credential_secret"`
	AllowInsecureTLS      bool                   `yaml:"allow_insecure_tls"`
	ChaoxingAESKey        string                 `yaml:"chaoxing_aes_key"`
	ChaoxingUserAgent     string                 `yaml:"chaoxing_user_agent"`
	ActivityListLimit     int                    `yaml:"activity_list_limit"`
	CourseSignWebhookURL  string                 `yaml:"course_sign_webhook_url"`
	QMXAutoSignWebhookURL string                 `yaml:"qmx_auto_sign_webhook_url"`
	PostgresDSN           string                 `yaml:"postgres_dsn"`
	QMXLocationPresets    []QMXLocationPreset    `yaml:"qmx_location_presets"`
	CourseLocationPresets []CourseLocationPreset `yaml:"course_location_presets"`
}

func Load() Config {
	cfg := Config{}

	raw, err := os.ReadFile("config.yaml")
	if err != nil {
		panic(fmt.Errorf("read config.yaml failed: %w", err))
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		panic(fmt.Errorf("parse config.yaml failed: %w", err))
	}

	if cfg.ActivityListLimit <= 0 {
		cfg.ActivityListLimit = 5
	}
	if len(cfg.CourseLocationPresets) == 0 {
		cfg.CourseLocationPresets = defaultCourseLocationPresets()
	}
	return cfg
}

func (c Config) MaskedDSN() string {
	return fmt.Sprintf("%s ...", c.PostgresDSN[:min(len(c.PostgresDSN), 24)])
}

func defaultCourseLocationPresets() []CourseLocationPreset {
	return []CourseLocationPreset{
		{
			Name:        "七号教学楼",
			Lng:         "119.535984",
			Lat:         "35.475740",
			Description: "中国山东省日照市东港区秦楼街道",
		},
		{
			Name:        "扩展训练基地",
			Lng:         "119.528179",
			Lat:         "35.460785",
			Description: "中国山东省日照市东港区秦楼街道山海路",
		},
		{
			Name:        "体育馆",
			Lng:         "119.521267",
			Lat:         "35.462135",
			Description: "中国山东省日照市东港区秦楼街道",
		},
	}
}
