package service

import (
	"errors"
	"testing"
	"time"

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

func qmxWindowTime(hour, minute, second int) time.Time {
	return time.Date(2026, 10, 21, hour, minute, second, 0, time.FixedZone("CST", 8*60*60))
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
	svc.SetNowFunc(func() time.Time { return qmxWindowTime(22, 10, 0) })
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

func TestQMXManualWindow(t *testing.T) {
	cases := []struct {
		name    string
		trigger string
		now     time.Time
		blocked bool
	}{
		{name: "manual before window", trigger: QMXAutoSignTriggerManual, now: qmxWindowTime(21, 59, 59), blocked: true},
		{name: "manual at window start", trigger: QMXAutoSignTriggerManual, now: qmxWindowTime(22, 0, 0)},
		{name: "manual at window end minus second", trigger: QMXAutoSignTriggerManual, now: qmxWindowTime(22, 29, 59)},
		{name: "manual at window end", trigger: QMXAutoSignTriggerManual, now: qmxWindowTime(22, 30, 0), blocked: true},
		{name: "scheduled at 22:00", trigger: QMXAutoSignTriggerScheduled, now: qmxWindowTime(22, 0, 0)},
		{name: "scheduled at 23:00", trigger: QMXAutoSignTriggerScheduled, now: qmxWindowTime(23, 0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database := newAuthorizationTestDB(t)
			if err := database.AutoMigrate(&model.QMXAutoSignAccount{}, &model.QMXAutoSignRecord{}); err != nil {
				t.Fatal(err)
			}
			cc := NewCredentialCrypto("qmx-window-test")
			user := seedAuthorizationUser(t, database, cc, 1)
			account := model.QMXAutoSignAccount{UserUID: user.UID, Enabled: true, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30}
			if err := database.Create(&account).Error; err != nil {
				t.Fatal(err)
			}
			cookies := &qmxAuthorizationCookie{}
			upstream := &qmxAuthorizationClient{}
			svc := NewQMXAutoSignService(database, nil, nil, cc, nil, nil)
			svc.xxt = cookies
			svc.client = upstream
			svc.SetNowFunc(func() time.Time { return tc.now })

			var record model.QMXAutoSignRecord
			var err error
			if tc.trigger == QMXAutoSignTriggerScheduled {
				record, err = svc.RunAccount(user.UID, tc.trigger)
			} else {
				record, err = svc.RunSavedAccount(user.UID, tc.trigger)
			}

			var saved int64
			if countErr := database.Model(&model.QMXAutoSignRecord{}).Count(&saved).Error; countErr != nil {
				t.Fatal(countErr)
			}
			if tc.blocked {
				if !errors.Is(err, ErrQMXManualWindowClosed) || record.ID != 0 {
					t.Fatalf("outside window must be rejected without a record: record=%+v err=%v", record, err)
				}
				if cookies.calls != 0 || upstream.calls != 0 || saved != 0 {
					t.Fatalf("outside window reached QMX or wrote a record: cookies=%d execute=%d records=%d", cookies.calls, upstream.calls, saved)
				}
				return
			}
			if err != nil || record.ID == 0 || !record.Success || record.Trigger != tc.trigger {
				t.Fatalf("run must succeed inside window/scheduled path: record=%+v err=%v", record, err)
			}
			if cookies.calls != 1 || upstream.calls != 1 || saved != 1 {
				t.Fatalf("expected one QMX execution: cookies=%d execute=%d records=%d", cookies.calls, upstream.calls, saved)
			}
		})
	}
}
