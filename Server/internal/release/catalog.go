// Package release loads the on-disk app release manifest used by the public
// update endpoint. The manifest is read once at startup; publishing a new APK
// requires a server restart.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// apkFilePattern keeps manifest file names to a single, non-traversing APK
// basename. It is also what makes the download route safe to join with the
// manifest directory.
var apkFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.apk$`)

// Manifest is the JSON document stored next to the APK files.
//
// Example:
//
//	{
//	  "package": "com.github.enderwolf006.xbt",
//	  "abi": "arm64-v8a",
//	  "minSupportedVersionCode": 3,
//	  "latest": {
//	    "versionCode": 4,
//	    "versionName": "2.0.2",
//	    "apkFile": "xbt-2.0.2.apk",
//	    "changelog": "...",
//	    "publishedAt": "2026-10-21T00:00:00Z"
//	  }
//	}
//
// sha256 and sizeBytes are optional. When present they are verified against
// the APK; the values exposed by the API always come from the file itself.
type Manifest struct {
	Package                 string `json:"package"`
	ABI                     string `json:"abi"`
	MinSupportedVersionCode int64  `json:"minSupportedVersionCode"`
	Latest                  Latest `json:"latest"`
}

// Latest describes the newest published APK.
type Latest struct {
	VersionCode int64     `json:"versionCode"`
	VersionName string    `json:"versionName"`
	APKFile     string    `json:"apkFile"`
	SHA256      string    `json:"sha256,omitempty"`
	SizeBytes   int64     `json:"sizeBytes,omitempty"`
	Changelog   string    `json:"changelog"`
	PublishedAt time.Time `json:"publishedAt"`
}

// Catalog is a fully validated manifest plus the verified APK metadata.
type Catalog struct {
	ManifestPath string
	Manifest     Manifest
	APKPath      string
	// SHA256 and SizeBytes are computed from the APK file at load time.
	SHA256    string
	SizeBytes int64
	ModTime   time.Time
}

// Load reads and validates a release manifest and the APK it points to.
// Every failure is returned instead of panicking so the caller can keep the
// rest of the API available and serve 503 on the update routes.
func Load(manifestPath string) (*Catalog, error) {
	manifestPath = strings.TrimSpace(manifestPath)
	if manifestPath == "" {
		return nil, errors.New("release manifest path is empty")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read release manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse release manifest: %w", err)
	}
	manifest.Package = strings.TrimSpace(manifest.Package)
	manifest.ABI = strings.TrimSpace(manifest.ABI)
	manifest.Latest.VersionName = strings.TrimSpace(manifest.Latest.VersionName)
	manifest.Latest.APKFile = strings.TrimSpace(manifest.Latest.APKFile)
	manifest.Latest.SHA256 = strings.ToLower(strings.TrimSpace(manifest.Latest.SHA256))

	if manifest.Package == "" {
		return nil, errors.New("release manifest package is empty")
	}
	if manifest.ABI == "" {
		return nil, errors.New("release manifest abi is empty")
	}
	if manifest.Latest.VersionCode <= 0 {
		return nil, errors.New("release manifest latest.versionCode must be positive")
	}
	if manifest.Latest.VersionName == "" {
		return nil, errors.New("release manifest latest.versionName is empty")
	}
	if manifest.MinSupportedVersionCode < 0 {
		return nil, errors.New("release manifest minSupportedVersionCode must not be negative")
	}
	if manifest.MinSupportedVersionCode > manifest.Latest.VersionCode {
		return nil, errors.New("release manifest minSupportedVersionCode exceeds latest.versionCode")
	}
	if err := validateAPKFile(manifest.Latest.APKFile); err != nil {
		return nil, err
	}
	if manifest.Latest.PublishedAt.IsZero() {
		return nil, errors.New("release manifest latest.publishedAt is required")
	}

	apkPath := filepath.Join(filepath.Dir(manifestPath), manifest.Latest.APKFile)
	info, err := os.Stat(apkPath)
	if err != nil {
		return nil, fmt.Errorf("stat release apk: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("release apk is not a regular file: %s", apkPath)
	}
	if info.Size() <= 0 {
		return nil, fmt.Errorf("release apk is empty: %s", apkPath)
	}

	sum, size, err := fileSHA256(apkPath)
	if err != nil {
		return nil, err
	}
	if manifest.Latest.SHA256 != "" && manifest.Latest.SHA256 != sum {
		return nil, fmt.Errorf("release apk sha256 mismatch: manifest=%s actual=%s", manifest.Latest.SHA256, sum)
	}
	if manifest.Latest.SizeBytes != 0 && manifest.Latest.SizeBytes != size {
		return nil, fmt.Errorf("release apk size mismatch: manifest=%d actual=%d", manifest.Latest.SizeBytes, size)
	}

	manifest.Latest.SHA256 = sum
	manifest.Latest.SizeBytes = size
	return &Catalog{
		ManifestPath: manifestPath,
		Manifest:     manifest,
		APKPath:      apkPath,
		SHA256:       sum,
		SizeBytes:    size,
		ModTime:      info.ModTime(),
	}, nil
}

// Matches reports whether this catalog serves the requested package and ABI.
func (c *Catalog) Matches(pkg, abi string) bool {
	return c != nil && c.Manifest.Package == pkg && c.Manifest.ABI == abi
}

func validateAPKFile(name string) error {
	if name == "" {
		return errors.New("release manifest latest.apkFile is empty")
	}
	if filepath.Base(name) != name || !apkFilePattern.MatchString(name) {
		return fmt.Errorf("release manifest latest.apkFile is not a safe .apk basename: %q", name)
	}
	return nil
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open release apk: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash release apk: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
