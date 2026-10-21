package service

import (
	"context"
	"errors"
	"sync"
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

type qmxTimedClient struct {
	mu       sync.Mutex
	calls    int
	executed time.Time
}

func (c *qmxTimedClient) Preview(qmx.CredentialInput) (qmx.Preview, error) {
	panic("unexpected preview request")
}

func (c *qmxTimedClient) Execute(qmx.ExecuteInput) (qmx.ExecuteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.executed = time.Now()
	return qmx.ExecuteResult{Success: true, Message: "success"}, nil
}

func (c *qmxTimedClient) executedAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.executed
}

func TestQMXJitterSecondsDeterministic(t *testing.T) {
	const (
		uid  = int64(42)
		date = "2026-10-21"
	)
	got := jitterSeconds(uid, date)
	if got != 35 {
		t.Fatalf("jitterSeconds(%d, %s) = %d, want 35", uid, date, got)
	}
	if got < 0 || got >= qmxAutoSignJitterSeconds {
		t.Fatalf("jitter %d outside [0, %d)", got, qmxAutoSignJitterSeconds)
	}
	if again := jitterSeconds(uid, date); again != got {
		t.Fatalf("same uid+date must be stable: %d != %d", again, got)
	}
	if other := jitterSeconds(43, date); other == got {
		t.Fatalf("different uids must differ: both %d", got)
	}
	if other := jitterSeconds(uid, "2026-10-22"); other == got {
		t.Fatalf("different dates must differ: both %d", got)
	}
}

func TestQMXNextRunAtForUsesDeterministicJitter(t *testing.T) {
	svc := NewQMXAutoSignService(nil, nil, nil, nil, nil, nil)
	const uid = int64(7)
	base := qmxWindowTime(22, 0, 0)
	target := base.Add(time.Duration(jitterSeconds(uid, "2026-10-21")) * time.Second)
	nextTarget := base.AddDate(0, 0, 1).Add(time.Duration(jitterSeconds(uid, "2026-10-22")) * time.Second)

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{name: "before batch", now: qmxWindowTime(21, 59, 0), want: target},
		{name: "before account instant", now: target.Add(-time.Second), want: target},
		{name: "at account instant", now: target, want: nextTarget},
		{name: "after account instant", now: target.Add(time.Second), want: nextTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := svc.NextRunAtFor(uid, tc.now); !got.Equal(tc.want) {
				t.Fatalf("NextRunAtFor(%d, %s) = %s, want %s", uid, tc.now, got, tc.want)
			}
		})
	}

	svc.SetJitterFunc(func(int64, string) int { return 0 })
	if got := svc.NextRunAtFor(1, qmxWindowTime(21, 59, 59)); !got.Equal(base) {
		t.Fatalf("zero jitter before batch = %s, want %s", got, base)
	}
	if got := svc.NextRunAtFor(1, base); !got.Equal(base.AddDate(0, 0, 1)) {
		t.Fatalf("zero jitter at batch base = %s, want %s", got, base.AddDate(0, 0, 1))
	}
}

func TestQMXNextRunWindowAggregatesEnabledAccounts(t *testing.T) {
	database := newAuthorizationTestDB(t)
	if err := database.AutoMigrate(&model.QMXAutoSignAccount{}); err != nil {
		t.Fatal(err)
	}
	accounts := []model.QMXAutoSignAccount{
		{UserUID: 42, Enabled: true, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30},
		{UserUID: 43, Enabled: true, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30},
		{UserUID: 44, Enabled: false, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30},
		{UserUID: 45, Enabled: true, LocationIndex: -1},
	}
	for i := range accounts {
		if err := database.Create(&accounts[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := NewQMXAutoSignService(database, nil, nil, nil, nil, nil)
	now := qmxWindowTime(21, 0, 0)

	start, end, err := svc.NextRunWindow(now)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := svc.NextRunAtFor(42, now)
	wantEnd := svc.NextRunAtFor(43, now)
	if wantEnd.Before(wantStart) {
		wantStart, wantEnd = wantEnd, wantStart
	}
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("window = %s..%s, want %s..%s", start, end, wantStart, wantEnd)
	}
	if !end.After(start) {
		t.Fatalf("enabled accounts must spread the window: %s..%s", start, end)
	}
}

func TestQMXNextRunWindowWithoutEnabledAccounts(t *testing.T) {
	database := newAuthorizationTestDB(t)
	if err := database.AutoMigrate(&model.QMXAutoSignAccount{}); err != nil {
		t.Fatal(err)
	}
	svc := NewQMXAutoSignService(database, nil, nil, nil, nil, nil)
	now := qmxWindowTime(21, 0, 0)

	start, end, err := svc.NextRunWindow(now)
	if err != nil {
		t.Fatal(err)
	}
	base := svc.NextRunAt(now)
	if !start.Equal(base) || !end.Equal(base) {
		t.Fatalf("empty window = %s..%s, want %s", start, end, base)
	}
}

func TestQMXScheduledRunHonorsScheduledInstant(t *testing.T) {
	database := newAuthorizationTestDB(t)
	if err := database.AutoMigrate(&model.QMXAutoSignSetting{}, &model.QMXAutoSignAccount{}, &model.QMXAutoSignRecord{}); err != nil {
		t.Fatal(err)
	}
	cc := NewCredentialCrypto("qmx-schedule-test")
	user := seedAuthorizationUser(t, database, cc, 1)
	if err := database.Create(&model.QMXAutoSignSetting{ID: 1, Enabled: true, Timezone: qmxAutoSignTimezone, RunAt: qmxAutoSignRunAt}).Error; err != nil {
		t.Fatal(err)
	}
	account := model.QMXAutoSignAccount{UserUID: user.UID, Enabled: true, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	cookies := &qmxAuthorizationCookie{}
	upstream := &qmxTimedClient{}
	svc := NewQMXAutoSignService(database, nil, nil, cc, nil, nil)
	svc.xxt = cookies
	svc.client = upstream
	svc.SetJitterFunc(func(int64, string) int { return 0 })

	batchAt := time.Now().Add(time.Second)
	svc.SetNowFunc(func() time.Time { return batchAt.Add(-600 * time.Millisecond) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.runScheduled(ctx, batchAt); err != nil {
		t.Fatal(err)
	}
	if upstream.calls != 1 || cookies.calls != 1 {
		t.Fatalf("scheduled run did not execute once: cookies=%d execute=%d", cookies.calls, upstream.calls)
	}
	if delta := upstream.executedAt().Sub(batchAt); delta < -time.Second || delta > time.Second {
		t.Fatalf("execution at %s, scheduled at %s (delta %v)", upstream.executedAt(), batchAt, delta)
	}
	var records int64
	if err := database.Model(&model.QMXAutoSignRecord{}).Where("run_id = ?", svc.scheduledQMXAutoSignRunID(batchAt)).Count(&records).Error; err != nil {
		t.Fatal(err)
	}
	if records != 1 {
		t.Fatalf("scheduled records = %d, want 1", records)
	}
}
