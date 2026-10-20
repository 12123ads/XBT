package xxt

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestPreSignPostsExtForEverySignType pins the handshake shape: POST carrying
// the opaque ext in the body, fixed parameters in the query, and rcode only
// for QR activities.
func TestPreSignPostsExtForEverySignType(t *testing.T) {
	cases := []struct {
		name     string
		signType int
		code     string
		enc      string
		wantRC   string
	}{
		{name: "normal", signType: SignNormal},
		{name: "qr", signType: SignQRCode, code: "abc", enc: "xyz-enc", wantRC: "SIGNIN:aid=100&source=15&Code=abc&enc=xyz-enc"},
		{name: "gesture", signType: SignGesture},
		{name: "location", signType: SignLocation},
		{name: "sign code", signType: SignCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests []*http.Request
			var bodies []string
			transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/newsign/preSign" {
					raw, _ := io.ReadAll(req.Body)
					requests = append(requests, req)
					bodies = append(bodies, string(raw))
				}
				return learningTestResponse(req, http.StatusOK, "<html></html>"), nil
			})
			client := newSignResultTestClient(t, transport)
			fixed := signResultTestFixed()
			fixed.Ext = "opaque ext&value"

			result, err := client.PreSign(captchaTestMobile, captchaTestPassword, fixed, tc.signType, tc.code, tc.enc)
			if err != nil {
				t.Fatalf("PreSign error = %v", err)
			}
			if result.Status != PreSignReady {
				t.Fatalf("status = %q, want ready", result.Status)
			}
			if len(requests) != 1 {
				t.Fatalf("preSign requests = %d, want 1", len(requests))
			}
			req := requests[0]
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.Method)
			}
			body, err := url.ParseQuery(bodies[0])
			if err != nil {
				t.Fatalf("body %q: %v", bodies[0], err)
			}
			if got := body.Get("ext"); got != fixed.Ext {
				t.Fatalf("ext = %q, want %q", got, fixed.Ext)
			}
			if req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Fatalf("content-type = %q", req.Header.Get("Content-Type"))
			}
			query := req.URL.Query()
			want := map[string]string{
				"courseId":        "200",
				"classId":         "300",
				"activePrimaryId": "100",
				"uid":             "1",
				"appType":         "15",
			}
			for key, value := range want {
				if got := query.Get(key); got != value {
					t.Fatalf("query %s = %q, want %q (%s)", key, got, value, req.URL)
				}
			}
			if got := query.Get("rcode"); got != tc.wantRC {
				t.Fatalf("rcode = %q, want %q", got, tc.wantRC)
			}
		})
	}
}

func TestParsePreSignBodyClassifiesUpstreamState(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want PreSignStatus
	}{
		{name: "normal page", raw: `<html><script>var signstatus = 0;</script></html>`, want: PreSignReady},
		{name: "empty body", raw: "", want: PreSignReady},
		{name: "closed activity", raw: `<html>下次早点哦</html>`, want: PreSignExpired},
		{name: "primary attend signed", raw: `{"primaryAttend":{"id":1,"status":2}}`, want: PreSignAlreadySigned},
		{name: "signstatus pending view", raw: `<script>signstatus = 1;</script>`, want: PreSignAlreadySigned},
		{name: "signstatus late", raw: `<script>signstatus = 3;</script>`, want: PreSignAlreadySigned},
		{name: "signstatus signed", raw: `<script>signstatus = 9;</script>`, want: PreSignAlreadySigned},
		{name: "expired wins over signed marker", raw: `{"primaryAttend":{"status":2}} 下次早点哦`, want: PreSignExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsePreSignBody(tc.raw)
			if got.Status != tc.want {
				t.Fatalf("ParsePreSignBody(%q).Status = %q, want %q", tc.raw, got.Status, tc.want)
			}
			if got.Status == PreSignExpired && got.Message == "" {
				t.Fatalf("expired result must carry a readable message")
			}
		})
	}
}

type preSignLoginFixture struct {
	preSignCalls []string
	loginCalls   int
	preSign      func(call int) (*http.Response, error)
	loginStatus  bool
}

