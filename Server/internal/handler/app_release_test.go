package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/release"
)

const (
	appReleasePackage = "com.github.enderwolf006.xbt"
	appReleaseABI     = "arm64-v8a"
	appReleaseFile    = "xbt-2.0.2.apk"
)

func appReleaseFixture(t *testing.T) (*release.Catalog, []byte) {
	t.Helper()
	dir := t.TempDir()
	content := []byte(strings.Repeat("0123456789abcdef", 16))
	if err := os.WriteFile(dir+"/"+appReleaseFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := release.Manifest{
		Package:                 appReleasePackage,
		ABI:                     appReleaseABI,
		MinSupportedVersionCode: 3,
		Latest: release.Latest{
			VersionCode: 4,
			VersionName: "2.0.2",
			APKFile:     appReleaseFile,
			Changelog:   "fix update flow",
			PublishedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := dir + "/releases.json"
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := release.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, content
}

func appReleaseRouter(h *AppReleaseHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/app/version", h.Version)
	router.GET("/api/app/download/:file", h.Download)
	return router
}

func appReleaseGet(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeAppVersion(t *testing.T, rec *httptest.ResponseRecorder) appReleaseVersionResponse {
	t.Helper()
	var body appReleaseVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	return body
}

func TestAppVersionReturnsLatestAndMandatoryFlag(t *testing.T) {
	catalog, _ := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, ""))

	rec := appReleaseGet(t, router, "/api/app/version?package="+appReleasePackage+"&versionCode=3&abi="+appReleaseABI)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAppVersion(t, rec)
	if body.Latest.VersionCode != 4 || body.Latest.VersionName != "2.0.2" {
		t.Fatalf("unexpected latest: %+v", body.Latest)
	}
	if body.MinSupportedVersionCode != 3 || body.Mandatory {
		t.Fatalf("min=%d mandatory=%v, want 3/false", body.MinSupportedVersionCode, body.Mandatory)
	}
	if body.Latest.APKURL != "https://example.test/api/app/download/"+appReleaseFile {
		t.Fatalf("apkUrl=%q", body.Latest.APKURL)
	}
	wantContent := []byte(strings.Repeat("0123456789abcdef", 16))
	wantSum := sha256.Sum256(wantContent)
	if body.Latest.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("sha256=%q", body.Latest.SHA256)
	}
	if body.Latest.SizeBytes != int64(len(wantContent)) {
		t.Fatalf("sizeBytes=%d", body.Latest.SizeBytes)
	}
	if body.Latest.Changelog != "fix update flow" || !body.Latest.PublishedAt.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("metadata mismatch: %+v", body.Latest)
	}
}

func TestAppVersionEnforcesMinimumAndCachedNoUpdate(t *testing.T) {
	catalog, _ := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, "https://cdn.example.com/"))

	rec := appReleaseGet(t, router, "/api/app/version?package="+appReleasePackage+"&versionCode=2&abi="+appReleaseABI)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAppVersion(t, rec)
	if !body.Mandatory {
		t.Fatal("versionCode below minSupportedVersionCode must be mandatory")
	}
	if body.Latest.APKURL != "https://cdn.example.com/api/app/download/"+appReleaseFile {
		t.Fatalf("configured base URL not used: %q", body.Latest.APKURL)
	}

	for _, versionCode := range []string{"4", "5"} {
		rec := appReleaseGet(t, router, "/api/app/version?package="+appReleasePackage+"&versionCode="+versionCode+"&abi="+appReleaseABI)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("versionCode=%s status=%d, want 204", versionCode, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("versionCode=%s body=%q, want empty", versionCode, rec.Body.String())
		}
	}
}

func TestAppVersionHidesOtherPackageAndABI(t *testing.T) {
	catalog, _ := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, ""))

	for _, path := range []string{
		"/api/app/version?package=com.example.other&versionCode=3&abi=" + appReleaseABI,
		"/api/app/version?package=" + appReleasePackage + "&versionCode=3&abi=x86_64",
	} {
		rec := appReleaseGet(t, router, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, leaked := range []string{appReleaseFile, `"latest"`, `"versionCode"`, "2.0.2"} {
			if strings.Contains(body, leaked) {
				t.Fatalf("%s leaked %q: %s", path, leaked, body)
			}
		}
	}
}

func TestAppVersionValidatesQuery(t *testing.T) {
	catalog, _ := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, ""))

	for _, path := range []string{
		"/api/app/version?package=" + appReleasePackage + "&abi=" + appReleaseABI,
		"/api/app/version?package=" + appReleasePackage + "&versionCode=abc&abi=" + appReleaseABI,
		"/api/app/version?package=" + appReleasePackage + "&versionCode=-1&abi=" + appReleaseABI,
		"/api/app/version?versionCode=3&abi=" + appReleaseABI,
		"/api/app/version?package=" + appReleasePackage + "&versionCode=3",
	} {
		rec := appReleaseGet(t, router, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestAppReleaseUnavailableReturns503(t *testing.T) {
	router := appReleaseRouter(NewAppReleaseHandler(nil, ""))

	for _, path := range []string{
		"/api/app/version?package=" + appReleasePackage + "&versionCode=3&abi=" + appReleaseABI,
		"/api/app/download/" + appReleaseFile,
	} {
		rec := appReleaseGet(t, router, path)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestAppDownloadServesAPKWithValidators(t *testing.T) {
	catalog, content := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, ""))

	rec := appReleaseGet(t, router, "/api/app/download/"+appReleaseFile)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("body does not match APK bytes")
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.android.package-archive" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges=%q", got)
	}
	if got := rec.Header().Get("ETag"); got != `"`+catalog.SHA256+`"` {
		t.Fatalf("ETag=%q", got)
	}
	if got := rec.Header().Get("Last-Modified"); got == "" {
		t.Fatal("Last-Modified missing")
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, appReleaseFile) {
		t.Fatalf("Content-Disposition=%q", got)
	}
}

func TestAppDownloadSupportsRangeAndConditionalRequests(t *testing.T) {
	catalog, content := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, ""))

	req := httptest.NewRequest(http.MethodGet, "/api/app/download/"+appReleaseFile, nil)
	req.Header.Set("Range", "bytes=0-99")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 0-99/"+strconv.Itoa(len(content)) {
		t.Fatalf("Content-Range=%q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), content[:100]) {
		t.Fatalf("range body length=%d", rec.Body.Len())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/app/download/"+appReleaseFile, nil)
	req.Header.Set("If-None-Match", `"`+catalog.SHA256+`"`)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional status=%d", rec.Code)
	}
}

func TestAppDownloadOnlyServesPublishedFile(t *testing.T) {
	catalog, _ := appReleaseFixture(t)
	router := appReleaseRouter(NewAppReleaseHandler(catalog, ""))

	for _, name := range []string{"other.apk", "releases.json", "..%2F" + appReleaseFile} {
		rec := appReleaseGet(t, router, "/api/app/download/"+name)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", name, rec.Code, rec.Body.String())
		}
	}
}
