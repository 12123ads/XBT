package service

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"xbt2/server/internal/im"
	"xbt2/server/internal/model"
	"xbt2/server/internal/testutil"
	"xbt2/server/internal/xxt"
)

const imTestEncryptedPassword = "0bd654e244170185155ddeefe47a8007" // "secret-im-pass"

type fakeIMAccountProvider struct {
	mu      sync.Mutex
	account xxt.IMAccount
	err     error
	calls   int
}

func (f *fakeIMAccountProvider) GetIMAccountContext(context.Context, string, string) (xxt.IMAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.account, f.err
}

type fakeIMClient struct {
	mu            sync.Mutex
	tokenCalls    int
	discoverCalls int
	tokenErr      error
	discover      func(ctx context.Context, token im.Token) ([]im.Group, []im.SignActivity, error)
	passwordSeen  string
}

func (f *fakeIMClient) Token(_ context.Context, _ int64, password string) (im.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenCalls++
	f.passwordSeen = password
	if f.tokenErr != nil {
		return im.Token{}, f.tokenErr
	}
	return im.Token{
		AccessToken: fmt.Sprintf("token-%d", f.tokenCalls),
		Username:    "im-user",
		ExpiresAt:   time.Now().Add(time.Hour),
	}, nil
}

func (f *fakeIMClient) Discover(ctx context.Context, token im.Token) ([]im.Group, []im.SignActivity, error) {
	f.mu.Lock()
	f.discoverCalls++
	discover := f.discover
	f.mu.Unlock()
	if discover != nil {
		return discover(ctx, token)
	}
	return []im.Group{{ID: "group-1", Name: "课程群"}}, nil, nil
}

func (f *fakeIMClient) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls, f.discoverCalls
}

func imServiceFixture(t *testing.T) (*gorm.DB, *CredentialCrypto, int64) {
	t.Helper()
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.Whitelist{}, &model.UserCourse{}); err != nil {
		t.Fatal(err)
	}
	cc := NewCredentialCrypto("im-service-test")
	cipher, err := cc.Encrypt("chaoxing-password")
	if err != nil {
		t.Fatal(err)
	}
	uid := int64(101)
	user := model.User{
		UID:              uid,
		Mobile:           "13800000101",
		Name:             "测试用户",
		CredentialCipher: cipher,
		Permission:       1,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.Whitelist{Mobile: user.Mobile, Permission: 1}).Error; err != nil {
		t.Fatal(err)
	}
	return database, cc, uid
}

func newTestIMService(t *testing.T, provider IMAccountProvider, client IMClient) (*IMSignService, *CredentialCrypto, int64) {
	t.Helper()
	database, cc, uid := imServiceFixture(t)
	return NewIMSignService(database, provider, client, cc, 30*time.Second, 5*time.Second, true), cc, uid
}

func TestIMFetchDiscoversActivities(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{UID: 101, Password: imTestEncryptedPassword}}
	client := &fakeIMClient{discover: func(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
		return []im.Group{{ID: "g1"}, {ID: "g2"}}, []im.SignActivity{
			{ActiveID: 100, CourseID: 10, ClassID: 20, AType: 2},
			{ActiveID: 101, CourseID: 10, ClassID: 20, AType: 74},
		}, nil
	}}
	svc, _, uid := newTestIMService(t, provider, client)
	snapshot := svc.RefreshIfStale(context.Background(), uid)
	if snapshot.ErrorCode != "" || snapshot.GroupCount != 2 || snapshot.ActivityCount != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if client.passwordSeen != "secret-im-pass" {
		t.Fatalf("decrypted password not passed to token: %q", client.passwordSeen)
	}
	if snapshot.LastSuccessAt.IsZero() || snapshot.LastPollAt.IsZero() {
		t.Fatal("poll timestamps were not set")
	}
	if len(snapshot.Activities) != 2 || snapshot.Activities[0].ActiveID != 100 {
		t.Fatalf("unexpected activities: %+v", snapshot.Activities)
	}
}