func (f *preSignLoginFixture) transport(t *testing.T) learningTestTransport {
	t.Helper()
	return learningTestTransport(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "passport2.chaoxing.com" && req.URL.Path == "/fanyalogin":
			f.loginCalls++
			resp := learningTestResponse(req, http.StatusOK, fmt.Sprintf(`{"status":%t}`, f.loginStatus))
			resp.Header.Add("Set-Cookie", "UID=42; Domain=.chaoxing.com; Path=/")
			return resp, nil
		case req.URL.Host == "i.chaoxing.com" && req.URL.Path == "/base":
			resp := learningTestResponse(req, http.StatusOK, `<p class="user-name">张三</p><img class="icon-head" src="avatar.png">`)
			resp.Header.Add("Set-Cookie", "UID=42; Domain=.chaoxing.com; Path=/")
			return resp, nil
		case req.URL.Path == "/newsign/preSign":
			raw, _ := io.ReadAll(req.Body)
			f.preSignCalls = append(f.preSignCalls, string(raw))
			return f.preSign(len(f.preSignCalls))
		}
		return learningTestResponse(req, http.StatusOK, ""), nil
	})
}

// TestPreSignReloginsOnceAfterForbidden verifies the recovery budget: a 403
// triggers exactly one forced login and one retry; a second 403 ends with
// ErrSessionExpired instead of looping.
func TestPreSignReloginsOnceAfterForbidden(t *testing.T) {
	fixture := &preSignLoginFixture{
		loginStatus: true,
		preSign: func(call int) (*http.Response, error) {
			if call == 1 {
				return learningTestResponse(nil, http.StatusForbidden, "forbidden"), nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: nil}, nil
		},
	}
	// learningTestResponse needs the request; rebuild responses with the actual
	// request through a wrapper instead.
	transport := fixture.transport(t)
	client := newSignResultTestClient(t, transport)
	fixed := signResultTestFixed()
	fixed.Ext = "ext-1"

	result, err := client.PreSign(captchaTestMobile, captchaTestPassword, fixed, SignNormal, "", "")
	if err != nil {
		t.Fatalf("PreSign error = %v", err)
	}
	if result.Status != PreSignReady {
		t.Fatalf("status = %q, want ready", result.Status)
	}
	if len(fixture.preSignCalls) != 2 || fixture.loginCalls != 1 {
		t.Fatalf("preSign=%d login=%d, want 2/1", len(fixture.preSignCalls), fixture.loginCalls)
	}
	for i, body := range fixture.preSignCalls {
		if !strings.Contains(body, "ext=ext-1") {
			t.Fatalf("attempt %d body = %q, want ext", i+1, body)
		}
	}

	second := &preSignLoginFixture{
		loginStatus: true,
		preSign: func(int) (*http.Response, error) {
			return learningTestResponse(nil, http.StatusForbidden, ""), nil
		},
	}
	client = newSignResultTestClient(t, second.transport(t))
	_, err = client.PreSign(captchaTestMobile, captchaTestPassword, fixed, SignNormal, "", "")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("second forbidden error = %v, want ErrSessionExpired", err)
	}
	if len(second.preSignCalls) != 2 || second.loginCalls != 1 {
		t.Fatalf("preSign=%d login=%d, want 2/1 (no third attempt)", len(second.preSignCalls), second.loginCalls)
	}

	failed := &preSignLoginFixture{
		loginStatus: false,
		preSign: func(int) (*http.Response, error) {
			return learningTestResponse(nil, http.StatusForbidden, ""), nil
		},
	}
	client = newSignResultTestClient(t, failed.transport(t))
	_, err = client.PreSign(captchaTestMobile, captchaTestPassword, fixed, SignNormal, "", "")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("failed relogin error = %v, want ErrSessionExpired", err)
	}
	if len(failed.preSignCalls) != 1 || failed.loginCalls != 1 {
		t.Fatalf("preSign=%d login=%d, want 1/1", len(failed.preSignCalls), failed.loginCalls)
	}
}

func TestPreSignMapsNoPermissionRedirect(t *testing.T) {
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/newsign/preSign" {
			return learningTestResponse(req, http.StatusOK, "校验失败，未查询到活动数据"), nil
		}
		return learningTestResponse(req, http.StatusOK, ""), nil
	})
	client := newSignResultTestClient(t, transport)
	result, err := client.PreSign(captchaTestMobile, captchaTestPassword, signResultTestFixed(), SignNormal, "", "")
	if err != nil {
		t.Fatalf("PreSign error = %v", err)
	}
	if result.Status != PreSignNoPermission || result.Message == "" {
		t.Fatalf("result = %+v, want no_permission with message", result)
	}
}
