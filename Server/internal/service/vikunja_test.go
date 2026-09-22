package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"xbt2/server/internal/model"
	"xbt2/server/internal/testutil"
	"xbt2/server/internal/xxt"
)

func TestVikunjaClientDoesNotRedirectCredentials(t *testing.T) {
	var sinkRequests atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sinkRequests.Add(1)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer sink.Close()
	var fixedRequests atomic.Int32
	fixed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixedRequests.Add(1)
		if r.URL.Path != "/prefix/api/v1/projects" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("unexpected request to fixed instance: %s, authorization present=%t", r.URL.Path, r.Header.Get("Authorization") != "")
		}
		w.Header().Set("Location", sink.URL+"/stolen")
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, "private-token remote-response-secret")
	}))
	defer fixed.Close()

	client, err := NewVikunjaClient("HTTP://"+strings.TrimPrefix(fixed.URL, "http://")+"/prefix/", "private-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListProjects(context.Background())
	var httpErr *VikunjaHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusFound {
		t.Fatalf("redirect must fail as an upstream HTTP error, got %v", err)
	}
	if fixedRequests.Load() != 1 || sinkRequests.Load() != 0 {
		t.Fatalf("requests reached fixed=%d, redirected=%d", fixedRequests.Load(), sinkRequests.Load())
	}
	if strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "remote-response-secret") || strings.Contains(err.Error(), sink.URL) {
		t.Fatalf("upstream error disclosed a secret: %v", err)
	}
}

func TestVikunjaClientSanitizesErrorsAndPreservesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "private-token echoed-response")
	}))
	client, err := NewVikunjaClient(server.URL, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FindLabelByTitle(context.Background(), "private-course-query")
	var httpErr *VikunjaHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503, got %v", err)
	}
	for _, secret := range []string{"private-token", "echoed-response", "private-course-query"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error disclosed %q", secret)
		}
	}
	server.Close()
	_, err = client.ListProjects(context.Background())
	var requestErr *VikunjaRequestError
	if !errors.As(err, &requestErr) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("transport failure must be safely classified, got %v", err)
	}

	started := make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer blocked.Close()
	client, err = NewVikunjaClient(blocked.URL, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := client.ListProjects(ctx)
		finished <- err
	}()
	awaitVikunjaSignal(t, started)
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation was lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not finish")
	}
}

type vikunjaHomeworkFunc func(string, string) ([]xxt.LearningItem, error)

func (f vikunjaHomeworkFunc) GetLearningHomeworkFor(mobile, password string) ([]xxt.LearningItem, error) {
	return f(mobile, password)
}

type vikunjaSyncFixture struct {
	service  *VikunjaSyncService
	db       *gorm.DB
	crypto   *CredentialCrypto
	settings model.VikunjaSettings
}

