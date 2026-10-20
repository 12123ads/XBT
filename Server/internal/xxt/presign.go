package xxt

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// ErrSessionExpired marks an upstream 403 that survived one forced re-login.
// Callers map it to their own session_expired result; the client never retries
// a third time.
var ErrSessionExpired = errors.New("学习通登录已过期")

// PreSignStatus is the typed outcome of a preSign request. It is derived only
// from the response body; transport/session failures are returned as errors.
type PreSignStatus string

const (
	PreSignReady         PreSignStatus = "ready"
	PreSignExpired       PreSignStatus = "expired"
	PreSignAlreadySigned PreSignStatus = "already_signed"
	PreSignNoPermission  PreSignStatus = "no_permission"
)

type PreSignResult struct {
	Status  PreSignStatus
	Message string
}

const (
	preSignExpiredMarker    = "下次早点哦"
	preSignNoPermissionMark = "校验失败，未查询到活动数据"
)

// signedStatuses mirrors the upstream page's signed status enumeration
// (1=pending view, 2=signed, 3=late/closed, 9=already signed).
var signedStatuses = map[int]struct{}{1: {}, 2: {}, 3: {}, 9: {}}

var (
	primaryAttendStatusPattern = regexp.MustCompile(`"primaryAttend"\s*:\s*\{[^{}]*?"status"\s*:\s*(\d+)`)
	signStatusPattern          = regexp.MustCompile(`signstatus\s*=\s*(\d+)`)
)

// ParsePreSignBody classifies the preSign HTML. Expiry wins over the signed
// status because a closed activity may still render a signed marker; an
// unrecognised body is ready, matching the upstream page's normal state.
func ParsePreSignBody(raw string) PreSignResult {
	if strings.Contains(raw, preSignExpiredMarker) {
		return PreSignResult{Status: PreSignExpired, Message: "签到已截止"}
	}
	if status, ok := extractPreSignStatus(raw); ok {
		if _, signed := signedStatuses[status]; signed {
			return PreSignResult{Status: PreSignAlreadySigned, Message: "该同学已在学习通签到"}
		}
	}
	return PreSignResult{Status: PreSignReady}
}

func extractPreSignStatus(raw string) (int, bool) {
	for _, re := range []*regexp.Regexp{primaryAttendStatusPattern, signStatusPattern} {
		m := re.FindStringSubmatch(raw)
		if len(m) != 2 {
			continue
		}
		status, err := strconv.Atoi(m[1])
		if err == nil {
			return status, true
		}
	}
	return 0, false
}