func TestIMFetchRefreshesTokenOnceAfterUnauthorized(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	attempts := 0
	client := &fakeIMClient{discover: func(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
		attempts++
		if attempts == 1 {
			return nil, nil, &im.Error{Code: im.CodeUnauthorized, Status: 401}
		}
		return []im.Group{{ID: "g1"}}, []im.SignActivity{{ActiveID: 7, CourseID: 1, ClassID: 2, AType: 2}}, nil
	}}
	svc, _, uid := newTestIMService(t, provider, client)
	snapshot := svc.RefreshIfStale(context.Background(), uid)
	if snapshot.ErrorCode != "" || snapshot.ActivityCount != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	tokenCalls, discoverCalls := client.counts()
	if tokenCalls != 2 || discoverCalls != 2 {
		t.Fatalf("tokenCalls=%d discoverCalls=%d, want 2/2", tokenCalls, discoverCalls)
	}
	if snapshot.Activities[0].ActiveID != 7 {
		t.Fatalf("unexpected activities: %+v", snapshot.Activities)
	}
}

func TestIMFetchStopsAfterSecondUnauthorized(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	client := &fakeIMClient{discover: func(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
		return nil, nil, &im.Error{Code: im.CodeUnauthorized, Status: 401}
	}}
	svc, _, uid := newTestIMService(t, provider, client)
	snapshot := svc.RefreshIfStale(context.Background(), uid)
	if snapshot.ErrorCode != im.CodeUnauthorized {
		t.Fatalf("error_code=%q, want %q", snapshot.ErrorCode, im.CodeUnauthorized)
	}
	tokenCalls, discoverCalls := client.counts()
	if tokenCalls != 2 || discoverCalls != 2 {
		t.Fatalf("tokenCalls=%d discoverCalls=%d, want 2/2", tokenCalls, discoverCalls)
	}
}

func TestIMFetchMapsCredentialFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider *fakeIMAccountProvider
		want     string
	}{
		{
			name:     "decrypt failure",
			provider: &fakeIMAccountProvider{account: xxt.IMAccount{Password: "not-hex"}},
			want:     im.CodeAuthFailed,
		},
		{
			name:     "missing account",
			provider: &fakeIMAccountProvider{err: xxt.ErrIMAccountMissing},
			want:     im.CodeNotConfigured,
		},
		{
			name:     "userinfo failure",
			provider: &fakeIMAccountProvider{err: fmt.Errorf("upstream unavailable")},
			want:     im.CodeAuthFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeIMClient{}
			svc, _, uid := newTestIMService(t, tc.provider, client)
			snapshot := svc.RefreshIfStale(context.Background(), uid)
			if snapshot.ErrorCode != tc.want {
				t.Fatalf("error_code=%q, want %q", snapshot.ErrorCode, tc.want)
			}
			if tokenCalls, _ := client.counts(); tokenCalls != 0 {
				t.Fatalf("token endpoint called %d times after credential failure", tokenCalls)
			}
		})
	}
}

func TestIMFetchRetainsLastSuccessOnFailure(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	fail := false
	client := &fakeIMClient{discover: func(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
		if fail {
			return nil, nil, &im.Error{Code: im.CodeNetwork}
		}
		return []im.Group{{ID: "g1"}}, []im.SignActivity{{ActiveID: 55, CourseID: 1, ClassID: 2, AType: 2}}, nil
	}}
	svc, _, uid := newTestIMService(t, provider, client)
	if snapshot := svc.RefreshIfStale(context.Background(), uid); snapshot.ErrorCode != "" || snapshot.ActivityCount != 1 {
		t.Fatalf("initial poll: %+v", snapshot)
	}
	fail = true
	snapshot := svc.refreshAccount(context.Background(), uid, true)
	if snapshot.ErrorCode != im.CodeNetwork {
		t.Fatalf("error_code=%q, want %q", snapshot.ErrorCode, im.CodeNetwork)
	}
	if snapshot.ActivityCount != 1 || len(snapshot.Activities) != 1 || snapshot.Activities[0].ActiveID != 55 {
		t.Fatalf("last successful activities were not retained: %+v", snapshot)
	}
}

func TestIMConcurrentFetchIsSingleFlightPerAccount(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	client := &fakeIMClient{discover: func(ctx context.Context, _ im.Token) ([]im.Group, []im.SignActivity, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		return []im.Group{{ID: "g1"}}, nil, nil
	}}
	svc, _, uid := newTestIMService(t, provider, client)

	firstDone := make(chan IMSnapshot, 1)
	go func() { firstDone <- svc.RefreshIfStale(context.Background(), uid) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first poll did not reach Discover")
	}

	secondDone := make(chan IMSnapshot, 1)
	go func() { secondDone <- svc.RefreshIfStale(context.Background(), uid) }()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second poll did not return while the first was in flight")
	}
	close(release)
	<-firstDone

	if tokenCalls, discoverCalls := client.counts(); tokenCalls != 1 || discoverCalls != 1 {
		t.Fatalf("tokenCalls=%d discoverCalls=%d, want 1/1", tokenCalls, discoverCalls)
	}
}

