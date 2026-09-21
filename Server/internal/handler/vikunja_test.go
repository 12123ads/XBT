package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xbt2/server/internal/common"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/testutil"
)

func vikunjaTestRouter(handler *VikunjaHandler) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(common.CtxUserUID, int64(42))
		c.Next()
	})
	router.GET("/settings", handler.Settings)
	router.PUT("/settings", handler.UpdateSettings)
	router.POST("/test", handler.TestConnection)
	return router
}

func requestVikunja(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func vikunjaResponseData(t *testing.T, recorder *httptest.ResponseRecorder, out interface{}) {
	t.Helper()
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || envelope.Code != 0 {
		t.Fatalf("request failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatal(err)
	}
}

func newVikunjaHandlerDatabase(t *testing.T) (*gorm.DB, *service.CredentialCrypto) {
	t.Helper()
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.VikunjaSettings{}); err != nil {
		t.Fatal(err)
	}
	return database, service.NewCredentialCrypto("vikunja-handler-test-secret")
}

func createVikunjaBinding(t *testing.T, database *gorm.DB, crypto *service.CredentialCrypto, baseURL string) model.VikunjaSettings {
	t.Helper()
	cipher, err := crypto.Encrypt("old-token")
	if err != nil {
		t.Fatal(err)
	}
	lastSync := time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)
	settings := model.VikunjaSettings{
		UserUID: 42, BoundInstanceURL: baseURL, APITokenCipher: cipher,
		ProjectID: 11, ProjectTitle: "Original project", Enabled: true,
		LastSyncAt: &lastSync, LastSyncMessage: "Existing sync result",
	}
	if err := database.Create(&settings).Error; err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestVikunjaHandlersRejectWritableDestinationsAndExtraJSON(t *testing.T) {
	var fixedRequests, sinkRequests atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sinkRequests.Add(1)
	}))
	defer sink.Close()
	fixed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixedRequests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects" || r.Header.Get("Authorization") != "Bearer scoped-token" {
			t.Errorf("unexpected connection-test request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `[{"id":11,"title":"Allowed project"}]`)
	}))
	defer fixed.Close()
	router := vikunjaTestRouter(NewVikunjaHandler(nil, service.NewCredentialCrypto("test"), nil, fixed.URL))
	cases := []struct {
		name, method, path, body string
	}{
		{"settings-address", http.MethodPut, "/settings", `{"enabled":true,"api_token":"scoped-token","project_id":11,"base_url":"` + sink.URL + `"}`},
		{"settings-title", http.MethodPut, "/settings", `{"enabled":true,"project_id":11,"project_title":"untrusted"}`},
		{"test-address", http.MethodPost, "/test", `{"api_token":"scoped-token","base_url":"` + sink.URL + `"}`},
		{"test-project", http.MethodPost, "/test", `{"api_token":"scoped-token","project_id":11}`},
		{"settings-two-values", http.MethodPut, "/settings", `{"enabled":false,"project_id":0} {}`},
		{"test-two-values", http.MethodPost, "/test", `{"api_token":"scoped-token"} {}`},
		{"missing-settings-fields", http.MethodPut, "/settings", `{}`},
		{"null-settings", http.MethodPut, "/settings", `null`},
		{"null-test", http.MethodPost, "/test", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := requestVikunja(router, tc.method, tc.path, tc.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid request was accepted: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if fixedRequests.Load() != 0 || sinkRequests.Load() != 0 {
		t.Fatalf("invalid body issued requests: fixed=%d sink=%d", fixedRequests.Load(), sinkRequests.Load())
	}
	response := requestVikunja(router, http.MethodPost, "/test", `{"api_token":"scoped-token"}`)
	var result struct {
		Projects []service.VikunjaProject `json:"projects"`
		User     json.RawMessage          `json:"user"`
	}
	vikunjaResponseData(t, response, &result)
	if len(result.Projects) != 1 || result.Projects[0].ID != 11 || result.User != nil || fixedRequests.Load() != 1 || sinkRequests.Load() != 0 {
		t.Fatalf("connection test must only list projects at the fixed instance: %+v", result)
	}
}

func TestVikunjaHandlerUpstreamUnauthorizedIsNotXBTUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "private-token echoed-upstream-body")
	}))
	defer server.Close()
	router := vikunjaTestRouter(NewVikunjaHandler(nil, service.NewCredentialCrypto("test"), nil, server.URL))
	response := requestVikunja(router, http.MethodPost, "/test", `{"api_token":"private-token"}`)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("upstream rejection must not invalidate XBT login: status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-token") || strings.Contains(response.Body.String(), "echoed-upstream-body") {
		t.Fatalf("response disclosed the upstream token/body: %s", response.Body.String())
	}
}

