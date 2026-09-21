package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/model"
)

func TestWhitelistCreateRollsBackWhenUserUpdateFails(t *testing.T) {
	database := newHandlerAccessDB(t)
	user := model.User{UID: 1, Mobile: "13800000001", Name: "user", CredentialCipher: "unused", Permission: 2}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("ALTER TABLE users ADD CONSTRAINT reject_permission_one CHECK (permission <> 1)").Error; err != nil {
		t.Fatal(err)
	}
	h := NewWhitelistHandler(database)
	router := gin.New()
	router.POST("/users", h.CreateUser)
	got := accessRequest(t, router, http.MethodPost, "/users", gin.H{"mobile": user.Mobile})
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("user-update failure reported success: status=%d body=%s", got.Code, got.Body.String())
	}
	assertLoginRows(t, database, 1, 0)
	var saved model.User
	if err := database.Where("uid = ?", user.UID).Take(&saved).Error; err != nil || saved.Permission != 2 {
		t.Fatalf("failed whitelist upsert changed user permission: user=%+v err=%v", saved, err)
	}
}

func TestWhitelistBatchRollsBackEarlierUsersOnFailure(t *testing.T) {
	database := newHandlerAccessDB(t)
	users := []model.User{
		{UID: 1, Mobile: "13800000001", Name: "first", CredentialCipher: "unused", Permission: 2},
		{UID: 2, Mobile: "13800000002", Name: "second", CredentialCipher: "unused", Permission: 2},
	}
	if err := database.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("ALTER TABLE users ADD CONSTRAINT reject_second_permission CHECK (uid <> 2 OR permission <> 1)").Error; err != nil {
		t.Fatal(err)
	}
	h := NewWhitelistHandler(database)
	router := gin.New()
	router.POST("/batch", h.BatchImportUsers)
	got := accessRequest(t, router, http.MethodPost, "/batch", gin.H{"mobiles": "13800000002\n13800000001"})
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("batch failure counted as success: status=%d body=%s", got.Code, got.Body.String())
	}
	assertLoginRows(t, database, 2, 0)
	var changed int64
	if err := database.Model(&model.User{}).Where("permission <> 2").Count(&changed).Error; err != nil || changed != 0 {
		t.Fatalf("batch retained partial permission changes: changed=%d err=%v", changed, err)
	}
}

func TestWhitelistDeleteRollsBackRemovalWhenRevocationFails(t *testing.T) {
	database := newHandlerAccessDB(t)
	user := model.User{UID: 1, Mobile: "13800000001", Name: "user", CredentialCipher: "unused", Permission: 1}
	wl := model.Whitelist{Mobile: user.Mobile, Permission: 1}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&wl).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("ALTER TABLE users ADD CONSTRAINT reject_test_revocation CHECK (permission > 0)").Error; err != nil {
		t.Fatal(err)
	}
	h := NewWhitelistHandler(database)
	router := gin.New()
	router.DELETE("/users/:id", h.DeleteUser)
	got := accessRequest(t, router, http.MethodDelete, fmt.Sprintf("/users/%d", wl.ID), nil)
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("failed revocation was reported as deletion: status=%d body=%s", got.Code, got.Body.String())
	}
	assertLoginRows(t, database, 1, 1)
	var saved model.Whitelist
	if err := database.First(&saved, wl.ID).Error; err != nil || saved.Permission != 1 {
		t.Fatalf("failed revocation removed authorization row: row=%+v err=%v", saved, err)
	}
}

func TestWhitelistMutationsPreserveAdministrators(t *testing.T) {
	database := newHandlerAccessDB(t)
	user := model.User{UID: 1, Mobile: "13800000001", Name: "admin", CredentialCipher: "unused", Permission: 2}
	admin := model.Whitelist{Mobile: user.Mobile, Permission: 2}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	h := NewWhitelistHandler(database)
	router := gin.New()
	router.POST("/users", h.CreateUser)
	router.POST("/batch", h.BatchImportUsers)
	router.DELETE("/users/:id", h.DeleteUser)
	if got := accessRequest(t, router, http.MethodPost, "/users", gin.H{"mobile": user.Mobile}); got.Code != http.StatusBadRequest {
		t.Fatalf("admin downgrade was allowed: status=%d", got.Code)
	}
	if got := accessRequest(t, router, http.MethodDelete, fmt.Sprintf("/users/%d", admin.ID), nil); got.Code != http.StatusBadRequest {
		t.Fatalf("admin removal was allowed: status=%d", got.Code)
	}
	if got := accessRequest(t, router, http.MethodPost, "/batch", gin.H{"mobiles": "13800000001,13800000002,13800000002"}); got.Code != http.StatusOK {
		t.Fatalf("ordinary import should skip admin and dedupe users: status=%d body=%s", got.Code, got.Body.String())
	}
	assertLoginRows(t, database, 1, 2)
	var savedAdmin model.Whitelist
	if err := database.First(&savedAdmin, admin.ID).Error; err != nil || savedAdmin.Permission != 2 {
		t.Fatalf("batch changed administrator authorization: row=%+v err=%v", savedAdmin, err)
	}
	var savedUser model.User
	if err := database.Where("uid = ?", user.UID).Take(&savedUser).Error; err != nil || savedUser.Permission != 2 {
		t.Fatalf("batch changed administrator user permission: user=%+v err=%v", savedUser, err)
	}
}
