package im

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type easemobTestServer struct {
	tokenCalls    int
	groupCalls    int
	messageCalls  int
	tokenStatus   int
	groupStatus   int
	messageStatus int
	tokenBody     string
	groupBody     string
}

func newEasemobTestServer(t *testing.T, fixture *easemobTestServer) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fixture.tokenCalls++
		if fixture.tokenStatus != 0 {
			w.WriteHeader(fixture.tokenStatus)
			_, _ = io.WriteString(w, fixture.tokenBody)
			return
		}
		var request struct {
			GrantType string `json:"grant_type"`
			Username  string `json:"username"`
			Password  string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("token body: %v", err)
		}
		if request.GrantType != "password" || request.Username != "12345" || request.Password != "secret-im-pass" {
			t.Errorf("unexpected token request: %+v", request)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("token content type: %q", got)
		}
		_, _ = io.WriteString(w, `{"access_token":"token-1","expires_in":3600,"user":{"username":"im-user","uuid":"uuid-1"}}`)
	})
	mux.HandleFunc("/users/im-user/joined_chatgroups", func(w http.ResponseWriter, r *http.Request) {
		fixture.groupCalls++
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Errorf("group auth header: %q", r.Header.Get("Authorization"))
		}
		if fixture.groupStatus != 0 {
			w.WriteHeader(fixture.groupStatus)
			_, _ = io.WriteString(w, fixture.groupBody)
			return
		}
		_, _ = io.WriteString(w, `{"data":[
			{"id":"group-1","name":"","description":"{\"courseInfo\":{\"coursename\":\"高等数学\"}}"},
			{"id":42,"name":"班级群","description":null},
			{"id":"","name":"ignored"}
		]}`)
	})
	mux.HandleFunc("/users/im-user/messageroaming", func(w http.ResponseWriter, r *http.Request) {
		fixture.messageCalls++
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Errorf("message auth header: %q", r.Header.Get("Authorization"))
		}
		var request struct {
			Queue string `json:"queue"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("message body: %v", err)
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain;charset=UTF-8" {
			t.Errorf("message content type: %q", got)
		}
		attachment := `{"attachmentType":15,"att_chat_course":{"aid":100,"atype":2,"title":"签到","courseInfo":{"courseid":7,"classid":8,"coursename":"高等数学"}}}`
		other := `{"attachmentType":15,"att_chat_course":{"aid":101,"atype":74,"title":"签退","courseInfo":{"courseid":7,"classid":8,"coursename":"高等数学"}}}`
		timestamp := uint64(1000)
		if request.Queue == "42@conference.easemob.com" {
			timestamp = 2000
		}
		payload := map[string]any{"data": map[string]any{"msgs": []map[string]string{
			{"msg": protoRoamingMessage(timestamp, protoKeyValue("attachment", attachment), protoKeyValue("text", "hello"))},
			{"msg": protoRoamingMessage(timestamp+1, protoKeyValue("attachment", other))},
			{"msg": base64.StdEncoding.EncodeToString([]byte("not a message"))},
		}}}
		_ = json.NewEncoder(w).Encode(payload)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := New(server.URL+"/token", server.URL, server.URL)
	return server, client
}

func TestClientTokenAndDiscover(t *testing.T) {
	fixture := &easemobTestServer{}
	_, client := newEasemobTestServer(t, fixture)
	token, err := client.Token(context.Background(), 12345, "secret-im-pass")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "token-1" || token.Username != "im-user" || token.UUID != "uuid-1" {
		t.Fatalf("unexpected token: %+v", token)
	}
	if token.ExpiresAt.IsZero() {
		t.Fatal("token expiry was not set")
	}

	groups, activities, err := client.Discover(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Name != "高等数学" || groups[1].Name != "班级群" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if len(activities) != 2 {
		t.Fatalf("unexpected activities: %+v", activities)
	}
	byID := map[int64]SignActivity{}
	for _, activity := range activities {
		byID[activity.ActiveID] = activity
	}
	if byID[100].MessageTimeMS != 2000 {
		t.Fatalf("duplicate active id did not keep latest message: %+v", byID[100])
	}
	if byID[101].AType != 74 || byID[101].Title != "签退" {
		t.Fatalf("unexpected atype=74 card: %+v", byID[101])
	}
	if fixture.tokenCalls != 1 || fixture.groupCalls != 1 || fixture.messageCalls != 2 {
		t.Fatalf("unexpected call counts: %+v", fixture)
	}
}

func TestClientTokenAuthFailure(t *testing.T) {
	fixture := &easemobTestServer{tokenStatus: http.StatusUnauthorized, tokenBody: "bad credentials"}
	_, client := newEasemobTestServer(t, fixture)
	_, err := client.Token(context.Background(), 12345, "secret-im-pass")
	var imErr *Error
	if !errors.As(err, &imErr) || imErr.Code != CodeAuthFailed {
		t.Fatalf("got %v, want %s", err, CodeAuthFailed)
	}
	if strings.Contains(err.Error(), "bad credentials") {
		t.Fatal("upstream body leaked into error")
	}
}

func TestClientTokenServerFailure(t *testing.T) {
	fixture := &easemobTestServer{tokenStatus: http.StatusInternalServerError, tokenBody: "oops"}
	_, client := newEasemobTestServer(t, fixture)
	_, err := client.Token(context.Background(), 12345, "secret-im-pass")
	var imErr *Error
	if !errors.As(err, &imErr) || imErr.Code != CodeTokenFailed {
		t.Fatalf("got %v, want %s", err, CodeTokenFailed)
	}
}

func TestClientDiscoverUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusRequestTimeout} {
		fixture := &easemobTestServer{groupStatus: status, groupBody: "expired"}
		_, client := newEasemobTestServer(t, fixture)
		_, _, err := client.Discover(context.Background(), Token{AccessToken: "token-1", Username: "im-user"})
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("status %d: got %v, want ErrUnauthorized", status, err)
		}
	}
}

func TestClientGroupFailureDoesNotLeakBody(t *testing.T) {
	fixture := &easemobTestServer{groupStatus: http.StatusInternalServerError, groupBody: "token-1 secret body"}
	_, client := newEasemobTestServer(t, fixture)
	_, _, err := client.Discover(context.Background(), Token{AccessToken: "token-1", Username: "im-user"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret body") || strings.Contains(err.Error(), "token-1") {
		t.Fatalf("error leaked response content: %v", err)
	}
	var imErr *Error
	if !errors.As(err, &imErr) || imErr.Code != CodeGroupsFailed {
		t.Fatalf("got %v, want %s", err, CodeGroupsFailed)
	}
}

func TestClientMalformedToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := New(server.URL+"/token", server.URL, server.URL)
	_, err := client.Token(context.Background(), 1, "x")
	var imErr *Error
	if !errors.As(err, &imErr) || imErr.Code != CodeParseFailed {
		t.Fatalf("got %v, want %s", err, CodeParseFailed)
	}
}
