package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	"xbt2/server/internal/im"
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
	AppEnv                string `yaml:"app_env"`
	HTTPAddr              string `yaml:"http_addr"`
	JWTSecret             string `yaml:"jwt_secret"`
	CredentialSecret      string `yaml:"credential_secret"`
	AllowInsecureTLS      bool   `yaml:"allow_insecure_tls"`
	ChaoxingAESKey        string `yaml:"chaoxing_aes_key"`
	ChaoxingFID           string `yaml:"chaoxing_fid"`
	ChaoxingUserAgent     string `yaml:"chaoxing_user_agent"`
	ActivityListLimit     int    `yaml:"activity_list_limit"`
	CourseSignWebhookURL  string `yaml:"course_sign_webhook_url"`
	QMXAutoSignWebhookURL string `yaml:"qmx_auto_sign_webhook_url"`
	// Sign submission guard. Non-positive values fall back to the defaults in
	// applySignDefaults; the limiter is per account, in-process.
	SignRateLimitPerMinute int `yaml:"sign_rate_limit_per_minute"`
	SignRateLimitBurst     int `yaml:"sign_rate_limit_burst"`
	SignMaxConcurrent      int `yaml:"sign_max_concurrent"`
	// Bounded offset applied around an explicitly selected point. Non-positive
	// values keep the default 5m; auto mode never uses it (XBT-S26).
	LocationJitterMeters  float64                `yaml:"location_jitter_meters"`
	PostgresDSN           string                 `yaml:"postgres_dsn"`
	VikunjaBaseURL        string                 `yaml:"vikunja_base_url"`
	QMXLocationPresets    []QMXLocationPreset    `yaml:"qmx_location_presets"`
	CourseLocationPresets []CourseLocationPreset `yaml:"course_location_presets"`

	// Face sign diagnostics (OMP-58). Both keep the default production behavior
	// unless explicitly enabled and are meant for short attribution runs only.
	// FaceTransformMild selects crop 99-100%, 0 degrees and q92 instead of the
	// default 90-99%, +/-5 degrees and q85.
	FaceTransformMild bool `yaml:"face_transform_mild"`
	// FaceUploadMode is face_temp (default) or generic. generic posts to the
	// same endpoint as UploadPanFile without uploadtype=face_temp.
	FaceUploadMode string `yaml:"face_upload_mode"`

	// Group-chat sign discovery (Easemob IM, fetched on demand while serving
	// /sign/activities). Endpoints are reverse-engineered and not a public
	// contract, so they stay configurable. A min interval of 0 disables fetch.
	IMDisabled                bool   `yaml:"im_disabled"`
	IMMinFetchIntervalSeconds *int   `yaml:"im_min_fetch_interval_seconds"`
	IMFetchTimeoutSeconds     int    `yaml:"im_fetch_timeout_seconds"`
	IMTokenURL                string `yaml:"im_token_url"`
	IMAPIBaseURL              string `yaml:"im_api_base_url"`
	IMMessageBaseURL          string `yaml:"im_message_base_url"`

	// Slider-captcha ONNX detector. The model is optional: a missing file or
	// failed inference falls back to the built-in heuristic.
	CaptchaModelPath    string `yaml:"captcha_model_path"`
	CaptchaModelEnabled *bool  `yaml:"captcha_model_enabled"`

	// In-app update manifest (JSON). The APK files live in the same
	// directory. A missing or invalid manifest disables only /api/app/*; the
	// rest of the server keeps starting and serving.
	AppReleaseManifest string `yaml:"app_release_manifest"`
	// Public HTTPS origin used to build apkUrl. Empty derives it from the
	// request Host while still forcing https.
	PublicBaseURL string `yaml:"public_base_url"`
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
	cfg.applyIMDefaults()
	cfg.applySignDefaults()
	cfg.applyCaptchaDefaults()
	cfg.applyAppReleaseDefaults()
	if err := cfg.applyFaceDefaults(); err != nil {
		panic(err)
	}
	if cfg.PublicBaseURL, err = NormalizePublicBaseURL(cfg.PublicBaseURL); err != nil {
		panic(err)
	}
	return cfg
}