func newVikunjaSyncFixture(t *testing.T, baseURL string, homework learningHomeworkClient) *vikunjaSyncFixture {
	t.Helper()
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.Whitelist{}, &model.VikunjaSettings{}, &model.VikunjaSyncItem{}); err != nil {
		t.Fatal(err)
	}
	crypto := NewCredentialCrypto("vikunja-test-secret")
	password, err := crypto.Encrypt("school-password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := crypto.Encrypt("token-for-a")
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{UID: 42, Mobile: "13800138000", Name: "Test", Permission: 1, CredentialCipher: password}
	whitelist := model.Whitelist{Mobile: user.Mobile, Permission: 1}
	settings := model.VikunjaSettings{UserUID: user.UID, BoundInstanceURL: baseURL, APITokenCipher: token, ProjectID: 11, ProjectTitle: "A", Enabled: true}
	for _, row := range []interface{}{&user, &whitelist, &settings} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc, err := NewVikunjaSyncService(database, homework, crypto, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	return &vikunjaSyncFixture{service: svc, db: database, crypto: crypto, settings: settings}
}

type vikunjaRemote struct {
	server  *httptest.Server
	mu      sync.Mutex
	nextID  int64
	tasks   map[int64]map[string]json.RawMessage
	created map[int64]int
	updated map[int64]int
	tokens  []string
}

func newVikunjaRemote(t *testing.T) *vikunjaRemote {
	t.Helper()
	remote := &vikunjaRemote{nextID: 1, tasks: make(map[int64]map[string]json.RawMessage), created: make(map[int64]int), updated: make(map[int64]int)}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{project}/tasks", func(w http.ResponseWriter, r *http.Request) {
		var task map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			t.Errorf("decode created task: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		projectID, _ := strconv.ParseInt(r.PathValue("project"), 10, 64)
		remote.mu.Lock()
		defer remote.mu.Unlock()
		id := remote.nextID
		remote.nextID++
		task["id"] = json.RawMessage(strconv.FormatInt(id, 10))
		task["project_id"] = json.RawMessage(strconv.FormatInt(projectID, 10))
		remote.tasks[id] = task
		remote.created[projectID]++
		_ = json.NewEncoder(w).Encode(task)
	})
	mux.HandleFunc("GET /api/v1/tasks/{task}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("task"), 10, 64)
		remote.mu.Lock()
		defer remote.mu.Unlock()
		task, ok := remote.tasks[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(task)
	})
	mux.HandleFunc("POST /api/v1/tasks/{task}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("task"), 10, 64)
		var task map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			t.Errorf("decode updated task: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		remote.mu.Lock()
		defer remote.mu.Unlock()
		if _, ok := remote.tasks[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		remote.tasks[id] = task
		remote.updated[id]++
		_ = json.NewEncoder(w).Encode(task)
	})
	remote.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote.mu.Lock()
		remote.tokens = append(remote.tokens, r.Header.Get("Authorization"))
		remote.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(remote.server.Close)
	return remote
}

func (r *vikunjaRemote) task(t *testing.T, id int64) map[string]json.RawMessage {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		t.Fatalf("remote task %d is missing", id)
	}
	return maps.Clone(task)
}

func (r *vikunjaRemote) edit(id int64, fields map[string]json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range fields {
		r.tasks[id][key] = value
	}
}

func (r *vikunjaRemote) createdIn(projectID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.created[projectID]
}

func requireVikunjaJSON(t *testing.T, actual, expected json.RawMessage) {
	t.Helper()
	var a, b bytes.Buffer
	if err := json.Compact(&a, actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&b, expected); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("JSON changed: got %s, want %s", actual, expected)
	}
}

