package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

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
	VikunjaBaseURL        string                 `yaml:"vikunja_base_url"`
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
	if cfg.VikunjaBaseURL, err = NormalizeVikunjaBaseURL(cfg.VikunjaBaseURL); err != nil {
		panic(err)
	}

	if cfg.ActivityListLimit <= 0 {
		cfg.ActivityListLimit = 5
	}
	if len(cfg.CourseLocationPresets) == 0 {
		cfg.CourseLocationPresets = defaultCourseLocationPresets()
	}
	return cfg
}

// NormalizeVikunjaBaseURL is shared by configured endpoints and credential bindings.
func NormalizeVikunjaBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid vikunja_base_url")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Opaque != "" {
		return "", fmt.Errorf("vikunja_base_url must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("invalid port in vikunja_base_url")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port in vikunja_base_url")
		}
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("invalid host in vikunja_base_url")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("vikunja_base_url cannot contain dot path segments")
		}
	}
	escapedPath := strings.ToLower(u.EscapedPath())
	if strings.Contains(u.Path, "\\") || strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
		return "", fmt.Errorf("vikunja_base_url cannot contain escaped path separators")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	normalized := u.String()
	if utf8.RuneCountInString(normalized) > 512 {
		return "", fmt.Errorf("vikunja_base_url exceeds 512 characters")
	}
	return normalized, nil
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
