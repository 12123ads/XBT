package xxt

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseSignResultMapsEveryUpstreamState(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		kind     SignResultKind
		enc2     string
		distance float64
		message  string
	}{
		{name: "success", raw: "success", kind: SignResultSuccess},
		{name: "late success", raw: "success2", kind: SignResultSuccess2},
		{name: "already signed", raw: "您已签到过了", kind: SignResultAlreadySigned},
		{name: "captcha with token", raw: "validate_abc", kind: SignResultCaptchaRequired, enc2: "abc"},
		{name: "captcha without token", raw: "validate", kind: SignResultCaptchaRequired},
		{name: "face marker", raw: "[face]请正对摄像头", kind: SignResultFaceRequired, message: "请正对摄像头"},
		{name: "face challenge", raw: "checkFace_face-enc", kind: SignResultFaceRequired, enc2: "face-enc"},
		{name: "location out of range", raw: "errorLocation_12.5", kind: SignResultLocationOutOfRange, distance: 12.5},
		{name: "location without distance", raw: "errorLocation", kind: SignResultLocationOutOfRange},
		{name: "qr expired", raw: "签到失败，请重新扫描。", kind: SignResultQRExpired},
		{name: "session expired", raw: "请重新登录", kind: SignResultSessionExpired},
		{name: "unknown body", raw: "  服务器开小差了 ", kind: SignResultUpstreamError, message: "服务器开小差了"},
		{name: "empty body", raw: "", kind: SignResultUpstreamError, message: "上游返回为空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSignResult(tc.raw)
			if got.Kind != tc.kind || got.Enc2 != tc.enc2 || got.DistanceM != tc.distance {
				t.Fatalf("ParseSignResult(%q) = %+v, want kind=%s enc2=%q distance=%v", tc.raw, got, tc.kind, tc.enc2, tc.distance)
			}
			if tc.message != "" && got.Message != tc.message {
				t.Fatalf("ParseSignResult(%q) message = %q, want %q", tc.raw, got.Message, tc.message)
			}
		})
	}
}

func newSignResultTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		aesKey:   "1234567890123456",
		mobileUA: "sign-result-test",
		http:     &http.Client{Transport: transport, Timeout: 5 * time.Second},
		sessions: map[string]*Session{
			captchaTestMobile: {
				Mobile:      captchaTestMobile,
				Password:    captchaTestPassword,
				UID:         1,
				Name:        "张三",
				Jar:         jar,
				LastLoginAt: time.Now(),
			},
		},
	}
}

func signResultTestFixed() FixedParams {
	return FixedParams{ActiveID: 100, UID: 1, CourseID: 200, ClassID: 300}
}

// TestSignCaptchaRetrySendsValidateAndEnc2Once verifies the exact protocol
// requirement: validate_abc triggers one second submission carrying both
// validate and enc2=abc, and a second captcha challenge ends the attempt.
func TestSignCaptchaRetrySendsValidateAndEnc2Once(t *testing.T) {
	shade, cutout, _ := captchaTestImages(t)

	var signRequests []url.Values
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/pptSign/stuSignajax":
			signRequests = append(signRequests, req.URL.Query())
			if len(signRequests) == 1 {
				return learningTestResponse(req, http.StatusOK, "validate_abc"), nil
			}
			return learningTestResponse(req, http.StatusOK, "validate_again"), nil
		case "/captcha/get/conf":
			return learningTestResponse(req, http.StatusOK, fmt.Sprintf(`cx_captcha_function({"t":%d})`, captchaTestConfT)), nil
		case "/captcha/get/verification/image":
			return learningTestResponse(req, http.StatusOK,
				`cx_captcha_function({"token":"token-1","imageVerificationVo":{"shadeImage":"https://captcha.chaoxing.com/shade.png","cutoutImage":"https://captcha.chaoxing.com/cutout.png"}})`), nil
		case "/shade.png":
			return captchaTestPNG(req, shade), nil
		case "/cutout.png":
			return captchaTestPNG(req, cutout), nil
		case "/captcha/check/verification/result":
			return learningTestResponse(req, http.StatusOK,
				`cx_captcha_function({"result":true,"extraData":"{\"validate\":\"validate-ok\"}"})`), nil
		}
		return nil, fmt.Errorf("unexpected request: %s", req.URL)
	})

	client := newSignResultTestClient(t, transport)
	result, err := client.Sign(captchaTestMobile, captchaTestPassword, signResultTestFixed(), SignNormal, nil)
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	if result.Kind != SignResultCaptchaRequired || result.Enc2 != "again" {
		t.Fatalf("second captcha challenge = %+v, want captcha_required(enc2=again)", result)
	}
	if len(signRequests) != 2 {
		t.Fatalf("stuSignajax calls = %d, want exactly 2", len(signRequests))
	}
	if got := signRequests[0]; got.Has("validate") || got.Has("enc2") {
		t.Fatalf("first submission must not pre-seed captcha state: %v", got)
	}
	second := signRequests[1]
	if second.Get("validate") != "validate-ok" || second.Get("enc2") != "abc" {
		t.Fatalf("retry must carry validate and enc2=abc: %v", second)
	}
}

func TestSignMapsForbiddenResponseToSessionExpired(t *testing.T) {
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		return learningTestResponse(req, http.StatusForbidden, "<html>login</html>"), nil
	})
	client := newSignResultTestClient(t, transport)
	result, err := client.Sign(captchaTestMobile, captchaTestPassword, signResultTestFixed(), SignNormal, nil)
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	if result.Kind != SignResultSessionExpired {
		t.Fatalf("403 mapped to %+v, want session_expired", result)
	}
}

func TestSignCarriesCallerSuppliedStateOnly(t *testing.T) {
	var params []url.Values
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		params = append(params, req.URL.Query())
		return learningTestResponse(req, http.StatusOK, "success2"), nil
	})
	client := newSignResultTestClient(t, transport)
	special := map[string]interface{}{"validate": "validate_abc", "enc2": "abc"}
	result, err := client.Sign(captchaTestMobile, captchaTestPassword, signResultTestFixed(), SignNormal, special)
	if err != nil || result.Kind != SignResultSuccess2 {
		t.Fatalf("Sign = %+v err=%v, want success2", result, err)
	}
	if len(params) != 1 || params[0].Get("validate") != "validate_abc" || params[0].Get("enc2") != "abc" {
		t.Fatalf("caller state was not passed through: %v", params)
	}
}

// Defensive: an unexpected raw body must never be treated as success.
func TestParseSignResultUnknownNeverSucceeds(t *testing.T) {
	if got := ParseSignResult("SUCCESS"); got.Kind == SignResultSuccess {
		t.Fatalf("case-folded success accepted: %+v", got)
	}
	if got := ParseSignResult(strings.Repeat("x", 16)); got.Kind != SignResultUpstreamError {
		t.Fatalf("unknown body = %+v, want upstream_error", got)
	}
}