func TestIMRefreshIfStaleRespectsMinInterval(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	client := &fakeIMClient{discover: func(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
		return []im.Group{{ID: "g1"}}, []im.SignActivity{{ActiveID: 5, CourseID: 10, ClassID: 20, AType: 2}}, nil
	}}
	svc, _, uid := newTestIMService(t, provider, client)

	first := svc.RefreshIfStale(context.Background(), uid)
	if first.ErrorCode != "" || first.ActivityCount != 1 {
		t.Fatalf("first fetch: %+v", first)
	}
	_, discoverCalls := client.counts()
	if discoverCalls != 1 {
		t.Fatalf("discover calls=%d, want 1", discoverCalls)
	}

	second := svc.RefreshIfStale(context.Background(), uid)
	if second.ErrorCode != "" || second.ActivityCount != 1 {
		t.Fatalf("cached snapshot: %+v", second)
	}
	if _, discoverCalls := client.counts(); discoverCalls != 1 {
		t.Fatalf("second request inside interval fetched upstream: %d", discoverCalls)
	}

	svc.mu.Lock()
	snapshot := svc.snapshots[uid]
	snapshot.LastPollAt = time.Now().Add(-31 * time.Second)
	svc.snapshots[uid] = snapshot
	svc.mu.Unlock()
	_ = svc.RefreshIfStale(context.Background(), uid)
	if _, discoverCalls := client.counts(); discoverCalls != 2 {
		t.Fatalf("stale snapshot did not fetch: %d", discoverCalls)
	}
}

func TestIMRefreshIfStaleTimesOutAndDegrades(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	client := &fakeIMClient{discover: func(ctx context.Context, _ im.Token) ([]im.Group, []im.SignActivity, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}}
	database, cc, uid := imServiceFixture(t)
	svc := NewIMSignService(database, provider, client, cc, 30*time.Second, 50*time.Millisecond, true)

	started := time.Now()
	snapshot := svc.RefreshIfStale(context.Background(), uid)
	if snapshot.ErrorCode != im.CodeTimeout {
		t.Fatalf("error_code=%q, want %q", snapshot.ErrorCode, im.CodeTimeout)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("fetch timeout not enforced: %v", elapsed)
	}
}

func TestIMRefreshIfStaleDisabledWhenIntervalZero(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	client := &fakeIMClient{}
	database, cc, uid := imServiceFixture(t)
	svc := NewIMSignService(database, provider, client, cc, 0, 5*time.Second, true)
	snapshot := svc.RefreshIfStale(context.Background(), uid)
	if snapshot.Enabled || snapshot.ErrorCode != im.CodeDisabled {
		t.Fatalf("unexpected disabled snapshot: %+v", snapshot)
	}
	if tokenCalls, discoverCalls := client.counts(); tokenCalls != 0 || discoverCalls != 0 {
		t.Fatalf("disabled service contacted upstream: token=%d discover=%d", tokenCalls, discoverCalls)
	}
}

func TestIMFetchLogsDoNotContainSecrets(t *testing.T) {
	provider := &fakeIMAccountProvider{account: xxt.IMAccount{Password: imTestEncryptedPassword}}
	client := &fakeIMClient{discover: func(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
		return []im.Group{{ID: "g1"}}, []im.SignActivity{{
			ActiveID: 4242, Title: "秘密签到标题", SubTitle: "秘密副标题", CourseName: "秘密课程",
			CourseID: 10, ClassID: 20, AType: 2,
		}}, nil
	}}
	svc, _, uid := newTestIMService(t, provider, client)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	snapshot := svc.RefreshIfStale(context.Background(), uid)
	if snapshot.ErrorCode != "" {
		t.Fatalf("unexpected error: %s", snapshot.ErrorCode)
	}
	output := logs.String()
	for _, secret := range []string{"secret-im-pass", "token-1", "秘密", "chaoxing-password"} {
		if strings.Contains(output, secret) {
			t.Fatalf("log leaked %q: %s", secret, output)
		}
	}
	for _, want := range []string{"active_id=4242", "source=im_group", "result=ok"} {
		if !strings.Contains(output, want) {
			t.Fatalf("log missing %q: %s", want, output)
		}
	}
}
