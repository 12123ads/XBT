package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type activityListPayload struct {
	Code int `json:"code"`
	Data []struct {
		Activities []struct {
			ActiveID      int64  `json:"active_id"`
			ActivityName  string `json:"activity_name"`
			Type          int    `json:"type"`
			Status        int    `json:"status"`
			UserStatus    int    `json:"user_status"`
			StartTime     int64  `json:"start_time"`
			EndTime       int64  `json:"end_time"`
			SignType      int    `json:"sign_type"`
			IsSignOut     bool   `json:"is_sign_out"`
			ActivityState string `json:"activity_state"`
		} `json:"activities"`
		HasMore bool `json:"has_more"`
	} `json:"data"`
}

func decodeActivityList(t *testing.T, body []byte) activityListPayload {
	t.Helper()
	var payload activityListPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 || len(payload.Data) != 1 {
		t.Fatalf("unexpected activities payload: %s", body)
	}
	return payload
}

type activityItemView struct {
	Type          int
	Status        int
	SignType      int
	IsSignOut     bool
	ActivityState string
}

func TestActivitiesMarksExpiredAndNotStartedWithoutSilentDrops(t *testing.T) {
	_, router, upstream := handlerSignFixture(t)
	now := time.Now()
	upstream.detailErr = errors.New("metadata unavailable")
	upstream.activities = []xxt.Active{
		{ActiveID: 100, Name: "进行中", Type: xxt.ActivityKindSignIn, Status: 1, StartTime: now.Add(-5 * time.Minute).UnixMilli(), EndTime: now.Add(5 * time.Minute).UnixMilli()},
		{ActiveID: 101, Name: "签退", Type: xxt.ActivityKindSignOut, Status: 1, StartTime: now.Add(-5 * time.Minute).UnixMilli(), EndTime: now.Add(5 * time.Minute).UnixMilli()},
		{ActiveID: 102, Name: "已结束", Type: xxt.ActivityKindSignIn, Status: 2, StartTime: now.Add(-60 * time.Minute).UnixMilli(), EndTime: now.Add(-30 * time.Minute).UnixMilli()},
		{ActiveID: 103, Name: "未开始", Type: xxt.ActivityKindSignIn, Status: 1, StartTime: now.Add(10 * time.Minute).UnixMilli(), EndTime: now.Add(40 * time.Minute).UnixMilli()},
		{ActiveID: 104, Name: "仅有列表数据", Type: xxt.ActivityKindSignIn, Status: 1, StartTime: now.Add(-2 * time.Minute).UnixMilli(), EndTime: now.Add(2 * time.Minute).UnixMilli(), SignType: xxt.SignCode},
	}

	response := accessRequest(t, router, http.MethodGet, "/activities", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}
	payload := decodeActivityList(t, response.Body.Bytes())
	if len(payload.Data[0].Activities) != 5 {
		t.Fatalf("discovered activities were dropped: %+v", payload.Data[0])
	}
	byID := make(map[int64]activityItemView, 5)
	for _, item := range payload.Data[0].Activities {
		byID[item.ActiveID] = activityItemView{item.Type, item.Status, item.SignType, item.IsSignOut, item.ActivityState}
	}
	cases := []struct {
		id           int64
		wantType     int
		wantSignOut  bool
		wantState    string
		wantSignType int
	}{
		{100, xxt.ActivityKindSignIn, false, ActivityStateOngoing, 0},
		{101, xxt.ActivityKindSignOut, true, ActivityStateOngoing, 0},
		{102, xxt.ActivityKindSignIn, false, ActivityStateExpired, 0},
		{103, xxt.ActivityKindSignIn, false, ActivityStateNotStarted, 0},
		{104, xxt.ActivityKindSignIn, false, ActivityStateOngoing, xxt.SignCode},
	}
	for _, tc := range cases {
		item, ok := byID[tc.id]
		if !ok {
			t.Fatalf("activity %d missing from response", tc.id)
		}
		if item.Type != tc.wantType || item.IsSignOut != tc.wantSignOut {
			t.Fatalf("activity %d type/sign-out = %+v", tc.id, item)
		}
		if item.ActivityState != tc.wantState {
			t.Fatalf("activity %d state = %q, want %q", tc.id, item.ActivityState, tc.wantState)
		}
		if item.SignType != tc.wantSignType {
			t.Fatalf("activity %d sign_type = %d, want %d", tc.id, item.SignType, tc.wantSignType)
		}
	}
	if payload.Data[0].HasMore {
		t.Fatalf("has_more changed: %+v", payload.Data[0])
	}
}

func TestActivitiesCachesDetailExtAndDiscoveryFields(t *testing.T) {
	database, router, upstream := handlerSignFixture(t)
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "签到", Type: xxt.ActivityKindSignIn, Status: 1, UserStatus: 1}}
	upstream.detail = xxt.SignDetail{StartTime: 1700000000000, EndTime: 1700000600000, SignType: xxt.SignLocation, Ext: `{"detail":true}`}

	if response := accessRequest(t, router, http.MethodGet, "/activities", nil); response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}

	var cached model.SignActivity
	if err := database.Where("activity_id = ?", 100).First(&cached).Error; err != nil {
		t.Fatal(err)
	}
	if cached.ExtJSON != `{"detail":true}` || cached.ActivityType != xxt.ActivityKindSignIn || cached.Status != 1 || cached.UserStatus != 1 {
		t.Fatalf("discovery fields not cached: %+v", cached)
	}
}

func TestActivitiesSyncsCachedExtFromListWhenDetailFails(t *testing.T) {
	database, router, upstream := handlerSignFixture(t)
	if err := database.Create(&model.SignActivity{ActivityID: 100, StartTime: 1, EndTime: 2}).Error; err != nil {
		t.Fatal(err)
	}
	upstream.detailErr = errors.New("metadata unavailable")
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "签到", Type: xxt.ActivityKindSignOut, Status: 1, UserStatus: 1, Ext: `{"list":true}`}}

	if response := accessRequest(t, router, http.MethodGet, "/activities", nil); response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}

	var cached model.SignActivity
	if err := database.Where("activity_id = ?", 100).First(&cached).Error; err != nil {
		t.Fatal(err)
	}
	if cached.ExtJSON != `{"list":true}` || cached.ActivityType != xxt.ActivityKindSignOut || cached.Status != 1 || cached.UserStatus != 1 {
		t.Fatalf("list discovery fields not synced: %+v", cached)
	}
}
