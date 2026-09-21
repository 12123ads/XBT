package service

import (
	"errors"
	"testing"

	"xbt2/server/internal/model"
	"xbt2/server/internal/qmx"
)

type qmxAuthorizationCookie struct{ calls int }

func (c *qmxAuthorizationCookie) CookieHeader(string, string, string) (string, error) {
	c.calls++
	return "synthetic-cookie", nil
}

type qmxAuthorizationClient struct{ calls int }

func (c *qmxAuthorizationClient) Preview(qmx.CredentialInput) (qmx.Preview, error) {
	panic("unexpected preview request")
}

func (c *qmxAuthorizationClient) Execute(qmx.ExecuteInput) (qmx.ExecuteResult, error) {
	c.calls++
	return qmx.ExecuteResult{Success: true, Message: "success"}, nil
}

func TestQMXManualRunIgnoresAutoSwitchButNotAccountRevocation(t *testing.T) {
	database := newAuthorizationTestDB(t)
	if err := database.AutoMigrate(&model.QMXAutoSignAccount{}, &model.QMXAutoSignRecord{}); err != nil {
		t.Fatal(err)
	}
	cc := NewCredentialCrypto("qmx-access-test")
	user := seedAuthorizationUser(t, database, cc, 1)
	account := model.QMXAutoSignAccount{UserUID: user.UID, Enabled: false, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	cookies := &qmxAuthorizationCookie{}
	upstream := &qmxAuthorizationClient{}
	svc := NewQMXAutoSignService(database, nil, nil, cc, nil, nil)
	svc.xxt = cookies
	svc.client = upstream
	if _, err := svc.RunAccount(user.UID, QMXAutoSignTriggerScheduled); err == nil || cookies.calls != 0 || upstream.calls != 0 {
		t.Fatalf("automatic execution ignored its switch: cookies=%d execute=%d err=%v", cookies.calls, upstream.calls, err)
	}
	record, err := svc.RunSavedAccount(user.UID, QMXAutoSignTriggerManual)
	if err != nil || !record.Success || record.ID == 0 || cookies.calls != 1 || upstream.calls != 1 {
		t.Fatalf("active account cannot run manually with automation disabled: record=%+v err=%v", record, err)
	}
	if err := database.Model(&model.Whitelist{}).Where("mobile = ?", user.Mobile).Update("permission", 0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunSavedAccount(user.UID, QMXAutoSignTriggerManual); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("manual execution bypassed current whitelist: %v", err)
	}
	if err := database.Model(&model.QMXAutoSignAccount{}).Where("user_uid = ?", user.UID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.runAccount(user.UID, QMXAutoSignTriggerScheduled, true, "scheduled-revoked-test"); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("new scheduled account item bypassed revocation: %v", err)
	}
	if _, err := svc.PreviewLocations(user.UID); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("preview bypassed revoked account: %v", err)
	}
	if cookies.calls != 1 || upstream.calls != 1 {
		t.Fatalf("revoked account reached credential or QMX upstream: cookies=%d execute=%d", cookies.calls, upstream.calls)
	}
}
