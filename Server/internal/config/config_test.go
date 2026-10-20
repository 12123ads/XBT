package config

import (
	"testing"
	"time"
)

func TestVikunjaEndpointIdentity(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{" HTTPS://Vikunja.Example:443/prefix/ ", "https://vikunja.example/prefix"},
		{"http://192.168.1.10:3456", "http://192.168.1.10:3456"},
		{"http://[0:0:0:0:0:0:0:1]:80/prefix", "http://[::1]/prefix"},
	} {
		got, err := NormalizeVikunjaBaseURL(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("endpoint identity %q: got %q, want %q; err=%v", tc.input, got, tc.want, err)
		}
	}
}

func TestVikunjaEndpointRejectsAmbiguousDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"vikunja.example",
		"ftp://vikunja.example",
		"https://user:password@vikunja.example",
		"https://vikunja.example/private?prefix=",
		"https://vikunja.example/#fragment",
		"https://vikunja.example:65536",
		"https://vikunja.example:",
		"https://vikunja.example/a/%2e%2e/b",
		"https://vikunja.example/a%2fb",
	} {
		if _, err := NormalizeVikunjaBaseURL(endpoint); err == nil {
			t.Errorf("accepted invalid configured endpoint %q", endpoint)
		}
	}
}

func TestIMConfigDefaultsAndOverrides(t *testing.T) {
	cfg := Config{}
	cfg.applyIMDefaults()
	if cfg.IMMinFetchInterval() != 30*time.Second || cfg.IMFetchTimeout() != 5*time.Second {
		t.Fatalf("defaults: interval=%v timeout=%v", cfg.IMMinFetchInterval(), cfg.IMFetchTimeout())
	}
	if cfg.IMMinFetchIntervalSeconds == nil || *cfg.IMMinFetchIntervalSeconds != 30 {
		t.Fatalf("default min interval pointer: %+v", cfg.IMMinFetchIntervalSeconds)
	}
	if cfg.IMTokenURL != "https://a1-vip6.easemob.com/cx-dev/cxstudy/token" ||
		cfg.IMAPIBaseURL != "https://a1-vip6.easemob.com/cx-dev/cxstudy" ||
		cfg.IMMessageBaseURL != "https://a1-vip6.easecdn.com/cx-dev/cxstudy" {
		t.Fatalf("unexpected default endpoints: %+v", cfg)
	}
	if !cfg.IMEnabled() {
		t.Fatal("IM fetch should default to enabled")
	}

	zero := 0
	cfg = Config{IMMinFetchIntervalSeconds: &zero}
	cfg.applyIMDefaults()
	if cfg.IMEnabled() || cfg.IMMinFetchInterval() != 0 {
		t.Fatalf("min interval 0 must disable fetch: %+v", cfg)
	}

	override := 120
	cfg = Config{
		IMDisabled:                true,
		IMMinFetchIntervalSeconds: &override,
		IMFetchTimeoutSeconds:     9,
		IMTokenURL:                "https://example.test/token",
		IMAPIBaseURL:              "https://example.test/api",
		IMMessageBaseURL:          "https://example.test/messages",
	}
	cfg.applyIMDefaults()
	if cfg.IMMinFetchInterval() != 120*time.Second || cfg.IMFetchTimeout() != 9*time.Second || cfg.IMEnabled() {
		t.Fatalf("override not applied: %+v", cfg)
	}
	if cfg.IMTokenURL != "https://example.test/token" {
		t.Fatalf("token URL overridden: %s", cfg.IMTokenURL)
	}
}