func TestVikunjaSettingsRequireExplicitRebindingToNewInstance(t *testing.T) {
	var oldRequests, newRequests atomic.Int32
	oldInstance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldRequests.Add(1)
	}))
	defer oldInstance.Close()
	newInstance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer replacement-token" {
			t.Error("a token belonging to the old instance was sent to the new one")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/projects":
			_, _ = io.WriteString(w, `[{"id":22,"title":"Verified project"}]`)
		case "/api/v1/projects/22":
			_, _ = io.WriteString(w, `{"id":22,"title":"Verified project"}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer newInstance.Close()
	database, crypto := newVikunjaHandlerDatabase(t)
	original := createVikunjaBinding(t, database, crypto, oldInstance.URL)
	router := vikunjaTestRouter(NewVikunjaHandler(database, crypto, nil, newInstance.URL))
	var view vikunjaSettingsView
	vikunjaResponseData(t, requestVikunja(router, http.MethodGet, "/settings", ""), &view)
	if view.BaseURL != newInstance.URL || view.TokenConfigured || view.TokenMask != "" || view.Enabled || view.ProjectID != 0 || view.ProjectTitle != "" {
		t.Fatalf("old instance settings leaked as a usable binding: %+v", view)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/test", ""},
		{http.MethodPut, "/settings", `{"enabled":true,"project_id":22}`},
	} {
		response := requestVikunja(router, request.method, request.path, request.body)
		if response.Code != http.StatusConflict {
			t.Fatalf("old token was reused: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if oldRequests.Load() != 0 || newRequests.Load() != 0 {
		t.Fatalf("rejected binding issued requests: old=%d new=%d", oldRequests.Load(), newRequests.Load())
	}
	var projects struct {
		Projects []service.VikunjaProject `json:"projects"`
	}
	vikunjaResponseData(t, requestVikunja(router, http.MethodPost, "/test", `{"api_token":"replacement-token"}`), &projects)
	vikunjaResponseData(t, requestVikunja(router, http.MethodGet, "/settings", ""), &view)
	if view.TokenConfigured {
		t.Fatal("connection testing must not silently replace saved credentials")
	}
	response := requestVikunja(router, http.MethodPut, "/settings", `{"enabled":true,"project_id":22,"api_token":"replacement-token"}`)
	vikunjaResponseData(t, response, &view)
	if !view.Enabled || !view.TokenConfigured || view.ProjectID != 22 || view.ProjectTitle != "Verified project" {
		t.Fatalf("verified binding was not saved: %+v", view)
	}
	var saved model.VikunjaSettings
	if err := database.Where("user_uid = ?", 42).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	token, err := crypto.Decrypt(saved.APITokenCipher)
	if err != nil || token != "replacement-token" || saved.BoundInstanceURL != newInstance.URL {
		t.Fatal("new token was not bound to the configured instance")
	}
	if saved.LastSyncMessage != original.LastSyncMessage || saved.LastSyncAt == nil || !saved.LastSyncAt.Equal(*original.LastSyncAt) {
		t.Fatal("saving a connection overwrote sync status")
	}
	vikunjaResponseData(t, requestVikunja(router, http.MethodPost, "/test", ""), &projects)
	if len(projects.Projects) != 1 || projects.Projects[0].ID != 22 || oldRequests.Load() != 0 || newRequests.Load() != 3 {
		t.Fatal("empty-token test did not use the newly bound credentials")
	}

	unconfigured := vikunjaTestRouter(NewVikunjaHandler(database, crypto, nil, ""))
	response = requestVikunja(unconfigured, http.MethodPost, "/test", `{"api_token":"replacement-token"}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty global configuration must disable outbound requests: %d", response.Code)
	}
	response = requestVikunja(unconfigured, http.MethodPut, "/settings", `{"enabled":false,"project_id":0}`)
	vikunjaResponseData(t, response, &view)
	var disabled model.VikunjaSettings
	if err := database.Where("user_uid = ?", 42).First(&disabled).Error; err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.ProjectID != 0 || disabled.ProjectTitle != "" || disabled.APITokenCipher != saved.APITokenCipher || disabled.BoundInstanceURL != saved.BoundInstanceURL || newRequests.Load() != 3 {
		t.Fatal("offline disable must clear selection, preserve credentials and avoid the network")
	}
}

func TestVikunjaSettingsRejectUndecryptableSavedToken(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	database, crypto := newVikunjaHandlerDatabase(t)
	settings := createVikunjaBinding(t, database, crypto, server.URL)
	if err := database.Model(&settings).Update("api_token_cipher", "unreadable-ciphertext").Error; err != nil {
		t.Fatal(err)
	}
	router := vikunjaTestRouter(NewVikunjaHandler(database, crypto, nil, server.URL))
	var view vikunjaSettingsView
	vikunjaResponseData(t, requestVikunja(router, http.MethodGet, "/settings", ""), &view)
	if view.TokenConfigured || view.Enabled || view.ProjectID != 0 {
		t.Fatal("unreadable credentials were presented as usable")
	}
	response := requestVikunja(router, http.MethodPost, "/test", "")
	if response.Code != http.StatusConflict || requests.Load() != 0 {
		t.Fatalf("unreadable credential was used: status=%d requests=%d", response.Code, requests.Load())
	}
}