func TestVikunjaSyncPreservesRemoteFieldsAndNamespaces(t *testing.T) {
	remoteA := newVikunjaRemote(t)
	items := []xxt.LearningItem{{ID: "shared-homework", Title: "original title", EndTime: 1800000000000, Pending: true}}
	fixture := newVikunjaSyncFixture(t, remoteA.server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) { return items, nil }))
	legacy := model.VikunjaSyncItem{UserUID: 42, InstanceURL: "", ProjectID: 11, ItemKey: "homework:shared-homework", VikunjaTaskID: 900, Title: "legacy"}
	if err := fixture.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.SyncUser(context.Background(), 42)
	if err != nil || result.Created != 1 || remoteA.createdIn(11) != 1 {
		t.Fatalf("first namespace sync: result=%+v, err=%v", result, err)
	}
	manual := map[string]json.RawMessage{
		"description":  json.RawMessage(`"human notes"`),
		"done":         json.RawMessage(`true`),
		"priority":     json.RawMessage(`5`),
		"reminders":    json.RawMessage(`[{"relative_period":-3600,"relative_to":"due_date"}]`),
		"assignees":    json.RawMessage(`[{"id":77,"username":"owner"}]`),
		"due_date":     json.RawMessage(`"2030-04-05T06:07:08Z"`),
		"repeat_after": json.RawMessage(`86400`),
		"hex_color":    json.RawMessage(`"112233"`),
		"future_field": json.RawMessage(`{"large_number":9007199254740993,"nested":[true,null]}`),
	}
	remoteA.edit(1, manual)
	items[0].Title = "new source title"
	items[0].EndTime = 0
	result, err = fixture.service.SyncUser(context.Background(), 42)
	if err != nil || result.Updated != 1 {
		t.Fatalf("title update: result=%+v, err=%v", result, err)
	}
	updated := remoteA.task(t, 1)
	requireVikunjaJSON(t, updated["title"], json.RawMessage(`"new source title"`))
	for key, value := range manual {
		requireVikunjaJSON(t, updated[key], value)
	}
	var mapping model.VikunjaSyncItem
	if err := fixture.db.Where("user_uid = ? AND instance_url = ? AND project_id = ? AND item_key = ?", 42, remoteA.server.URL, 11, legacy.ItemKey).First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if mapping.DueDate != 1800000000000 {
		t.Fatalf("unknown source deadline erased remembered deadline: %d", mapping.DueDate)
	}
	remoteA.edit(1, map[string]json.RawMessage{"title": json.RawMessage(`"manual title"`)})
	items[0].EndTime = 1900000000000
	result, err = fixture.service.SyncUser(context.Background(), 42)
	if err != nil || result.Updated != 1 {
		t.Fatalf("deadline update: result=%+v, err=%v", result, err)
	}
	updated = remoteA.task(t, 1)
	requireVikunjaJSON(t, updated["title"], json.RawMessage(`"manual title"`))
	due, _ := json.Marshal(time.UnixMilli(items[0].EndTime).UTC().Format(time.RFC3339))
	requireVikunjaJSON(t, updated["due_date"], due)
	for key, value := range manual {
		if key != "due_date" {
			requireVikunjaJSON(t, updated[key], value)
		}
	}

	if err := fixture.db.Model(&model.VikunjaSettings{}).Where("user_uid = ?", 42).Update("project_id", 22).Error; err != nil {
		t.Fatal(err)
	}
	result, err = fixture.service.SyncUser(context.Background(), 42)
	if err != nil || result.Created != 1 || remoteA.createdIn(22) != 1 {
		t.Fatalf("new project must create its own mapping: result=%+v, err=%v", result, err)
	}
	result, err = fixture.service.SyncUser(context.Background(), 42)
	if err != nil || result.Created != 0 || result.Updated != 0 || remoteA.createdIn(22) != 1 {
		t.Fatalf("repeating project sync was not idempotent: result=%+v, err=%v", result, err)
	}
	if err := fixture.db.Model(&model.VikunjaSettings{}).Where("user_uid = ?", 42).Update("project_id", 11).Error; err != nil {
		t.Fatal(err)
	}
	result, err = fixture.service.SyncUser(context.Background(), 42)
	if err != nil || result.Created != 0 || result.Updated != 0 || remoteA.createdIn(11) != 1 {
		t.Fatalf("returning to old project lost its mapping: result=%+v, err=%v", result, err)
	}

	remoteB := newVikunjaRemote(t)
	serviceB, err := NewVikunjaSyncService(fixture.db, fixture.service.xxt, fixture.crypto, remoteB.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceB.SyncUser(context.Background(), 42); !errors.Is(err, ErrVikunjaRebindRequired) {
		t.Fatalf("new instance accepted old credentials: %v", err)
	}
	remoteB.mu.Lock()
	beforeRebind := len(remoteB.tokens)
	remoteB.mu.Unlock()
	if beforeRebind != 0 {
		t.Fatalf("new instance received %d requests before rebinding", beforeRebind)
	}
	newToken, err := fixture.crypto.Encrypt("token-for-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&model.VikunjaSettings{}).Where("user_uid = ?", 42).Updates(map[string]interface{}{"bound_instance_url": remoteB.server.URL, "api_token_cipher": newToken}).Error; err != nil {
		t.Fatal(err)
	}
	result, err = serviceB.SyncUser(context.Background(), 42)
	if err != nil || result.Created != 1 || remoteB.createdIn(11) != 1 {
		t.Fatalf("new instance reused another instance's task ID: result=%+v, err=%v", result, err)
	}
	remoteB.mu.Lock()
	for _, token := range remoteB.tokens {
		if token != "Bearer token-for-b" {
			t.Errorf("old credential reached the new instance")
		}
	}
	remoteB.mu.Unlock()
	var count int64
	if err := fixture.db.Model(&model.VikunjaSyncItem{}).Where("user_uid = ? AND item_key = ?", 42, legacy.ItemKey).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("expected independent legacy, A/11, A/22 and B/11 mappings, count=%d err=%v", count, err)
	}
}

