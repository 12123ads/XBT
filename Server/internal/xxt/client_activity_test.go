package xxt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

type activityScopeTransport func(*http.Request) (*http.Response, error)

func (f activityScopeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func activityScopeTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client := New("1234567890123456", "activity-scope-test", false, 5)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.sessions["13800000001"] = &Session{Mobile: "13800000001", Password: "password", UID: 1, Jar: jar, LastLoginAt: time.Now()}
	client.http.Transport = transport
	return client
}

func TestActivityScopeLookupIsNotTruncatedByDisplayLimit(t *testing.T) {
	activities := make([]map[string]interface{}, 0, 8)
	for id := int64(1); id <= 8; id++ {
		activities = append(activities, map[string]interface{}{"id": id, "activeType": 2, "nameOne": "签到"})
	}
	payload, err := json.Marshal(map[string]interface{}{"data": map[string]interface{}{"activeList": activities}})
	if err != nil {
		t.Fatal(err)
	}
	client := activityScopeTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/ppt/activeAPI/taskactivelist" || req.URL.Query().Get("courseId") != "10" || req.URL.Query().Get("classId") != "20" {
			t.Fatalf("unexpected activity request: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(payload))), Request: req}, nil
	}))
	visible, err := client.GetActives("13800000001", "password", 10, 20)
	if err != nil || len(visible) != 5 || visible[4].ActiveID != 5 {
		t.Fatalf("display activity cap changed: visible=%+v err=%v", visible, err)
	}
	found, err := client.HasActivityInCourse("13800000001", "password", 10, 20, 7)
	if err != nil || !found {
		t.Fatalf("valid old activity beyond display cap lost authorization: found=%v err=%v", found, err)
	}
	found, err = client.HasActivityInCourse("13800000001", "password", 10, 20, 99)
	if err != nil || found {
		t.Fatalf("missing activity was authorized: found=%v err=%v", found, err)
	}
}

func TestActivityScopeLookupRejectsUnsuccessfulResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"HTTP failure with matching activity", http.StatusServiceUnavailable, `{"activeList":[{"id":7,"activeType":2,"nameOne":"签到"}]}`},
		{"invalid JSON", http.StatusOK, `<html>login required</html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := activityScopeTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			}))
			if found, err := client.HasActivityInCourse("13800000001", "password", 10, 20, 7); found || err == nil {
				t.Fatalf("failed activity response became trusted scope: found=%v err=%v", found, err)
			}
		})
	}
}