func TestVikunjaSettingsValidateSelectedProjectBeforeSaving(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"no-access", http.StatusForbidden, `private-token upstream-error`},
		{"wrong-project", http.StatusOK, `{"id":33,"title":"Wrong project"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects/22" {
					t.Errorf("selection was not validated by its project endpoint: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			database, crypto := newVikunjaHandlerDatabase(t)
			original := createVikunjaBinding(t, database, crypto, server.URL)
			router := vikunjaTestRouter(NewVikunjaHandler(database, crypto, nil, server.URL))
			response := requestVikunja(router, http.MethodPut, "/settings", `{"enabled":true,"project_id":22,"api_token":"private-token"}`)
			if response.Code != http.StatusBadGateway || requests.Load() != 1 {
				t.Fatalf("invalid project selection succeeded: status=%d body=%s", response.Code, response.Body.String())
			}
			var saved model.VikunjaSettings
			if err := database.Where("user_uid = ?", 42).First(&saved).Error; err != nil {
				t.Fatal(err)
			}
			if saved.APITokenCipher != original.APITokenCipher || saved.ProjectID != original.ProjectID || saved.ProjectTitle != original.ProjectTitle || saved.Enabled != original.Enabled {
				t.Fatal("failed project validation changed the saved connection")
			}
			if strings.Contains(response.Body.String(), "private-token") || strings.Contains(response.Body.String(), "upstream-error") {
				t.Fatal("upstream failure leaked its body or token")
			}
		})
	}
}

func TestVikunjaSettingsConcurrentSavesKeepNewestCredentials(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new-row"
		if existing {
			name = "existing-row"
		}
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/projects/22":
					close(started)
					<-release
					_, _ = io.WriteString(w, `{"id":22,"title":"Slow selection"}`)
				case "/api/v1/projects/33":
					_, _ = io.WriteString(w, `{"id":33,"title":"Newest selection"}`)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			defer resume()
			database, crypto := newVikunjaHandlerDatabase(t)
			if existing {
				createVikunjaBinding(t, database, crypto, server.URL)
			}
			router := vikunjaTestRouter(NewVikunjaHandler(database, crypto, nil, server.URL))
			slow := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				slow <- requestVikunja(router, http.MethodPut, "/settings", `{"enabled":true,"project_id":22,"api_token":"slow-token"}`)
			}()
			awaitVikunjaHandlerRequest(t, started)
			fast := requestVikunja(router, http.MethodPut, "/settings", `{"enabled":true,"project_id":33,"api_token":"newest-token"}`)
			if fast.Code != http.StatusOK {
				t.Fatalf("concurrent fresh save failed: %d %s", fast.Code, fast.Body.String())
			}
			newStatusAt := time.Date(2026, 9, 20, 7, 0, 0, 0, time.UTC)
			if err := database.Model(&model.VikunjaSettings{}).Where("user_uid = ?", 42).Updates(map[string]interface{}{
				"last_sync_message": "Latest sync result", "last_sync_at": newStatusAt,
			}).Error; err != nil {
				t.Fatal(err)
			}
			resume()
			select {
			case response := <-slow:
				if response.Code != http.StatusConflict {
					t.Fatalf("stale save overwrote a newer token: %d %s", response.Code, response.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stale save did not return")
			}
			var saved model.VikunjaSettings
			if err := database.Where("user_uid = ?", 42).First(&saved).Error; err != nil {
				t.Fatal(err)
			}
			token, err := crypto.Decrypt(saved.APITokenCipher)
			if err != nil || token != "newest-token" || saved.ProjectID != 33 || saved.ProjectTitle != "Newest selection" || saved.LastSyncMessage != "Latest sync result" || saved.LastSyncAt == nil || !saved.LastSyncAt.Equal(newStatusAt) {
				t.Fatal("stale verification overwrote newer settings or synchronization status")
			}
		})
	}
}

func TestVikunjaSettingsSavePreservesConcurrentSyncStatus(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"id":22,"title":"Verified project"}`)
	}))
	defer server.Close()
	defer resume()
	database, crypto := newVikunjaHandlerDatabase(t)
	createVikunjaBinding(t, database, crypto, server.URL)
	router := vikunjaTestRouter(NewVikunjaHandler(database, crypto, nil, server.URL))
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- requestVikunja(router, http.MethodPut, "/settings", `{"enabled":true,"project_id":22}`)
	}()
	awaitVikunjaHandlerRequest(t, started)
	newStatusAt := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	if err := database.Model(&model.VikunjaSettings{}).Where("user_uid = ?", 42).Updates(map[string]interface{}{
		"last_sync_message": "Concurrent sync result", "last_sync_at": newStatusAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case response := <-finished:
		var view vikunjaSettingsView
		vikunjaResponseData(t, response, &view)
		if view.ProjectID != 22 || view.LastSyncMessage != "Concurrent sync result" || view.LastSyncAt == nil || !view.LastSyncAt.Equal(newStatusAt) {
			t.Fatalf("saving connection fields lost a concurrently recorded result: %+v", view)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settings save did not finish")
	}
}

func awaitVikunjaHandlerRequest(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Vikunja project verification did not start")
	}
}
