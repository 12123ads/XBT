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

func decodeQMXSettings(t *testing.T, recorder *httptest.ResponseRecorder) (int64, int64) {
	t.Helper()
	var body struct {
		Data struct {
			Settings struct {
				NextRunAt        int64 `json:"next_run_at"`
				NextRunWindowEnd int64 `json:"next_run_window_end"`
			} `json:"settings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings: %v; body=%s", err, recorder.Body.String())
	}
	return body.Data.Settings.NextRunAt, body.Data.Settings.NextRunWindowEnd
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
	nextRunAt, windowEnd := decodeQMXSettings(t, recorder)
	want := svc.NextRunAtFor(42, now).UnixMilli()
	if nextRunAt != want || windowEnd != want {
		t.Fatalf("own settings = %d..%d, want %d", nextRunAt, windowEnd, want)
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
	nextRunAt, windowEnd := decodeQMXSettings(t, recorder)
	first := svc.NextRunAtFor(42, now)
	second := svc.NextRunAtFor(43, now)
	wantStart, wantEnd := first, second
	if wantEnd.Before(wantStart) {
		wantStart, wantEnd = wantEnd, wantStart
	}
	if nextRunAt != wantStart.UnixMilli() || windowEnd != wantEnd.UnixMilli() {
		t.Fatalf("admin window = %d..%d, want %d..%d", nextRunAt, windowEnd, wantStart.UnixMilli(), wantEnd.UnixMilli())
	}
	if windowEnd <= nextRunAt {
		t.Fatalf("admin window must span enabled accounts: %d..%d", nextRunAt, windowEnd)
	}
}
