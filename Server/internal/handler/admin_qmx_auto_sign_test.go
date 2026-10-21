package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/common"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/testutil"
)

func TestRunOwnAccountOutsideManualWindowReturns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.NewQMXAutoSignService(nil, nil, nil, nil, nil, nil)
	svc.SetNowFunc(func() time.Time {
		return time.Date(2026, 10, 21, 21, 59, 59, 0, time.FixedZone("CST", 8*60*60))
	})
	h := NewAdminQMXAutoSignHandler(nil, svc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(common.CtxUserUID, int64(1))
		c.Next()
	})
	router.POST("/qmx/auto-sign/run", h.RunOwnAccount)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/qmx/auto-sign/run", nil))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "当前不在签到时间（22:00–22:30）") {
		t.Fatalf("body missing window message: %s", recorder.Body.String())
	}
}

func qmxHandlerTime(hour, minute int) time.Time {
	return time.Date(2026, 10, 21, hour, minute, 0, 0, time.FixedZone("CST", 8*60*60))
}

type qmxSettingsResponse struct {
	NextRunAt        int64  `json:"next_run_at"`
	NextRunWindowEnd int64  `json:"next_run_window_end"`
	NextRunAtText    string `json:"next_run_at_text"`
	WindowEndText    string `json:"next_run_window_end_text"`
	Timezone         string `json:"timezone"`
}

func decodeQMXSettings(t *testing.T, recorder *httptest.ResponseRecorder) qmxSettingsResponse {
	t.Helper()
	var body struct {
		Data struct {
			Settings qmxSettingsResponse `json:"settings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings: %v; body=%s", err, recorder.Body.String())
	}
	return body.Data.Settings
}

func qmxDisplayTime(t *testing.T, at time.Time) string {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	return at.In(loc).Format("2006/01/02 15:04:05")
}

func TestGetOwnSettingsReturnsAccountExecutableTime(t *testing.T) {
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.QMXAutoSignSetting{}, &model.QMXAutoSignAccount{}, &model.QMXAutoSignRecord{}); err != nil {
		t.Fatal(err)
	}
	now := qmxHandlerTime(21, 0)
	svc := service.NewQMXAutoSignService(database, nil, nil, nil, nil, nil)
	svc.SetNowFunc(func() time.Time { return now })
	h := NewAdminQMXAutoSignHandler(database, svc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(common.CtxUserUID, int64(42))
		c.Next()
	})
	router.GET("/qmx/auto-sign/settings", h.GetOwnSettings)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/qmx/auto-sign/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	got := decodeQMXSettings(t, recorder)
	want := svc.NextRunAtFor(42, now)
	if got.NextRunAt != want.UnixMilli() || got.NextRunWindowEnd != want.UnixMilli() {
		t.Fatalf("own settings = %d..%d, want %d", got.NextRunAt, got.NextRunWindowEnd, want.UnixMilli())
	}
	wantText := qmxDisplayTime(t, want)
	if got.NextRunAtText != wantText || got.WindowEndText != wantText {
		t.Fatalf("own settings text = %q..%q, want %q", got.NextRunAtText, got.WindowEndText, wantText)
	}
	if got.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q, want Asia/Shanghai", got.Timezone)
	}
}

func TestAdminQMXAutoSignOverviewExposesWindow(t *testing.T) {
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.QMXAutoSignSetting{}, &model.QMXAutoSignAccount{}, &model.QMXAutoSignRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.QMXAutoSignSetting{ID: 1, Enabled: true, Timezone: "Asia/Shanghai", RunAt: "22:00"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{42, 43} {
		if err := database.Create(&model.User{UID: uid, Mobile: fmt.Sprintf("138%08d", uid), Name: fmt.Sprintf("user-%d", uid), Permission: 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&model.QMXAutoSignAccount{UserUID: uid, Enabled: true, LocationName: "dorm", LocationIndex: 1, Longitude: 120, Latitude: 30}).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := qmxHandlerTime(21, 0)
	svc := service.NewQMXAutoSignService(database, nil, nil, nil, nil, nil)
	svc.SetNowFunc(func() time.Time { return now })
	h := NewAdminQMXAutoSignHandler(database, svc)
	router := gin.New()
	router.GET("/admin/qmx-auto-sign", h.Overview)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/qmx-auto-sign", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	got := decodeQMXSettings(t, recorder)
	first := svc.NextRunAtFor(42, now)
	second := svc.NextRunAtFor(43, now)
	wantStart, wantEnd := first, second
	if wantEnd.Before(wantStart) {
		wantStart, wantEnd = wantEnd, wantStart
	}
	if got.NextRunAt != wantStart.UnixMilli() || got.NextRunWindowEnd != wantEnd.UnixMilli() {
		t.Fatalf("admin window = %d..%d, want %d..%d", got.NextRunAt, got.NextRunWindowEnd, wantStart.UnixMilli(), wantEnd.UnixMilli())
	}
	if got.NextRunAtText != qmxDisplayTime(t, wantStart) || got.WindowEndText != qmxDisplayTime(t, wantEnd) {
		t.Fatalf("admin text = %q..%q", got.NextRunAtText, got.WindowEndText)
	}
	if got.WindowEndText == got.NextRunAtText {
		t.Fatalf("admin window text must span enabled accounts: %q", got.NextRunAtText)
	}
	if got.NextRunAt >= got.NextRunWindowEnd {
		t.Fatalf("admin window must span enabled accounts: %d..%d", got.NextRunAt, got.NextRunWindowEnd)
	}
}

func TestQMXSettingsAndRecordEmitDisplayText(t *testing.T) {
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.QMXAutoSignSetting{}, &model.QMXAutoSignAccount{}, &model.QMXAutoSignRecord{}); err != nil {
		t.Fatal(err)
	}
	// 2026-10-22T13:30:00Z = 21:30 Shanghai; 14:03:12Z = 22:03:12 Shanghai.
	now := time.Date(2026, 10, 22, 13, 30, 0, 0, time.UTC)
	executedAt := time.Date(2026, 10, 22, 14, 3, 12, 0, time.UTC)
	if err := database.Create(&model.QMXAutoSignRecord{
		UserUID:    42,
		Trigger:    "scheduled",
		Success:    true,
		ExecutedAt: executedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.NewQMXAutoSignService(database, nil, nil, nil, nil, nil)
	svc.SetNowFunc(func() time.Time { return now })
	h := NewAdminQMXAutoSignHandler(database, svc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(common.CtxUserUID, int64(42))
		c.Next()
	})
	router.GET("/qmx/auto-sign/settings", h.GetOwnSettings)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/qmx/auto-sign/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Settings   qmxSettingsResponse `json:"settings"`
			LastRecord struct {
				ExecutedAt     int64  `json:"executed_at"`
				ExecutedAtText string `json:"executed_at_text"`
			} `json:"last_record"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings: %v; body=%s", err, recorder.Body.String())
	}
	wantNext := svc.NextRunAtFor(42, now)
	if body.Data.Settings.NextRunAtText != qmxDisplayTime(t, wantNext) {
		t.Fatalf("next_run_at_text = %q, want %q", body.Data.Settings.NextRunAtText, qmxDisplayTime(t, wantNext))
	}
	if !strings.HasPrefix(body.Data.Settings.NextRunAtText, "2026/10/22 22:") {
		t.Fatalf("next_run_at_text = %q, want 2026/10/22 22:xx:xx", body.Data.Settings.NextRunAtText)
	}
	if body.Data.Settings.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q, want Asia/Shanghai", body.Data.Settings.Timezone)
	}
	if body.Data.LastRecord.ExecutedAtText != "2026/10/22 22:03:12" {
		t.Fatalf("executed_at_text = %q, want 2026/10/22 22:03:12", body.Data.LastRecord.ExecutedAtText)
	}
	if body.Data.LastRecord.ExecutedAt != executedAt.UnixMilli() {
		t.Fatalf("executed_at = %d, want %d", body.Data.LastRecord.ExecutedAt, executedAt.UnixMilli())
	}
}

