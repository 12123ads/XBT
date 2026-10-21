package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/common"
	"xbt2/server/internal/service"
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
