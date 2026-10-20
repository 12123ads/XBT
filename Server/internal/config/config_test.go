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
	if cfg.IMPollIntervalSeconds != 30 || cfg.IMPollInterval() != 30*time.Second {
		t.Fatalf("default interval: %d %v", cfg.IMPollIntervalSeconds, cfg.IMPollInterval())
	}
	if cfg.IMTokenURL != "https://a1-vip6.easemob.com/cx-dev/cxstudy/token" ||
		cfg.IMAPIBaseURL != "https://a1-vip6.easemob.com/cx-dev/cxstudy" ||
		cfg.IMMessageBaseURL != "https://a1-vip6.easecdn.com/cx-dev/cxstudy" {
		t.Fatalf("unexpected default endpoints: %+v", cfg)
	}
	if !cfg.IMEnabled() {
		t.Fatal("IM polling should default to enabled")
	}

	cfg = Config{
		IMDisabled:            true,
		IMPollIntervalSeconds: 120,
		IMTokenURL:            "https://example.test/token",
		IMAPIBaseURL:          "https://example.test/api",
		IMMessageBaseURL:      "https://example.test/messages",
	}
	cfg.applyIMDefaults()
	if cfg.IMPollInterval() != 120*time.Second || cfg.IMEnabled() {
		t.Fatalf("override not applied: %+v", cfg)
	}
	if cfg.IMTokenURL != "https://example.test/token" {
		t.Fatalf("token URL overridden: %s", cfg.IMTokenURL)
	}
}
