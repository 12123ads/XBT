package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeReleaseFixture(t *testing.T, content []byte, mutate func(*Manifest, string)) (*Catalog, string) {
	t.Helper()
	dir := t.TempDir()
	apkName := "xbt-2.0.2.apk"
	apkPath := filepath.Join(dir, apkName)
	if err := os.WriteFile(apkPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Package:                 "com.github.enderwolf006.xbt",
		ABI:                     "arm64-v8a",
		MinSupportedVersionCode: 3,
		Latest: Latest{
			VersionCode: 4,
			VersionName: "2.0.2",
			APKFile:     apkName,
			Changelog:   "fix things",
			PublishedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		},
	}
	if mutate != nil {
		mutate(&manifest, dir)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "releases.json")
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, apkPath
}

func TestLoadComputesVerifiedAPKMetadata(t *testing.T) {
	content := []byte(strings.Repeat("apk-bytes-", 32))
	catalog, apkPath := writeReleaseFixture(t, content, nil)

	wantSum := sha256.Sum256(content)
	wantSHA := hex.EncodeToString(wantSum[:])
	if catalog.SHA256 != wantSHA {
		t.Fatalf("sha256=%s, want %s", catalog.SHA256, wantSHA)
	}
	if catalog.Manifest.Latest.SHA256 != wantSHA {
		t.Fatalf("manifest sha256=%s, want computed %s", catalog.Manifest.Latest.SHA256, wantSHA)
	}
	if catalog.SizeBytes != int64(len(content)) || catalog.Manifest.Latest.SizeBytes != int64(len(content)) {
		t.Fatalf("size=%d manifest size=%d, want %d", catalog.SizeBytes, catalog.Manifest.Latest.SizeBytes, len(content))
	}
	if catalog.APKPath != apkPath {
		t.Fatalf("apk path=%s, want %s", catalog.APKPath, apkPath)
	}
	if catalog.ModTime.IsZero() {
		t.Fatal("mod time must be recorded")
	}
	if !catalog.Matches("com.github.enderwolf006.xbt", "arm64-v8a") {
		t.Fatal("catalog should match its package and abi")
	}
	if catalog.Matches("com.example.other", "arm64-v8a") || catalog.Matches("com.github.enderwolf006.xbt", "x86_64") {
		t.Fatal("catalog must not match another package or abi")
	}
}

func TestLoadAcceptsManifestHashAndSizeWhenTheyMatch(t *testing.T) {
	content := []byte("verified-apk")
	sum := sha256.Sum256(content)
	catalog, _ := writeReleaseFixture(t, content, func(m *Manifest, _ string) {
		m.Latest.SHA256 = hex.EncodeToString(sum[:])
		m.Latest.SizeBytes = int64(len(content))
	})
	if catalog.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected sha: %s", catalog.SHA256)
	}
}

func TestLoadRejectsInvalidManifests(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Manifest, string)
		wantErr string
	}{
		{"empty package", func(m *Manifest, _ string) { m.Package = " " }, "package is empty"},
		{"empty abi", func(m *Manifest, _ string) { m.ABI = "" }, "abi is empty"},
		{"zero version code", func(m *Manifest, _ string) { m.Latest.VersionCode = 0 }, "versionCode must be positive"},
		{"empty version name", func(m *Manifest, _ string) { m.Latest.VersionName = " " }, "versionName is empty"},
		{"negative minimum", func(m *Manifest, _ string) { m.MinSupportedVersionCode = -1 }, "must not be negative"},
		{"minimum above latest", func(m *Manifest, _ string) { m.MinSupportedVersionCode = 5 }, "exceeds latest"},
		{"traversing apk name", func(m *Manifest, _ string) { m.Latest.APKFile = "../xbt.apk" }, "safe .apk basename"},
		{"non-apk name", func(m *Manifest, _ string) { m.Latest.APKFile = "xbt.apk.exe" }, "safe .apk basename"},
		{"missing publishedAt", func(m *Manifest, _ string) { m.Latest.PublishedAt = time.Time{} }, "publishedAt is required"},
		{"sha mismatch", func(m *Manifest, _ string) { m.Latest.SHA256 = strings.Repeat("0", 64) }, "sha256 mismatch"},
		{"size mismatch", func(m *Manifest, _ string) { m.Latest.SizeBytes = 1 }, "size mismatch"},
		{"missing apk", func(_ *Manifest, dir string) { os.Remove(filepath.Join(dir, "xbt-2.0.2.apk")) }, "stat release apk"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			apkPath := filepath.Join(dir, "xbt-2.0.2.apk")
			if err := os.WriteFile(apkPath, []byte("apk"), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest := Manifest{
				Package:                 "com.github.enderwolf006.xbt",
				ABI:                     "arm64-v8a",
				MinSupportedVersionCode: 3,
				Latest: Latest{
					VersionCode: 4,
					VersionName: "2.0.2",
					APKFile:     "xbt-2.0.2.apk",
					PublishedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
				},
			}
			tc.mutate(&manifest, dir)
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(dir, "releases.json")
			if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(manifestPath); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load error=%v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMissingManifestPath(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read release manifest") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := Load("  "); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "releases.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "parse release manifest") {
		t.Fatalf("unexpected error: %v", err)
	}
}