func TestSignLimiterDefaultsAndOverrides(t *testing.T) {
	cfg := Config{}
	cfg.applySignDefaults()
	if cfg.SignRateLimitPerMinute != 30 || cfg.SignRateLimitBurst != 10 || cfg.SignMaxConcurrent != 2 {
		t.Fatalf("defaults: %+v", cfg)
	}
	if cfg.LocationJitterMeters != 5 {
		t.Fatalf("location jitter default = %v, want 5", cfg.LocationJitterMeters)
	}

	cfg = Config{SignRateLimitPerMinute: 120, SignRateLimitBurst: 20, SignMaxConcurrent: 4, LocationJitterMeters: 12}
	cfg.applySignDefaults()
	if cfg.SignRateLimitPerMinute != 120 || cfg.SignRateLimitBurst != 20 || cfg.SignMaxConcurrent != 4 || cfg.LocationJitterMeters != 12 {
		t.Fatalf("overrides not kept: %+v", cfg)
	}

	// Zero or negative would submit the selected point verbatim; keep 5m.
	cfg = Config{LocationJitterMeters: -1}
	cfg.applySignDefaults()
	if cfg.LocationJitterMeters != 5 {
		t.Fatalf("non-positive jitter = %v, want 5", cfg.LocationJitterMeters)
	}
}

func TestFaceDiagnosticsDefaultsAndValidation(t *testing.T) {
	cfg := Config{}
	if err := cfg.applyFaceDefaults(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if cfg.FaceUploadMode != "face_temp" || cfg.FaceTransformMild {
		t.Fatalf("defaults = mild:%v mode:%q, want false/face_temp", cfg.FaceTransformMild, cfg.FaceUploadMode)
	}

	cfg = Config{FaceTransformMild: true, FaceUploadMode: " generic "}
	if err := cfg.applyFaceDefaults(); err != nil {
		t.Fatalf("generic: %v", err)
	}
	if !cfg.FaceTransformMild || cfg.FaceUploadMode != "generic" {
		t.Fatalf("overrides not kept: mild:%v mode:%q", cfg.FaceTransformMild, cfg.FaceUploadMode)
	}

	cfg = Config{FaceUploadMode: "face-temp"}
	if err := cfg.applyFaceDefaults(); err == nil {
		t.Fatal("unknown face_upload_mode must be rejected")
	}
}

func TestCaptchaModelDefaultsAndOverrides(t *testing.T) {
	cfg := Config{}
	cfg.applyCaptchaDefaults()
	if cfg.CaptchaModelPath != "./assets/captcha.ort" || !cfg.CaptchaModelIsEnabled() {
		t.Fatalf("defaults: path=%q enabled=%v", cfg.CaptchaModelPath, cfg.CaptchaModelIsEnabled())
	}

	disabled := false
	cfg = Config{CaptchaModelPath: "/models/custom.ort", CaptchaModelEnabled: &disabled}
	cfg.applyCaptchaDefaults()
	if cfg.CaptchaModelPath != "/models/custom.ort" || cfg.CaptchaModelIsEnabled() {
		t.Fatalf("overrides not kept: path=%q enabled=%v", cfg.CaptchaModelPath, cfg.CaptchaModelIsEnabled())
	}
}

func TestAppReleaseConfigDefaultsAndBaseURL(t *testing.T) {
	cfg := Config{}
	cfg.applyAppReleaseDefaults()
	if cfg.AppReleaseManifest != "./releases/releases.json" {
		t.Fatalf("default manifest=%q", cfg.AppReleaseManifest)
	}
	cfg = Config{AppReleaseManifest: "/srv/xbt/releases.json"}
	cfg.applyAppReleaseDefaults()
	if cfg.AppReleaseManifest != "/srv/xbt/releases.json" {
		t.Fatalf("override manifest=%q", cfg.AppReleaseManifest)
	}

	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{" HTTPS://XBT.Example/ ", "https://xbt.example"},
		{"https://xbt.example:443", "https://xbt.example"},
		{"https://xbt.example:8443", "https://xbt.example:8443"},
		{"https://[0:0:0:0:0:0:0:1]:443/", "https://[::1]"},
	} {
		got, err := NormalizePublicBaseURL(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("base URL %q: got %q want %q; err=%v", tc.input, got, tc.want, err)
		}
	}
	for _, input := range []string{
		"http://xbt.example",
		"xbt.example",
		"https://user:pass@xbt.example",
		"https://xbt.example/prefix",
		"https://xbt.example?x=1",
		"https://xbt.example/#frag",
		"https://xbt.example:65536",
		"https://xbt.example:",
	} {
		if _, err := NormalizePublicBaseURL(input); err == nil {
			t.Fatalf("accepted invalid public_base_url %q", input)
		}
	}
}
