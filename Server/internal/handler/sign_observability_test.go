package handler

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/ratelimit"
	"xbt2/server/internal/xxt"
)

func captureSignLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

func signResultLines(buffer *bytes.Buffer) []string {
	lines := []string{}
	const marker = "sign_execute "
	for _, line := range strings.Split(buffer.String(), "\n") {
		if index := strings.Index(line, marker); index >= 0 {
			lines = append(lines, line[index:])
		}
	}
	return lines
}

func TestExecuteEmitsExactlyOneStructuredResultLine(t *testing.T) {
	_, router, upstream, h := handlerSignFixtureParts(t)
	buffer := captureSignLog(t)
	upstream.signResults = []xxt.SignResult{{Kind: xxt.SignResultSuccess, Attempts: 1, HTTPStatus: 200}}

	response := accessRequest(t, router, http.MethodPost, "/execute", gin.H{
		"activity_id": 100, "course_id": 10, "class_id": 20, "sign_type": xxt.SignNormal,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	lines := signResultLines(buffer)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one sign result line, got %d: %q", len(lines), buffer.String())
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[0], "sign_execute ")), &event); err != nil {
		t.Fatalf("result line is not JSON: %v: %q", err, lines[0])
	}
	if event["activity_id"] != float64(100) || event["user_uid"] != float64(1) ||
		event["sign_type"] != float64(xxt.SignNormal) || event["result_code"] != "success" ||
		event["attempt"] != float64(1) || event["upstream_status"] != float64(200) {
		t.Fatalf("unexpected result fields: %v", event)
	}
	if _, ok := event["duration_ms"]; !ok {
		t.Fatalf("missing duration_ms: %v", event)
	}
	for _, forbidden := range []string{"password", "token", "photo", "client_id", "clientId", "special"} {
		for key := range event {
			if strings.Contains(strings.ToLower(key), strings.ToLower(forbidden)) {
				t.Fatalf("sensitive field %q leaked: %v", key, event)
			}
		}
	}

	snapshot := h.signService.MetricsSnapshot()
	if snapshot.Total != 1 || snapshot.Success != 1 || snapshot.ResultCodes["success"] != 1 {
		t.Fatalf("metrics not updated: %+v", snapshot)
	}
}

func TestExecuteRateLimitReturns429AndLogsOnce(t *testing.T) {
	_, router, upstream, h := handlerSignFixtureParts(t)
	h.SetSignLimiter(ratelimit.New(1, 1, 10))
	buffer := captureSignLog(t)
	upstream.signResults = []xxt.SignResult{
		{Kind: xxt.SignResultSuccess, Attempts: 1, HTTPStatus: 200},
		{Kind: xxt.SignResultSuccess, Attempts: 1, HTTPStatus: 200},
	}
	payload := gin.H{"activity_id": 100, "course_id": 10, "class_id": 20, "sign_type": xxt.SignNormal}

	first := accessRequest(t, router, http.MethodPost, "/execute", payload)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := accessRequest(t, router, http.MethodPost, "/execute", payload)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	if len(upstream.signed) != 1 {
		t.Fatalf("rate-limited request reached upstream: %d calls", len(upstream.signed))
	}
	if lines := signResultLines(buffer); len(lines) != 2 {
		t.Fatalf("expected two result lines, got %d: %q", len(lines), buffer.String())
	}
	snapshot := h.signService.MetricsSnapshot()
	if snapshot.Total != 2 || snapshot.ResultCodes["rate_limited"] != 1 {
		t.Fatalf("rate limit not measured: %+v", snapshot)
	}
}

func TestExecuteCaptchaOutcomeIsMeasured(t *testing.T) {
	_, router, upstream, h := handlerSignFixtureParts(t)
	upstream.signResults = []xxt.SignResult{{
		Kind:             xxt.SignResultSuccess,
		Attempts:         2,
		HTTPStatus:       200,
		CaptchaAttempted: true,
		CaptchaPassed:    true,
	}}

	response := accessRequest(t, router, http.MethodPost, "/execute", gin.H{
		"activity_id": 100, "course_id": 10, "class_id": 20, "sign_type": xxt.SignNormal,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	snapshot := h.signService.MetricsSnapshot()
	if snapshot.ValidateTriggered != 1 || snapshot.CaptchaPassed != 1 || snapshot.CaptchaPassRate != 1 {
		t.Fatalf("captcha metrics: %+v", snapshot)
	}
}

func TestExecuteUpstream403IsMeasured(t *testing.T) {
	_, router, upstream, h := handlerSignFixtureParts(t)
	upstream.signResults = []xxt.SignResult{{
		Kind:       xxt.SignResultSessionExpired,
		Attempts:   1,
		HTTPStatus: 403,
	}}

	response := accessRequest(t, router, http.MethodPost, "/execute", gin.H{
		"activity_id": 100, "course_id": 10, "class_id": 20, "sign_type": xxt.SignNormal,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	snapshot := h.signService.MetricsSnapshot()
	if snapshot.Upstream403 != 1 || snapshot.Upstream403Rate != 1 || snapshot.ResultCodes["session_expired"] != 1 {
		t.Fatalf("403 metrics: %+v", snapshot)
	}
}