func TestVikunjaSyncStopsWhenSettingsChangeDuringTaskRead(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/tasks/1" {
			t.Errorf("obsolete configuration made another remote operation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"id":1,"project_id":11,"title":"old","description":"keep"}`)
	}))
	defer server.Close()
	defer resume()
	fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		return []xxt.LearningItem{{ID: "one", Title: "new", Pending: true}, {ID: "two", Title: "second", Pending: true}}, nil
	}))
	mapping := model.VikunjaSyncItem{UserUID: 42, InstanceURL: server.URL, ProjectID: 11, ItemKey: "homework:one", VikunjaTaskID: 1, Title: "old"}
	if err := fixture.db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result *VikunjaSyncResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := fixture.service.SyncUser(context.Background(), 42)
		finished <- outcome{result, err}
	}()
	awaitVikunjaSignal(t, started)
	newToken, err := fixture.crypto.Encrypt("replacement-token")
	if err != nil {
		resume()
		t.Fatal(err)
	}
	newStatusAt := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	if err := fixture.db.Model(&model.VikunjaSettings{}).Where("user_uid = ?", 42).Updates(map[string]interface{}{
		"project_id": 22, "api_token_cipher": newToken, "last_sync_at": newStatusAt, "last_sync_message": "new configuration status",
	}).Error; err != nil {
		resume()
		t.Fatal(err)
	}
	resume()
	select {
	case got := <-finished:
		if !errors.Is(got.err, ErrVikunjaSettingsChanged) || got.result.Created != 0 || got.result.Updated != 0 {
			t.Fatalf("obsolete sync was accepted: result=%+v err=%v", got.result, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("obsolete sync did not stop")
	}
	if requests.Load() != 1 {
		t.Fatalf("obsolete sync issued %d requests", requests.Load())
	}
	var saved model.VikunjaSettings
	if err := fixture.db.Where("user_uid = ?", 42).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.APITokenCipher != newToken || saved.ProjectID != 22 || saved.LastSyncMessage != "new configuration status" || saved.LastSyncAt == nil || !saved.LastSyncAt.Equal(newStatusAt) {
		t.Fatal("obsolete sync overwrote the newer settings or their status")
	}
}

func TestVikunjaSyncRechecksRevocationBeforeRemoteWrites(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("revoked account issued %s", r.Method)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"id":1,"project_id":11,"title":"old"}`)
	}))
	defer server.Close()
	defer resume()
	var homeworkCalls atomic.Int32
	fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		homeworkCalls.Add(1)
		return []xxt.LearningItem{{ID: "one", Title: "new", Pending: true}}, nil
	}))
	mapping := model.VikunjaSyncItem{UserUID: 42, InstanceURL: server.URL, ProjectID: 11, ItemKey: "homework:one", VikunjaTaskID: 1, Title: "old"}
	if err := fixture.db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := fixture.service.SyncUser(context.Background(), 42)
		finished <- err
	}()
	awaitVikunjaSignal(t, started)
	if err := fixture.db.Where("mobile = ?", "13800138000").Delete(&model.Whitelist{}).Error; err != nil {
		resume()
		t.Fatal(err)
	}
	resume()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrAccountInactive) {
			t.Fatalf("revocation was ignored: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked synchronization did not finish")
	}
	if _, err := fixture.service.SyncUser(context.Background(), 42); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("inactive account was accepted on a new sync: %v", err)
	}
	if requests.Load() != 1 || homeworkCalls.Load() != 1 {
		t.Fatalf("revoked credentials were used again: requests=%d homework=%d", requests.Load(), homeworkCalls.Load())
	}
}

func TestVikunjaSyncRejectsStaleTaskMappings(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "forbidden", status: http.StatusForbidden},
		{name: "different-task", status: http.StatusOK, body: `{"id":99,"project_id":11}`},
		{name: "moved-project", status: http.StatusOK, body: `{"id":1,"project_id":22}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/tasks/1" {
					t.Errorf("stale mapping caused a write or recreation: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
				return []xxt.LearningItem{{ID: "one", Title: "new", Pending: true}}, nil
			}))
			mapping := model.VikunjaSyncItem{UserUID: 42, InstanceURL: server.URL, ProjectID: 11, ItemKey: "homework:one", VikunjaTaskID: 1, Title: "old"}
			if err := fixture.db.Create(&mapping).Error; err != nil {
				t.Fatal(err)
			}
			result, err := fixture.service.SyncUser(context.Background(), 42)
			if err == nil || result.Created != 0 || result.Updated != 0 || requests.Load() != 1 {
				t.Fatalf("stale mapping succeeded: result=%+v err=%v requests=%d", result, err, requests.Load())
			}
			var saved model.VikunjaSyncItem
			if err := fixture.db.First(&saved, mapping.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.VikunjaTaskID != 1 || saved.Title != "old" || saved.ProjectID != 11 {
				t.Fatal("failed synchronization mutated the mapping")
			}
		})
	}
}

func TestVikunjaSyncReportsPartialFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/projects/11/tasks" {
			t.Errorf("unexpected remote operation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var task map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, ok := task["due_date"]; ok {
			t.Error("unknown source deadline must be omitted when creating a task")
		}
		if string(task["title"]) == `"fails"` {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "token-for-a private-response")
			return
		}
		_, _ = io.WriteString(w, `{"id":2,"project_id":11,"title":"succeeds"}`)
	}))
	defer server.Close()
	fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		return []xxt.LearningItem{{ID: "one", Title: "fails", Pending: true}, {ID: "two", Title: "succeeds", Pending: true}}, nil
	}))
	result, err := fixture.service.SyncUser(context.Background(), 42)
	var upstream *VikunjaHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusServiceUnavailable || result.Total != 2 || result.Created != 1 || result.Updated != 0 {
		t.Fatalf("partial failure was reported as success: result=%+v err=%v", result, err)
	}
	if strings.Contains(err.Error(), "token-for-a") || strings.Contains(err.Error(), "private-response") || requests.Load() != 2 {
		t.Fatalf("unexpected failure disclosure or retries: %v; requests=%d", err, requests.Load())
	}
	var mappings []model.VikunjaSyncItem
	if err := fixture.db.Where("user_uid = ?", 42).Find(&mappings).Error; err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || mappings[0].ItemKey != "homework:two" || mappings[0].VikunjaTaskID != 2 {
		t.Fatalf("partial sync persisted the wrong successful work: %+v", mappings)
	}
}

