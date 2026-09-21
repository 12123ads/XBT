package middleware_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/common"
	"xbt2/server/internal/handler"
	"xbt2/server/internal/middleware"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/testutil"
)

func TestAuthReloadsPermissionsAndHonorsWhitelistDeletion(t *testing.T) {
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.Whitelist{}); err != nil {
		t.Fatal(err)
	}
	users := []model.User{
		{UID: 1, Mobile: "13800000001", Name: "demoted later", CredentialCipher: "unused", Permission: 2},
		{UID: 2, Mobile: "13800000002", Name: "admin", CredentialCipher: "unused", Permission: 2},
	}
	whitelists := []model.Whitelist{{Mobile: users[0].Mobile, Permission: 2}, {Mobile: users[1].Mobile, Permission: 2}}
	if err := database.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&whitelists).Error; err != nil {
		t.Fatal(err)
	}
	jwtSvc := service.NewJWTService("middleware-test-jwt")
	oldToken, err := jwtSvc.Sign(1, "obsolete-token-mobile", 2)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := jwtSvc.Sign(2, users[1].Mobile, 2)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	authed := router.Group("", middleware.Auth(jwtSvc, database))
	authed.GET("/me", func(c *gin.Context) {
		common.Success(c, gin.H{"uid": common.GetUserUID(c), "mobile": c.GetString(common.CtxMobile), "permission": common.GetPermission(c)})
	})
	admin := authed.Group("/admin", middleware.AdminOnly())
	admin.GET("/ping", func(c *gin.Context) { common.Success(c, nil) })
	admin.DELETE("/users/:id", handler.NewWhitelistHandler(database).DeleteUser)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	assertMe := func(permission int) {
		t.Helper()
		response := request(http.MethodGet, "/me", oldToken)
		var payload struct {
			Data struct {
				UID        int64  `json:"uid"`
				Mobile     string `json:"mobile"`
				Permission int    `json:"permission"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || payload.Data.UID != 1 || payload.Data.Mobile != users[0].Mobile || payload.Data.Permission != permission {
			t.Fatalf("request trusted old token claims: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	assertMe(2)
	if err := database.Model(&model.Whitelist{}).Where("id = ?", whitelists[0].ID).Update("permission", 1).Error; err != nil {
		t.Fatal(err)
	}
	assertMe(1)
	if response := request(http.MethodGet, "/admin/ping", oldToken); response.Code != http.StatusForbidden {
		t.Fatalf("old admin token retained admin permission: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, fmt.Sprintf("/admin/users/%d", whitelists[0].ID), adminToken); response.Code != http.StatusOK {
		t.Fatalf("whitelist revocation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/me", oldToken); response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted whitelist account reused old token: status=%d body=%s", response.Code, response.Body.String())
	}
	var revoked model.User
	if err := database.Where("uid = ?", 1).Take(&revoked).Error; err != nil || revoked.Permission != 0 {
		t.Fatalf("revocation did not update stored user atomically: user=%+v err=%v", revoked, err)
	}
	if err := database.Exec("ALTER TABLE users RENAME TO unavailable_users").Error; err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, "/me", adminToken); response.Code != http.StatusInternalServerError {
		t.Fatalf("database outage masqueraded as invalid token: status=%d body=%s", response.Code, response.Body.String())
	}
}
