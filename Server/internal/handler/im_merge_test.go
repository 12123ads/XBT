package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"xbt2/server/internal/im"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

type fakeIMSource struct {
	snapshot     service.IMSnapshot
	refreshCalls int
}

func (f *fakeIMSource) Snapshot(int64) service.IMSnapshot { return f.snapshot }

func (f *fakeIMSource) RefreshIfStale(context.Context, int64) service.IMSnapshot {
	f.refreshCalls++
	return f.snapshot
}

type imActivitiesPayload struct {
	Code int `json:"code"`
	Data []struct {
		CourseID   int64 `json:"course_id"`
		ClassID    int64 `json:"class_id"`
		HasMore    bool  `json:"has_more"`
		Activities []struct {
			ActiveID      int64    `json:"active_id"`
			ActivityName  string   `json:"activity_name"`
			StartTime     int64    `json:"start_time"`
			SignType      int      `json:"sign_type"`
			Source        string   `json:"source"`
			Sources       []string `json:"sources"`
			IMAType       int      `json:"im_atype"`
			IMSubTitle    string   `json:"im_sub_title"`
			IMMessageTime int64    `json:"im_message_time"`
		} `json:"activities"`
	} `json:"data"`
}

func TestActivitiesMergesIMGroupSourceAndDeduplicates(t *testing.T) {
	database, router, upstream, h := handlerSignFixtureParts(t)
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "任务列表签到"}, {ActiveID: 101, Name: "任务列表另一个"}}
	upstream.detail = xxtSignDetail()
	source := &fakeIMSource{snapshot: service.IMSnapshot{
		Enabled:         true,
		IntervalSeconds: 30,
		Activities: []im.SignActivity{
			{ActiveID: 100, Title: "群聊签到", CourseID: 10, ClassID: 20, AType: 2, MessageTimeMS: 1700000200000},
			{ActiveID: 200, Title: "群聊新签到", CourseID: 10, ClassID: 20, AType: 74, SubTitle: "10-20 10:10", MessageTimeMS: 1700000300000},
			{ActiveID: 300, Title: "未选课程", CourseID: 99, ClassID: 98, AType: 2, MessageTimeMS: 1700000400000},
		},
	}}
	h.SetIMSignSource(source)

	response := accessRequest(t, router, http.MethodGet, "/activities", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}
	if source.refreshCalls != 1 {
		t.Fatalf("activities did not trigger exactly one on-demand fetch: %d", source.refreshCalls)
	}
	var payload imActivitiesPayload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 || len(payload.Data) != 1 {
		t.Fatalf("unexpected payload: %s", response.Body.String())
	}
	group := payload.Data[0]
	if group.CourseID != 10 || group.ClassID != 20 {
		t.Fatalf("unexpected group: %+v", group)
	}
	byID := map[int64]int{}
	for index, activity := range group.Activities {
		byID[activity.ActiveID] = index
	}
	if len(group.Activities) != 3 {
		t.Fatalf("activities=%+v", group.Activities)
	}
	merged := group.Activities[byID[100]]
	if merged.ActivityName != "任务列表签到" {
		t.Fatalf("taskactivelist name was overwritten: %+v", merged)
	}
	if merged.Source != im.SourceIMGroup || len(merged.Sources) != 2 {
		t.Fatalf("merged source=%q sources=%v", merged.Source, merged.Sources)
	}
	if merged.Sources[0] != im.SourceTaskActiveList && merged.Sources[0] != im.SourceIMGroup {
		t.Fatalf("unexpected sources: %v", merged.Sources)
	}
	imOnly := group.Activities[byID[200]]
	if imOnly.Source != im.SourceIMGroup || len(imOnly.Sources) != 1 || imOnly.Sources[0] != im.SourceIMGroup {
		t.Fatalf("im-only source=%q sources=%v", imOnly.Source, imOnly.Sources)
	}
	if imOnly.IMAType != 74 || imOnly.ActivityName != "群聊新签到" || imOnly.IMSubTitle != "10-20 10:10" {
		t.Fatalf("unexpected im-only activity: %+v", imOnly)
	}
	if _, ok := byID[300]; ok {
		t.Fatal("unselected course activity was merged")
	}
	for _, activity := range group.Activities {
		if activity.ActiveID == 100 {
			continue
		}
		if len(activity.Sources) != 1 {
			t.Fatalf("unexpected sources on non-merged activity: %+v", activity)
		}
	}
	var scope model.SignActivityScope
	if err := database.Where("activity_id = ?", 200).Take(&scope).Error; err != nil {
		t.Fatalf("im activity scope was not saved: %v", err)
	}
	var cache model.SignActivity
	if err := database.Where("activity_id = ?", 200).Take(&cache).Error; err != nil {
		t.Fatalf("im activity detail was not cached: %v", err)
	}
}