func TestVikunjaSyncCancellationIsNotSuccess(t *testing.T) {
	started := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	var homeworkCalls atomic.Int32
	fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		homeworkCalls.Add(1)
		return []xxt.LearningItem{{ID: "one", Title: "first", Pending: true}, {ID: "two", Title: "second", Pending: true}}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result *VikunjaSyncResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := fixture.service.SyncUser(ctx, 42)
		finished <- outcome{result, err}
	}()
	awaitVikunjaSignal(t, started)
	cancel()
	select {
	case got := <-finished:
		if !errors.Is(got.err, context.Canceled) || got.result.Created != 0 || got.result.Updated != 0 {
			t.Fatalf("canceled synchronization succeeded: result=%+v err=%v", got.result, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled synchronization did not finish")
	}
	if _, err := fixture.service.SyncUser(ctx, 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled context was ignored: %v", err)
	}
	if requests.Load() != 1 || homeworkCalls.Load() != 1 {
		t.Fatalf("cancellation allowed more work: requests=%d homework=%d", requests.Load(), homeworkCalls.Load())
	}
}

func TestVikunjaSyncCountsOnlyPersistedTaskUpdates(t *testing.T) {
	var database atomic.Pointer[gorm.DB]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"id":1,"project_id":11,"title":"old"}`)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := database.Load().Where("user_uid = ?", 42).Delete(&model.VikunjaSyncItem{}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		return []xxt.LearningItem{{ID: "one", Title: "new", Pending: true}}, nil
	}))
	database.Store(fixture.db)
	mapping := model.VikunjaSyncItem{UserUID: 42, InstanceURL: server.URL, ProjectID: 11, ItemKey: "homework:one", VikunjaTaskID: 1, Title: "old"}
	if err := fixture.db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.SyncUser(context.Background(), 42)
	if err == nil || result.Updated != 0 {
		t.Fatalf("missing local mapping counted as an update: result=%+v err=%v", result, err)
	}
}

func awaitVikunjaSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("Vikunja request did not reach the expected boundary")
	}
}

func TestVikunjaSyncWithoutGlobalInstanceRejectsOutboundWork(t *testing.T) {
	var homeworkCalls atomic.Int32
	svc, err := NewVikunjaSyncService(nil, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		homeworkCalls.Add(1)
		return nil, nil
	}), NewCredentialCrypto("test"), "")
	if err != nil {
		t.Fatalf("an unconfigured optional integration must not prevent startup: %v", err)
	}
	if _, err := svc.SyncUser(context.Background(), 42); !errors.Is(err, ErrVikunjaNotConfigured) || homeworkCalls.Load() != 0 {
		t.Fatalf("unconfigured integration attempted synchronization: err=%v calls=%d", err, homeworkCalls.Load())
	}
}

func TestVikunjaSyncCanceledWhileWaitingDoesNotStartAnotherAccountItem(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	var remoteRequests, homeworkCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteRequests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	defer resume()
	fixture := newVikunjaSyncFixture(t, server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		if homeworkCalls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil, nil
	}))
	first := make(chan error, 1)
	go func() {
		_, err := fixture.service.SyncUser(context.Background(), 42)
		first <- err
	}()
	awaitVikunjaSignal(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := make(chan error, 1)
	go func() {
		_, err := fixture.service.SyncUser(ctx, 42)
		second <- err
	}()
	cancel()
	resume()
	select {
	case err := <-first:
		if err != nil {
			t.Fatalf("first synchronization failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first synchronization did not finish")
	}
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting cancellation was ignored: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled waiter did not finish")
	}
	if homeworkCalls.Load() != 1 || remoteRequests.Load() != 0 {
		t.Fatalf("canceled waiter used credentials: homework=%d remote=%d", homeworkCalls.Load(), remoteRequests.Load())
	}
}

func TestVikunjaSyncRecreatesDeletedRemoteTaskWithDueDate(t *testing.T) {
	remote := newVikunjaRemote(t)
	items := []xxt.LearningItem{{ID: "one", Title: "作业一", EndTime: 1900000000000, Pending: true}}
	fixture := newVikunjaSyncFixture(t, remote.server.URL, vikunjaHomeworkFunc(func(string, string) ([]xxt.LearningItem, error) {
		return items, nil
	}))
	// 指向一个远端已不存在的任务，模拟用户在 Vikunja 里删除后的失效映射。
	stale := model.VikunjaSyncItem{UserUID: 42, InstanceURL: remote.server.URL, ProjectID: 11, ItemKey: "homework:one", VikunjaTaskID: 404, Title: "旧标题"}
	if err := fixture.db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.SyncUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("deleted remote task must be recreated, not error: %v", err)
	}
	if result.Created != 1 || remote.createdIn(11) != 1 {
		t.Fatalf("expected one recreated task, result=%+v createdIn=%d", result, remote.createdIn(11))
	}
	var mapping model.VikunjaSyncItem
	if err := fixture.db.Where("user_uid = ? AND instance_url = ? AND project_id = ? AND item_key = ?", 42, remote.server.URL, 11, "homework:one").First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if mapping.VikunjaTaskID == 404 {
		t.Fatal("stale task ID was not replaced")
	}
	if mapping.DueDate != items[0].EndTime {
		t.Fatalf("recreated mapping lost the deadline: got %d want %d", mapping.DueDate, items[0].EndTime)
	}
	recreated := remote.task(t, mapping.VikunjaTaskID)
	due, _ := json.Marshal(time.UnixMilli(items[0].EndTime).Format(time.RFC3339))
	requireVikunjaJSON(t, recreated["due_date"], due)
}
