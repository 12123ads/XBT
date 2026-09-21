package service

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/testutil"
)

func newAuthorizationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.Whitelist{}, &model.UserCourse{}, &model.SignActivityScope{}, &model.SignRecord{}, &model.ClassGroup{}, &model.ClassGroupMember{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func seedAuthorizationUser(t *testing.T, database *gorm.DB, cc *CredentialCrypto, uid int64) model.User {
	t.Helper()
	cipher, err := cc.Encrypt("password")
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{UID: uid, Mobile: fmt.Sprintf("138%08d", uid), Name: fmt.Sprintf("user-%d", uid), CredentialCipher: cipher, Permission: 1}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.Whitelist{Mobile: user.Mobile, Permission: 1}).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func TestLoadActiveUserRequiresBothCurrentPermissions(t *testing.T) {
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("access-test")
	user := seedAuthorizationUser(t, database, cc, 1)
	if err := database.Model(&model.User{}).Where("uid = ?", user.UID).Update("permission", 2).Error; err != nil {
		t.Fatal(err)
	}
	active, err := LoadActiveUser(database, user.UID)
	if err != nil || active.Permission != 1 || active.Mobile != user.Mobile {
		t.Fatalf("whitelist must cap user permission: user=%+v err=%v", active, err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", user.UID).Update("permission", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.Whitelist{}).Where("mobile = ?", user.Mobile).Update("permission", 2).Error; err != nil {
		t.Fatal(err)
	}
	active, err = LoadActiveUser(database, user.UID)
	if err != nil || active.Permission != 1 {
		t.Fatalf("user must cap whitelist permission: user=%+v err=%v", active, err)
	}
	for _, table := range []string{"users", "whitelists"} {
		t.Run(table+" revocation", func(t *testing.T) {
			if err := database.Table(table).Where("mobile = ?", user.Mobile).Update("permission", 0).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := LoadActiveUser(database, user.UID); !errors.Is(err, ErrAccountInactive) {
				t.Fatalf("revoked account error = %v", err)
			}
			if err := database.Table(table).Where("mobile = ?", user.Mobile).Update("permission", 1).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := database.Where("mobile = ?", user.Mobile).Delete(&model.Whitelist{}).Error; err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{user.UID, 999, 0, -1} {
		if _, err := LoadActiveUser(database, uid); !errors.Is(err, ErrAccountInactive) {
			t.Fatalf("inactive uid %d error = %v", uid, err)
		}
	}
}

func TestLoadActiveUserPreservesDatabaseErrors(t *testing.T) {
	database := newAuthorizationTestDB(t)
	seedAuthorizationUser(t, database, NewCredentialCrypto("access-test"), 1)
	if err := database.Exec("ALTER TABLE whitelists RENAME TO unavailable_whitelists").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadActiveUser(database, 1); err == nil || errors.Is(err, ErrAccountInactive) {
		t.Fatalf("database fault must not become account revocation: %v", err)
	}
}
