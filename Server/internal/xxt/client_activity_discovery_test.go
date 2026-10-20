package xxt

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGetActivesParsesKindsStatusesAndExt(t *testing.T) {
	payload := `{"data":{"ext":{"course":"token"},"activeList":[
		{"id":7,"type":2,"status":1,"userStatus":0,"startTime":"1700000000000","endTime":1700000600000,"otherId":"4","nameOne":"签到","activeType":2},
		{"id":8,"type":74,"status":1,"userStatus":1,"startTime":1700000000000,"endTime":1700000600000,"nameOne":"签退"},
		{"id":9,"type":1,"status":1,"nameOne":"问卷","activeType":2},
		{"id":10,"activeType":2,"nameOne":"旧签到"}
	]}}`
	client := activityScopeTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	}))

	actives, err := client.GetActives("13800000001", "password", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(actives) != 3 {
		t.Fatalf("actives=%+v, want 3 sign activities", actives)
	}
	signIn := actives[0]
	if signIn.ActiveID != 7 || signIn.Type != ActivityKindSignIn || signIn.Status != 1 || signIn.UserStatus != 0 {
		t.Fatalf("sign-in discovery fields lost: %+v", signIn)
	}
	if signIn.StartTime != 1700000000000 || signIn.EndTime != 1700000600000 || signIn.SignType != SignLocation {
		t.Fatalf("sign-in list metadata lost: %+v", signIn)
	}
	if signIn.Ext != `{"course":"token"}` {
		t.Fatalf("list ext lost: %q", signIn.Ext)
	}

	signOut := actives[1]
	if signOut.ActiveID != 8 || signOut.Type != ActivityKindSignOut || signOut.UserStatus != 1 {
		t.Fatalf("sign-out activity lost: %+v", signOut)
	}

	legacy := actives[2]
	if legacy.ActiveID != 10 || legacy.Type != 0 || legacy.Name != "旧签到" {
		t.Fatalf("legacy payload shape regressed: %+v", legacy)
	}
}

func TestGetActivesPrefersActivityExtOverListExt(t *testing.T) {
	payload := `{"data":{"ext":"list-ext","activeList":[
		{"id":11,"type":2,"status":1,"nameOne":"签到","ext":{"activity":true}}
	]}}`
	client := activityScopeTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	}))
	actives, err := client.GetActives("13800000001", "password", 10, 20)
	if err != nil || len(actives) != 1 {
		t.Fatalf("actives=%+v err=%v", actives, err)
	}
	if actives[0].Ext != `{"activity":true}` {
		t.Fatalf("activity ext not preferred: %q", actives[0].Ext)
	}
}

func TestHasActivityInCourseIncludesSignOutActivities(t *testing.T) {
	payload := `{"data":{"activeList":[{"id":12,"type":74,"nameOne":"签退"}]}}`
	client := activityScopeTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	}))
	found, err := client.HasActivityInCourse("13800000001", "password", 10, 20, 12)
	if err != nil || !found {
		t.Fatalf("sign-out activity not authorized: found=%v err=%v", found, err)
	}
}
