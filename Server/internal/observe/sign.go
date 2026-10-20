// Package observe owns the sign execution ledger: one structured result line
// per execute, and the in-process counters used by the admin metrics endpoint.
//
// Events never carry credentials, captcha tokens, face photos or raw upstream
// bodies. Only identifiers and enum-like result codes are logged.
package observe

import (
	"encoding/json"
	"log"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"xbt2/server/internal/im"
)

const maxLatencySamples = 2048

// SignEvent is the single structured record emitted for one execute call.
// Field names are part of the log contract; keep them stable.
type SignEvent struct {
	ActivityID     int64  `json:"activity_id"`
	UserUID        int64  `json:"user_uid"`
	SignType       int    `json:"sign_type"`
	ResultCode     string `json:"result_code"`
	Attempt        int    `json:"attempt"`
	UpstreamStatus int    `json:"upstream_status"`
	DurationMS     int64  `json:"duration_ms"`
	Success        bool   `json:"success"`
	AlreadySigned  bool   `json:"already_signed"`
	// ValidateTriggered means upstream asked for a captcha (validate_), even
	// when the challenge could not be solved.
	ValidateTriggered bool `json:"validate_triggered"`
	// CaptchaPassed means the validate retry was accepted by upstream.
	CaptchaPassed bool `json:"captcha_passed"`
	// LocationError means an upstream errorLocation_ response was observed.
	LocationError bool `json:"location_error"`
	Upstream403   bool `json:"upstream_403"`
	// HasCxcid reports whether the face request carried the S13 device
	// signature. The clientId itself is never logged.
	HasCxcid bool `json:"has_cxcid"`
}

// SignSnapshot is the JSON form returned by the admin metrics endpoint.
type SignSnapshot struct {
	Total               int64            `json:"total"`
	Success             int64            `json:"success"`
	AlreadySigned       int64            `json:"already_signed"`
	SuccessRate         float64          `json:"success_rate"`
	ResultCodes         map[string]int64 `json:"result_codes"`
	ValidateTriggered   int64            `json:"validate_triggered"`
	ValidateTriggerRate float64          `json:"validate_trigger_rate"`
	CaptchaPassed       int64            `json:"captcha_passed"`
	CaptchaPassRate     float64          `json:"captcha_pass_rate"`
	LocationErrors      int64            `json:"location_errors"`
	ErrorLocationRate   float64          `json:"error_location_rate"`
	Upstream403         int64            `json:"upstream_403"`
	Upstream403Rate     float64          `json:"upstream_403_rate"`
	P95DurationMS       float64          `json:"p95_duration_ms"`
	LatencySamples      int              `json:"latency_samples"`

	IMFetchRequests     int64            `json:"im_fetch_requests"`
	IMFetchSkippedStale int64            `json:"im_fetch_skipped_stale"`
	IMFetchTimeout      int64            `json:"im_fetch_timeout"`
	IMFetchErrorCodes   map[string]int64 `json:"im_fetch_error_codes"`
	IMFetchLatencyP95MS float64          `json:"im_fetch_latency_p95_ms"`
	IMFetchSamples      int              `json:"im_fetch_latency_samples"`
	// IMAccounts keeps the same counters per account, which is what the IM
	// discovery addendum asks for. Keys are decimal uids.
	IMAccounts          map[string]IMAccountSnapshot `json:"im_accounts"`
	IMAccountsTruncated bool                         `json:"im_accounts_truncated"`

	// ActivitiesP95MS measures the whole /sign/activities handler, so it
	// includes the synchronous IM fetch.
	ActivitiesP95MS   float64 `json:"activities_p95_ms"`
	ActivitiesSamples int     `json:"activities_samples"`
}

// IMAccountSnapshot is the per-account IM discovery ledger.
type IMAccountSnapshot struct {
	Requests      int64            `json:"im_fetch_requests"`
	SkippedStale  int64            `json:"im_fetch_skipped_stale"`
	Timeout       int64            `json:"im_fetch_timeout"`
	ErrorCodes    map[string]int64 `json:"im_fetch_error_codes"`
	LastErrorCode string           `json:"im_fetch_error_code"`
	LatencyP95MS  float64          `json:"im_fetch_latency_ms_p95"`
	Samples       int              `json:"im_fetch_latency_samples"`
}

const maxIMAccounts = 512

type imAccountCounters struct {
	requests     int64
	skippedStale int64
	timeouts     int64
	errorCodes   map[string]int64
	lastError    string
	durations    []int64
	next         int
}

// SignMetrics is safe for concurrent use. Latency percentiles are computed
// from a bounded ring of recent samples; counters are cumulative for process
// lifetime.
type SignMetrics struct {
	mu sync.Mutex

	total             int64
	success           int64
	alreadySigned     int64
	resultCodes       map[string]int64
	validateTriggered int64
	captchaPassed     int64
	locationErrors    int64
	upstream403       int64

	durations    []int64
	durationNext int

	imRequests  int64
	imSkipped   int64
	imTimeouts  int64
	imErrors    map[string]int64
	imDurations []int64
	imNext      int
	imAccounts  map[int64]*imAccountCounters
	imTruncated bool

	activitiesDurations []int64
	activitiesNext      int
}

func NewSignMetrics() *SignMetrics {
	return &SignMetrics{
		resultCodes: make(map[string]int64),
		imErrors:    make(map[string]int64),
		imAccounts:  make(map[int64]*imAccountCounters),
	}
}

