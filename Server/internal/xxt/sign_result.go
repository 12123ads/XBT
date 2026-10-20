package xxt

import (
	"strconv"
	"strings"
)

// SignResultKind is the typed outcome of one stuSignajax exchange. The raw
// response is only parsed here; callers must branch on the kind, never on
// substrings of the body.
type SignResultKind string

const (
	SignResultSuccess            SignResultKind = "success"
	SignResultSuccess2           SignResultKind = "success2"
	SignResultAlreadySigned      SignResultKind = "already_signed"
	SignResultCaptchaRequired    SignResultKind = "captcha_required"
	SignResultFaceRequired       SignResultKind = "face_required"
	SignResultLocationOutOfRange SignResultKind = "location_out_of_range"
	SignResultQRExpired          SignResultKind = "qr_expired"
	SignResultSessionExpired     SignResultKind = "session_expired"
	SignResultUpstreamError      SignResultKind = "upstream_error"
)

// SignResult is the parsed state-machine result.
//
//   - Enc2 is the challenge token carried by validate_/checkFace_ responses.
//   - DistanceM is the distance upstream reported in errorLocation_<meters>.
//   - Message keeps the raw upstream text for upstream_error and the hint
//     carried by [face]<hint>.
type SignResult struct {
	Kind      SignResultKind
	Enc2      string
	DistanceM float64
	Message   string
}

const (
	alreadySignedMarker = "您已签到过了"
	qrExpiredMarker     = "签到失败，请重新扫描"
)

// ParseSignResult maps every upstream stuSignajax body known from CSF
// (9ae5398, ChaoxingSigner.checkSignResult) to a typed result. Unrecognised
// bodies are upstream_error with the raw body as message, never a success.
func ParseSignResult(raw string) SignResult {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "success":
		return SignResult{Kind: SignResultSuccess}
	case "success2":
		// Late or already-closed activity: upstream accepted the sign.
		return SignResult{Kind: SignResultSuccess2}
	case "":
		return SignResult{Kind: SignResultUpstreamError, Message: "上游返回为空"}
	}

	switch {
	case strings.Contains(raw, alreadySignedMarker):
		return SignResult{Kind: SignResultAlreadySigned, Message: raw}
	case strings.HasPrefix(raw, "validate"):
		// "validate_abc" carries the captcha challenge token; a bare
		// "validate" has none.
		enc2 := ""
		if strings.HasPrefix(raw, "validate_") {
			enc2 = strings.TrimPrefix(raw, "validate_")
		}
		return SignResult{Kind: SignResultCaptchaRequired, Enc2: enc2}
	case strings.HasPrefix(raw, "[face]"):
		// CSF treats the suffix of [face] as an explanation, while
		// checkFace_ carries the enc2 token.
		return SignResult{Kind: SignResultFaceRequired, Message: strings.TrimPrefix(raw, "[face]")}
	case strings.HasPrefix(raw, "checkFace_"):
		return SignResult{Kind: SignResultFaceRequired, Enc2: strings.TrimPrefix(raw, "checkFace_")}
	case strings.HasPrefix(raw, "errorLocation"):
		distance, _ := strconv.ParseFloat(strings.TrimPrefix(raw, "errorLocation_"), 64)
		return SignResult{Kind: SignResultLocationOutOfRange, DistanceM: distance, Message: raw}
	case strings.Contains(raw, qrExpiredMarker):
		return SignResult{Kind: SignResultQRExpired, Message: raw}
	case strings.Contains(raw, "请重新登录"), strings.Contains(raw, "登录已过期"):
		return SignResult{Kind: SignResultSessionExpired, Message: raw}
	default:
		return SignResult{Kind: SignResultUpstreamError, Message: raw}
	}
}
