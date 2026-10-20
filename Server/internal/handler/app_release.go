package handler

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/common"
	"xbt2/server/internal/release"
)

// AppReleaseHandler serves the public update manifest and APK download.
// Both routes are intentionally unauthenticated: an outdated or logged-out
// client must still be able to learn about and install an update.
type AppReleaseHandler struct {
	catalog       *release.Catalog
	publicBaseURL string
}

func NewAppReleaseHandler(catalog *release.Catalog, publicBaseURL string) *AppReleaseHandler {
	return &AppReleaseHandler{
		catalog:       catalog,
		publicBaseURL: strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"),
	}
}

type appReleaseLatest struct {
	VersionCode int64     `json:"versionCode"`
	VersionName string    `json:"versionName"`
	APKURL      string    `json:"apkUrl"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"sizeBytes"`
	Changelog   string    `json:"changelog"`
	PublishedAt time.Time `json:"publishedAt"`
}

type appReleaseVersionResponse struct {
	Latest                  appReleaseLatest `json:"latest"`
	MinSupportedVersionCode int64            `json:"minSupportedVersionCode"`
	Mandatory               bool             `json:"mandatory"`
}

// Version implements GET /api/app/version.
//
// The success body is the raw update object (not wrapped in the usual
// code/message/data envelope); errors keep the common envelope. A client
// already on the latest version or newer gets 204 No Content.
func (h *AppReleaseHandler) Version(c *gin.Context) {
	if h.catalog == nil {
		common.Fail(c, http.StatusServiceUnavailable, "app release unavailable")
		return
	}

	pkg := strings.TrimSpace(c.Query("package"))
	abi := strings.TrimSpace(c.Query("abi"))
	rawVersionCode := strings.TrimSpace(c.Query("versionCode"))
	if pkg == "" || abi == "" || rawVersionCode == "" {
		common.Fail(c, http.StatusBadRequest, "package, versionCode and abi are required")
		return
	}
	versionCode, err := strconv.ParseInt(rawVersionCode, 10, 64)
	if err != nil || versionCode < 0 {
		common.Fail(c, http.StatusBadRequest, "invalid versionCode")
		return
	}
	if !h.catalog.Matches(pkg, abi) {
		// Do not reveal another package's metadata or even whether a different
		// package exists.
		common.Fail(c, http.StatusNotFound, "not found")
		return
	}

	latest := h.catalog.Manifest.Latest
	if versionCode >= latest.VersionCode {
		c.Status(http.StatusNoContent)
		return
	}

	baseURL, ok := h.releaseBaseURL(c)
	if !ok {
		common.Fail(c, http.StatusServiceUnavailable, "app release unavailable")
		return
	}

	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, appReleaseVersionResponse{
		Latest: appReleaseLatest{
			VersionCode: latest.VersionCode,
			VersionName: latest.VersionName,
			APKURL:      baseURL + "/api/app/download/" + url.PathEscape(latest.APKFile),
			SHA256:      h.catalog.SHA256,
			SizeBytes:   h.catalog.SizeBytes,
			Changelog:   latest.Changelog,
			PublishedAt: latest.PublishedAt,
		},
		MinSupportedVersionCode: h.catalog.Manifest.MinSupportedVersionCode,
		Mandatory:               versionCode < h.catalog.Manifest.MinSupportedVersionCode,
	})
}

// Download implements GET /api/app/download/:file. Only the APK named by the
// currently loaded manifest is served, so internal or test packages placed in
// the release directory are never exposed by guessing a file name.
func (h *AppReleaseHandler) Download(c *gin.Context) {
	if h.catalog == nil {
		common.Fail(c, http.StatusServiceUnavailable, "app release unavailable")
		return
	}
	name := c.Param("file")
	if name != h.catalog.Manifest.Latest.APKFile {
		common.Fail(c, http.StatusNotFound, "not found")
		return
	}

	f, err := os.Open(h.catalog.APKPath)
	if err != nil {
		log.Printf("app_release download_open_failed path=%s error=%v", h.catalog.APKPath, err)
		common.Fail(c, http.StatusServiceUnavailable, "app release unavailable")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		log.Printf("app_release download_stat_failed path=%s error=%v", h.catalog.APKPath, err)
		common.Fail(c, http.StatusServiceUnavailable, "app release unavailable")
		return
	}
	if info.Size() != h.catalog.SizeBytes || !info.ModTime().Equal(h.catalog.ModTime) {
		// The manifest was loaded at startup. Replacing the APK in place
		// without a restart would make the advertised hash wrong, so refuse
		// rather than serve bytes that no longer match /api/app/version.
		log.Printf("app_release download_changed path=%s size=%d expected_size=%d", h.catalog.APKPath, info.Size(), h.catalog.SizeBytes)
		common.Fail(c, http.StatusServiceUnavailable, "app release unavailable")
		return
	}

	c.Header("Content-Type", "application/vnd.android.package-archive")
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Header("Accept-Ranges", "bytes")
	c.Header("ETag", `"`+h.catalog.SHA256+`"`)
	// The file name contains the version, so an immutable response is safe.
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), f)
}

func (h *AppReleaseHandler) releaseBaseURL(c *gin.Context) (string, bool) {
	if h.publicBaseURL != "" {
		return h.publicBaseURL, true
	}
	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		return "", false
	}
	// Never emit an http:// apkUrl, even when the request reached the server
	// over plain HTTP behind a misconfigured proxy.
	return "https://" + host, true
}
