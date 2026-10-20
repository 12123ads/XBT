package xxt

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func imAccountTestClient(t *testing.T, body string, status int) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	previous := imUserInfoURL
	imUserInfoURL = server.URL
	t.Cleanup(func() { imUserInfoURL = previous })

	client := New("aes-key", "test-agent", "", false, 20)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.sessions["13800000001"] = &Session{
		Mobile:      "13800000001",
		Password:    "password",
		UID:         77,
		Jar:         jar,
		LastLoginAt: time.Now(),
	}
	return client
}

func TestGetIMAccountParsesUserInfo(t *testing.T) {
	client := imAccountTestClient(t, `{"msg":{"uid":88,"accountInfo":{"imAccount":{"username":"im-user","password":"0bd654e244170185155ddeefe47a8007"}}}}`, http.StatusOK)
	account, err := client.GetIMAccount("13800000001", "password")
	if err != nil {
		t.Fatal(err)
	}
	if account.UID != 88 || account.Username != "im-user" || account.Password != "0bd654e244170185155ddeefe47a8007" {
		t.Fatalf("unexpected account: %+v", account)
	}
}

func TestGetIMAccountAcceptsNumericStrings(t *testing.T) {
	client := imAccountTestClient(t, `{"msg":{"uid":"88","accountInfo":{"imAccount":{"username":12345,"password":"abcd"}}}}`, http.StatusOK)
	account, err := client.GetIMAccount("13800000001", "password")
	if err != nil {
		t.Fatal(err)
	}
	if account.UID != 88 || account.Username != "12345" {
		t.Fatalf("unexpected account: %+v", account)
	}
}

func TestGetIMAccountFallsBackToSessionUID(t *testing.T) {
	client := imAccountTestClient(t, `{"msg":{"accountInfo":{"imAccount":{"password":"abcd"}}}}`, http.StatusOK)
	account, err := client.GetIMAccount("13800000001", "password")
	if err != nil {
		t.Fatal(err)
	}
	if account.UID != 77 {
		t.Fatalf("uid=%d, want session uid 77", account.UID)
	}
}

func TestGetIMAccountMissing(t *testing.T) {
	for _, body := range []string{
		`{"msg":{}}`,
		`{"msg":{"accountInfo":{}}}`,
		`{"msg":{"accountInfo":{"imAccount":{"password":""}}}}`,
	} {
		client := imAccountTestClient(t, body, http.StatusOK)
		if _, err := client.GetIMAccount("13800000001", "password"); !errors.Is(err, ErrIMAccountMissing) {
			t.Fatalf("body %s: got %v, want ErrIMAccountMissing", body, err)
		}
	}
}

func TestGetIMAccountHTTPFailureDoesNotLeakBody(t *testing.T) {
	client := imAccountTestClient(t, "secret upstream body", http.StatusInternalServerError)
	_, err := client.GetIMAccount("13800000001", "password")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret upstream body") {
		t.Fatalf("error leaked body: %v", err)
	}
}
