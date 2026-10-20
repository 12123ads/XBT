package observe

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"xbt2/server/internal/im"
)

func TestSignMetricsAggregatesRatesAndP95(t *testing.T) {
	metrics := NewSignMetrics()
	events := []SignEvent{
		{ActivityID: 1, UserUID: 2, SignType: 0, ResultCode: "success", Attempt: 1, UpstreamStatus: 200, DurationMS: 100, Success: true, ValidateTriggered: true, CaptchaPassed: true},
		{ActivityID: 1, UserUID: 2, SignType: 0, ResultCode: "captcha_required", Attempt: 1, UpstreamStatus: 200, DurationMS: 300, ValidateTriggered: true},
		{ActivityID: 1, UserUID: 3, SignType: 4, ResultCode: "location_out_of_range", Attempt: 2, UpstreamStatus: 200, DurationMS: 200, LocationError: true},
		{ActivityID: 1, UserUID: 4, SignType: 2, ResultCode: "session_expired", Attempt: 1, UpstreamStatus: 403, DurationMS: 400, Upstream403: true},
	}
	for _, event := range events {
		metrics.ObserveSign(event)
	}

	snapshot := metrics.Snapshot()
	if snapshot.Total != 4 || snapshot.Success != 1 {
		t.Fatalf("counts: total=%d success=%d", snapshot.Total, snapshot.Success)
	}
	if snapshot.SuccessRate != 0.25 || snapshot.ValidateTriggerRate != 0.5 ||
		snapshot.CaptchaPassRate != 0.5 || snapshot.ErrorLocationRate != 0.25 || snapshot.Upstream403Rate != 0.25 {
		t.Fatalf("rates: %+v", snapshot)
	}
	if snapshot.ResultCodes["captcha_required"] != 1 || snapshot.ResultCodes["success"] != 1 {
		t.Fatalf("result codes: %+v", snapshot.ResultCodes)
	}
	if snapshot.P95DurationMS != 400 || snapshot.LatencySamples != 4 {
		t.Fatalf("p95: %v samples=%d", snapshot.P95DurationMS, snapshot.LatencySamples)
	}
}

func TestSignMetricsLogsExactlyOneSanitizedLine(t *testing.T) {
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })

	metrics := NewSignMetrics()
	metrics.ObserveSign(SignEvent{
		ActivityID:     100,
		UserUID:        7,
		SignType:       4,
		ResultCode:     "success",
		Attempt:        2,
		UpstreamStatus: 200,
		DurationMS:     123,
		Success:        true,
		HasCxcid:       true,
	})

	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d: %q", len(lines), buffer.String())
	}
	const marker = "sign_execute "
	index := strings.Index(lines[0], marker)
	if index < 0 {
		t.Fatalf("missing prefix: %q", lines[0])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(lines[0][index+len(marker):])), &payload); err != nil {
		t.Fatalf("log line is not JSON: %v: %q", err, lines[0])
	}
	for _, key := range []string{"activity_id", "user_uid", "sign_type", "result_code", "attempt", "upstream_status", "duration_ms", "has_cxcid"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing required field %q: %v", key, payload)
		}
	}
	for _, forbidden := range []string{"password", "token", "photo", "client_id", "clientId", "sc", "mobile"} {
		for key := range payload {
			if strings.Contains(strings.ToLower(key), strings.ToLower(forbidden)) {
				t.Fatalf("sensitive field %q leaked: %v", key, payload)
			}
		}
	}
}

func TestSignMetricsIMAndActivitiesLatency(t *testing.T) {
	metrics := NewSignMetrics()
	metrics.ObserveIM(7, true, false, "", 5*time.Millisecond)
	metrics.ObserveIM(7, true, true, im.CodeTimeout, 5000*time.Millisecond)
	metrics.ObserveIM(7, true, true, "", 100*time.Millisecond)
	metrics.ObserveIM(8, false, true, "", time.Second)
	metrics.ObserveActivities(6000 * time.Millisecond)
	metrics.ObserveActivities(200 * time.Millisecond)

	snapshot := metrics.Snapshot()
	if snapshot.IMFetchRequests != 2 || snapshot.IMFetchSkippedStale != 1 || snapshot.IMFetchTimeout != 1 {
		t.Fatalf("im counters: %+v", snapshot)
	}
	if snapshot.IMFetchErrorCodes[im.CodeTimeout] != 1 {
		t.Fatalf("im error codes: %+v", snapshot.IMFetchErrorCodes)
	}
	if snapshot.IMFetchLatencyP95MS != 5000 {
		t.Fatalf("im p95: %v", snapshot.IMFetchLatencyP95MS)
	}
	if snapshot.ActivitiesP95MS != 6000 {
		t.Fatalf("activities p95: %v", snapshot.ActivitiesP95MS)
	}
	account := snapshot.IMAccounts["7"]
	if account.Requests != 2 || account.SkippedStale != 1 || account.Timeout != 1 ||
		account.LatencyP95MS != 5000 || account.LastErrorCode != "" {
		t.Fatalf("per-account im metrics: %+v", account)
	}
	if snapshot.IMAccounts["8"].Requests != 0 {
		t.Fatalf("disabled account must not be recorded: %+v", snapshot.IMAccounts)
	}
}
