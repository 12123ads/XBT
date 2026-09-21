package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/testutil"
	"xbt2/server/internal/xxt"
)

type authLoginFunc func(mobile, password string) (*xxt.LoginResult, error)

func (f authLoginFunc) PreLogin(mobile, password string) (*xxt.LoginResult, error) {
	return f(mobile, password)
}

func newHandlerAccessDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.Whitelist{}, &model.UserCourse{}, &model.SignActivityScope{}, &model.SignRecord{}, &model.SignShare{}, &model.ClassGroup{}, &model.ClassGroupMember{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func accessRequest(t *testing.T, router http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func loginRouter(database *gorm.DB, upstream loginClient) *gin.Engine {
	h := NewAuthHandler(database, service.NewJWTService("auth-test-jwt"), service.NewCredentialCrypto("auth-test-credential"), upstream)
	router := gin.New()
	router.POST("/login", h.Login)
	return router
}

func assertLoginRows(t *testing.T, database *gorm.DB, users, whitelists int64) {
	t.Helper()
	var userCount, whitelistCount int64
	if err := database.Model(&model.User{}).Count(&userCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.Whitelist{}).Count(&whitelistCount).Error; err != nil {
		t.Fatal(err)
	}
	if userCount != users || whitelistCount != whitelists {
		t.Fatalf("login rows: users=%d whitelists=%d, want %d/%d", userCount, whitelistCount, users, whitelists)
	}
}

func TestFailedLoginNeverClaimsBootstrapAdmin(t *testing.T) {
	database := newHandlerAccessDB(t)
	upstream := authLoginFunc(func(mobile, password string) (*xxt.LoginResult, error) {
		if password != "correct" {
			return nil, errors.New("bad password")
		}
		return &xxt.LoginResult{UID: 102, Name: "first successful user"}, nil
	})
	router := loginRouter(database, upstream)
	failed := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000001", "password": "wrong"})
	if failed.Code != http.StatusUnauthorized {
		t.Fatalf("failed login: status=%d body=%s", failed.Code, failed.Body.String())
	}
	assertLoginRows(t, database, 0, 0)
	success := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000002", "password": "correct"})
	if success.Code != http.StatusOK {
		t.Fatalf("subsequent valid bootstrap failed: status=%d body=%s", success.Code, success.Body.String())
	}
	assertLoginRows(t, database, 1, 1)
	active, err := service.LoadActiveUser(database, 102)
	if err != nil || active.Mobile != "13800000002" || active.Permission != 2 {
		t.Fatalf("successful login did not atomically establish admin: user=%+v err=%v", active, err)
	}
}

func TestLoginRollsBackBootstrapWhenUserSaveFails(t *testing.T) {
	database := newHandlerAccessDB(t)
	if err := database.Exec("ALTER TABLE users ADD CONSTRAINT reject_test_uid CHECK (uid <> 101)").Error; err != nil {
		t.Fatal(err)
	}
	upstream := authLoginFunc(func(mobile, _ string) (*xxt.LoginResult, error) {
		uid := int64(101)
		if mobile == "13800000002" {
			uid = 102
		}
		return &xxt.LoginResult{UID: uid, Name: "test user"}, nil
	})
	router := loginRouter(database, upstream)
	failed := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000001", "password": "correct"})
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("user-save failure: status=%d body=%s", failed.Code, failed.Body.String())
	}
	assertLoginRows(t, database, 0, 0)
	if got := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000002", "password": "correct"}); got.Code != http.StatusOK {
		t.Fatalf("rolled-back bootstrap blocked next account: status=%d body=%s", got.Code, got.Body.String())
	}
	assertLoginRows(t, database, 1, 1)
}

func TestLoginDoesNotReturnSuccessBeforeCommit(t *testing.T) {
	database := newHandlerAccessDB(t)
	if err := database.Exec(`CREATE FUNCTION reject_login_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced login commit failure'; END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE CONSTRAINT TRIGGER reject_login_commit AFTER INSERT OR UPDATE ON users DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_login_commit()`).Error; err != nil {
		t.Fatal(err)
	}
	router := loginRouter(database, authLoginFunc(func(string, string) (*xxt.LoginResult, error) {
		return &xxt.LoginResult{UID: 101, Name: "test user"}, nil
	}))
	got := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000001", "password": "correct"})
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("failed commit must not return a token: status=%d body=%s", got.Code, got.Body.String())
	}
	assertLoginRows(t, database, 0, 0)
}

func TestConcurrentFirstLoginsSerializeBootstrap(t *testing.T) {
	for _, sameMobile := range []bool{false, true} {
		name := "different accounts"
		if sameMobile {
			name = "same account"
		}
		t.Run(name, func(t *testing.T) {
			database := newHandlerAccessDB(t)
			arrived := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			router := loginRouter(database, authLoginFunc(func(mobile, _ string) (*xxt.LoginResult, error) {
				arrived <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				uid := int64(101)
				if mobile == "13800000002" {
					uid = 102
				}
				return &xxt.LoginResult{UID: uid, Name: "test user"}, nil
			}))
			mobiles := []string{"13800000001", "13800000002"}
			if sameMobile {
				mobiles[1] = mobiles[0]
			}
			responses := make(chan *httptest.ResponseRecorder, 2)
			for _, mobile := range mobiles {
				body, err := json.Marshal(gin.H{"mobile": mobile, "password": "correct"})
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body)).WithContext(ctx)
					req.Header.Set("Content-Type", "application/json")
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, req)
					responses <- recorder
				}()
			}
			for range 2 {
				select {
				case <-arrived:
				case <-ctx.Done():
					t.Fatal("network login was serialized or bootstrap was claimed before authentication")
				}
			}
			releaseOnce.Do(func() { close(release) })
			codes := make([]int, 0, 2)
			for range 2 {
				select {
				case response := <-responses:
					codes = append(codes, response.Code)
				case <-ctx.Done():
					t.Fatal("concurrent login did not finish")
				}
			}
			sort.Ints(codes)
			second := http.StatusForbidden
			if sameMobile {
				second = http.StatusOK
			}
			if codes[0] != http.StatusOK || codes[1] != second {
				t.Fatalf("unexpected concurrent login statuses: %v", codes)
			}
			assertLoginRows(t, database, 1, 1)
			var user model.User
			if err := database.Take(&user).Error; err != nil {
				t.Fatal(err)
			}
			active, err := service.LoadActiveUser(database, user.UID)
			if err != nil || active.Permission != 2 {
				t.Fatalf("winner is not the sole active admin: user=%+v err=%v", active, err)
			}
		})
	}
}

func TestWhitelistedLoginRetainsGrantedPermission(t *testing.T) {
	database := newHandlerAccessDB(t)
	if err := database.Create(&model.Whitelist{Mobile: "13800000001", Permission: 1}).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	router := loginRouter(database, authLoginFunc(func(string, string) (*xxt.LoginResult, error) {
		calls++
		return &xxt.LoginResult{UID: 101, Name: "ordinary user"}, nil
	}))
	if got := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000002", "password": "correct"}); got.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("unlisted account reached upstream: status=%d calls=%d", got.Code, calls)
	}
	if got := accessRequest(t, router, http.MethodPost, "/login", gin.H{"mobile": "13800000001", "password": "correct"}); got.Code != http.StatusOK {
		t.Fatalf("whitelisted login failed: status=%d body=%s", got.Code, got.Body.String())
	}
	user, err := service.LoadActiveUser(database, 101)
	if err != nil || user.Permission != 1 {
		t.Fatalf("ordinary whitelist was promoted: user=%+v err=%v", user, err)
	}
}