// applyFaceDefaults validates the OMP-58 diagnostic switches. An unknown
// upload mode is rejected instead of silently falling back so a typo cannot be
// mistaken for a completed A/B run.
func (c *Config) applyFaceDefaults() error {
	mode := strings.TrimSpace(c.FaceUploadMode)
	switch mode {
	case "":
		mode = "face_temp"
	case "face_temp", "generic":
	default:
		return fmt.Errorf("face_upload_mode must be face_temp or generic, got %q", c.FaceUploadMode)
	}
	c.FaceUploadMode = mode
	return nil
}

// applySignDefaults keeps production safe when the new keys are absent from
// an existing config.yaml: 30 submissions/min, burst 10, at most 2 in flight
// per account, and 5m of jitter around an explicitly selected point.
func (c *Config) applySignDefaults() {
	if c.SignRateLimitPerMinute <= 0 {
		c.SignRateLimitPerMinute = 30
	}
	if c.SignRateLimitBurst <= 0 {
		c.SignRateLimitBurst = 10
	}
	if c.SignMaxConcurrent <= 0 {
		c.SignMaxConcurrent = 2
	}
	if c.LocationJitterMeters <= 0 {
		c.LocationJitterMeters = 5
	}
}

func (c *Config) applyIMDefaults() {
	if c.IMMinFetchIntervalSeconds == nil {
		seconds := 30
		c.IMMinFetchIntervalSeconds = &seconds
	}
	if c.IMFetchTimeoutSeconds <= 0 {
		c.IMFetchTimeoutSeconds = 5
	}
	if strings.TrimSpace(c.IMTokenURL) == "" {
		c.IMTokenURL = im.DefaultTokenURL
	}
	if strings.TrimSpace(c.IMAPIBaseURL) == "" {
		c.IMAPIBaseURL = im.DefaultAPIBaseURL
	}
	if strings.TrimSpace(c.IMMessageBaseURL) == "" {
		c.IMMessageBaseURL = im.DefaultMessageBaseURL
	}
}

func (c Config) IMMinFetchInterval() time.Duration {
	if c.IMMinFetchIntervalSeconds == nil {
		return 30 * time.Second
	}
	seconds := *c.IMMinFetchIntervalSeconds
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds) * time.Second
}

func (c Config) IMFetchTimeout() time.Duration {
	seconds := c.IMFetchTimeoutSeconds
	if seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}

func (c Config) IMEnabled() bool {
	return !c.IMDisabled && c.IMMinFetchInterval() > 0
}

func (c *Config) applyCaptchaDefaults() {
	if strings.TrimSpace(c.CaptchaModelPath) == "" {
		c.CaptchaModelPath = "./assets/captcha.ort"
	}
	if c.CaptchaModelEnabled == nil {
		enabled := true
		c.CaptchaModelEnabled = &enabled
	}
}

// CaptchaModelIsEnabled reports the configured detector switch. Missing YAML
// values default to enabled; a missing model file is handled as a fallback.
func (c Config) CaptchaModelIsEnabled() bool {
	return c.CaptchaModelEnabled == nil || *c.CaptchaModelEnabled
}

func (c *Config) applyAppReleaseDefaults() {
	if strings.TrimSpace(c.AppReleaseManifest) == "" {
		c.AppReleaseManifest = "./releases/releases.json"
	}
}

// NormalizePublicBaseURL validates the externally visible HTTPS origin used in
// apkUrl. Only an origin is accepted: no credentials, path, query or fragment.
// An empty value means "derive from the request Host"; http is never accepted.
func NormalizePublicBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("public_base_url must be an absolute HTTPS origin without credentials, path, query or fragment")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("public_base_url must use https")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("public_base_url must not include a path")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("invalid port in public_base_url")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port in public_base_url")
		}
		if n == 443 {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("invalid host in public_base_url")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, nil
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