// ObserveSign emits exactly one structured log line and updates counters.
func (m *SignMetrics) ObserveSign(event SignEvent) {
	if event.DurationMS < 0 {
		event.DurationMS = 0
	}
	payload, err := json.Marshal(event)
	if err != nil {
		// The struct only contains scalar values; keep the fallback explicit so
		// a future field cannot silently drop the required result line.
		log.Printf("sign_execute {\"activity_id\":%d,\"user_uid\":%d,\"sign_type\":%d,\"result_code\":%q,\"attempt\":%d,\"upstream_status\":%d,\"duration_ms\":%d}",
			event.ActivityID, event.UserUID, event.SignType, event.ResultCode, event.Attempt, event.UpstreamStatus, event.DurationMS)
	} else {
		log.Printf("sign_execute %s", payload)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.total++
	if event.Success {
		m.success++
	}
	if event.AlreadySigned {
		m.alreadySigned++
	}
	m.resultCodes[event.ResultCode]++
	if event.ValidateTriggered {
		m.validateTriggered++
	}
	if event.CaptchaPassed {
		m.captchaPassed++
	}
	if event.LocationError {
		m.locationErrors++
	}
	if event.Upstream403 || event.UpstreamStatus == 403 {
		m.upstream403++
	}
	m.durations = appendSample(m.durations, &m.durationNext, event.DurationMS)
}

// ObserveActivities records the full /sign/activities handler duration,
// including the synchronous IM fetch.
func (m *SignMetrics) ObserveActivities(duration time.Duration) {
	ms := duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activitiesDurations = appendSample(m.activitiesDurations, &m.activitiesNext, ms)
}

// ObserveIM records the outcome of one on-demand IM refresh decision. fetched
// is false when the interval gate or the per-account lock skipped upstream.
func (m *SignMetrics) ObserveIM(uid int64, enabled, fetched bool, errorCode string, latency time.Duration) {
	if !enabled {
		return
	}
	ms := latency.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	account := m.imAccounts[uid]
	if account == nil {
		if len(m.imAccounts) >= maxIMAccounts {
			m.imTruncated = true
		} else {
			account = &imAccountCounters{errorCodes: make(map[string]int64)}
			m.imAccounts[uid] = account
		}
	}
	if account != nil {
		if errorCode != "" {
			account.lastError = errorCode
		}
	}
	if !fetched {
		m.imSkipped++
		if account != nil {
			account.skippedStale++
		}
		return
	}
	// A successful fetch clears the previous error for this account.
	if account != nil {
		account.lastError = errorCode
	}
	m.imRequests++
	m.imDurations = appendSample(m.imDurations, &m.imNext, ms)
	if account != nil {
		account.requests++
		account.durations = appendSample(account.durations, &account.next, ms)
	}
	if errorCode == "" {
		return
	}
	m.imErrors[errorCode]++
	if errorCode == im.CodeTimeout {
		m.imTimeouts++
	}
	if account != nil {
		account.errorCodes[errorCode]++
		if errorCode == im.CodeTimeout {
			account.timeouts++
		}
	}
}

func (m *SignMetrics) Snapshot() SignSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := SignSnapshot{
		Total:               m.total,
		Success:             m.success,
		AlreadySigned:       m.alreadySigned,
		SuccessRate:         ratio(m.success, m.total),
		ResultCodes:         copyCounts(m.resultCodes),
		ValidateTriggered:   m.validateTriggered,
		ValidateTriggerRate: ratio(m.validateTriggered, m.total),
		CaptchaPassed:       m.captchaPassed,
		CaptchaPassRate:     ratio(m.captchaPassed, m.validateTriggered),
		LocationErrors:      m.locationErrors,
		ErrorLocationRate:   ratio(m.locationErrors, m.total),
		Upstream403:         m.upstream403,
		Upstream403Rate:     ratio(m.upstream403, m.total),
		P95DurationMS:       percentile95(m.durations),
		LatencySamples:      len(m.durations),
		IMFetchRequests:     m.imRequests,
		IMFetchSkippedStale: m.imSkipped,
		IMFetchTimeout:      m.imTimeouts,
		IMFetchErrorCodes:   copyCounts(m.imErrors),
		IMFetchLatencyP95MS: percentile95(m.imDurations),
		IMFetchSamples:      len(m.imDurations),
		ActivitiesP95MS:     percentile95(m.activitiesDurations),
		ActivitiesSamples:   len(m.activitiesDurations),
	}
	snapshot.IMAccountsTruncated = m.imTruncated
	snapshot.IMAccounts = make(map[string]IMAccountSnapshot, len(m.imAccounts))
	for uid, account := range m.imAccounts {
		snapshot.IMAccounts[strconv.FormatInt(uid, 10)] = IMAccountSnapshot{
			Requests:      account.requests,
			SkippedStale:  account.skippedStale,
			Timeout:       account.timeouts,
			ErrorCodes:    copyCounts(account.errorCodes),
			LastErrorCode: account.lastError,
			LatencyP95MS:  percentile95(account.durations),
			Samples:       len(account.durations),
		}
	}
	return snapshot
}

func appendSample(samples []int64, next *int, value int64) []int64 {
	if len(samples) < maxLatencySamples {
		samples = append(samples, value)
		*next = len(samples) % maxLatencySamples
		return samples
	}
	samples[*next] = value
	*next = (*next + 1) % maxLatencySamples
	return samples
}

func percentile95(samples []int64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return float64(sorted[index])
}

func ratio(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func copyCounts(counts map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(counts))
	for key, value := range counts {
		out[key] = value
	}
	return out
}