func TestQMXRecordViewsEmitDisplayText(t *testing.T) {
	executedAt := time.Date(2026, 10, 22, 14, 3, 12, 0, time.UTC)
	h := &AdminQMXAutoSignHandler{}

	record := h.recordView(model.QMXAutoSignRecord{ExecutedAt: executedAt}, nil)
	if got := record["executed_at_text"]; got != "2026/10/22 22:03:12" {
		t.Fatalf("executed_at_text = %v, want 2026/10/22 22:03:12", got)
	}

	group := adminQMXAutoSignRecordGroupView(adminQMXAutoSignRecordGroupRow{
		FirstExecutedAt: executedAt,
		LastExecutedAt:  executedAt.Add(time.Minute),
	})
	if got := group["first_executed_at_text"]; got != "2026/10/22 22:03:12" {
		t.Fatalf("first_executed_at_text = %v, want 2026/10/22 22:03:12", got)
	}
	if got := group["last_executed_at_text"]; got != "2026/10/22 22:04:12" {
		t.Fatalf("last_executed_at_text = %v, want 2026/10/22 22:04:12", got)
	}
}

func TestQMXTimeViewsEmitEmptyTextForZeroTime(t *testing.T) {
	h := &AdminQMXAutoSignHandler{}
	record := h.recordView(model.QMXAutoSignRecord{}, nil)
	if got := record["executed_at_text"]; got != "" {
		t.Fatalf("executed_at_text = %v, want empty", got)
	}
	group := adminQMXAutoSignRecordGroupView(adminQMXAutoSignRecordGroupRow{})
	if got := group["first_executed_at_text"]; got != "" {
		t.Fatalf("first_executed_at_text = %v, want empty", got)
	}
}