func TestActivitiesKeepsTaskListWhenIMFetchFailed(t *testing.T) {
	_, router, upstream, h := handlerSignFixtureParts(t)
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "任务列表签到"}}
	upstream.detail = xxtSignDetail()
	h.SetIMSignSource(&fakeIMSource{snapshot: service.IMSnapshot{
		Enabled:         true,
		IntervalSeconds: 30,
		ErrorCode:       im.CodeAuthFailed,
	}})

	response := accessRequest(t, router, http.MethodGet, "/activities", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}
	var payload imActivitiesPayload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || len(payload.Data[0].Activities) != 1 || payload.Data[0].Activities[0].ActiveID != 100 {
		t.Fatalf("normal activity list changed after IM failure: %s", response.Body.String())
	}

	status := accessRequest(t, router, http.MethodGet, "/im/status", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	var statusPayload struct {
		Code int `json:"code"`
		Data struct {
			Enabled         bool   `json:"enabled"`
			IntervalSeconds int    `json:"interval_seconds"`
			ErrorCode       string `json:"error_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if statusPayload.Code != 0 || !statusPayload.Data.Enabled || statusPayload.Data.IntervalSeconds != 30 || statusPayload.Data.ErrorCode != im.CodeAuthFailed {
		t.Fatalf("unexpected status payload: %s", status.Body.String())
	}
}

type handlerIMProvider struct{}

func (handlerIMProvider) GetIMAccountContext(context.Context, string, string) (xxt.IMAccount, error) {
	return xxt.IMAccount{Password: "0bd654e244170185155ddeefe47a8007"}, nil
}

type handlerIMClient struct {
	discoverCalls int
}

func (c *handlerIMClient) Token(context.Context, int64, string) (im.Token, error) {
	return im.Token{AccessToken: "token-1", Username: "im-user", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (c *handlerIMClient) Discover(context.Context, im.Token) ([]im.Group, []im.SignActivity, error) {
	c.discoverCalls++
	return []im.Group{{ID: "g1"}}, []im.SignActivity{{
		ActiveID: 200, Title: "群聊签到", CourseID: 10, ClassID: 20, AType: 2, MessageTimeMS: 1700000300000,
	}}, nil
}

func TestActivitiesFetchesIMAtMostOncePerInterval(t *testing.T) {
	database, router, upstream, h := handlerSignFixtureParts(t)
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "任务列表签到"}}
	upstream.detail = xxtSignDetail()
	client := &handlerIMClient{}
	cc := service.NewCredentialCrypto("handler-sign-test")
	h.SetIMSignSource(service.NewIMSignService(database, handlerIMProvider{}, client, cc, time.Minute, time.Second, true))

	for attempt := 1; attempt <= 2; attempt++ {
		response := accessRequest(t, router, http.MethodGet, "/activities", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
		var payload imActivitiesPayload
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, group := range payload.Data {
			for _, activity := range group.Activities {
				if activity.ActiveID == 200 && activity.Source == im.SourceIMGroup {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("attempt %d missing IM activity: %s", attempt, response.Body.String())
		}
	}
	if client.discoverCalls != 1 {
		t.Fatalf("discover calls=%d, want 1 within the interval", client.discoverCalls)
	}
}

type handlerBlockingIMClient struct{}

func (handlerBlockingIMClient) Token(context.Context, int64, string) (im.Token, error) {
	return im.Token{AccessToken: "token-1", Username: "im-user", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (handlerBlockingIMClient) Discover(ctx context.Context, _ im.Token) ([]im.Group, []im.SignActivity, error) {
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func TestActivitiesTimesOutIMFetchAndKeepsTaskList(t *testing.T) {
	database, router, upstream, h := handlerSignFixtureParts(t)
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "任务列表签到"}}
	upstream.detail = xxtSignDetail()
	cc := service.NewCredentialCrypto("handler-sign-test")
	h.SetIMSignSource(service.NewIMSignService(database, handlerIMProvider{}, handlerBlockingIMClient{}, cc, time.Minute, 50*time.Millisecond, true))

	response := accessRequest(t, router, http.MethodGet, "/activities", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}
	var payload imActivitiesPayload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	foundTask := false
	for _, group := range payload.Data {
		for _, activity := range group.Activities {
			if activity.ActiveID == 100 {
				foundTask = true
			}
		}
	}
	if !foundTask {
		t.Fatalf("taskactivelist result missing after IM timeout: %s", response.Body.String())
	}

	status := accessRequest(t, router, http.MethodGet, "/im/status", nil)
	var statusPayload struct {
		Data struct {
			ErrorCode string `json:"error_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if statusPayload.Data.ErrorCode != im.CodeTimeout {
		t.Fatalf("error_code=%q, want %q", statusPayload.Data.ErrorCode, im.CodeTimeout)
	}
}

func TestIMStatusWithoutSourceReportsDisabled(t *testing.T) {
	_, router, _, _ := handlerSignFixtureParts(t)
	response := accessRequest(t, router, http.MethodGet, "/im/status", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			Enabled   bool   `json:"enabled"`
			ErrorCode string `json:"error_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Enabled || payload.Data.ErrorCode != im.CodeDisabled {
		t.Fatalf("unexpected disabled status: %s", response.Body.String())
	}
}

func xxtSignDetail() xxt.SignDetail {
	return xxt.SignDetail{
		StartTime: 1700000000000,
		EndTime:   1700000600000,
		SignType:  2,
	}
}
