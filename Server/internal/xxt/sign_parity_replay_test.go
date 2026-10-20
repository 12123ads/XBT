package xxt

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file is the S12 protocol replay. It drives the real xxt.Client over a
// local HTTP server (all upstream hosts are rewritten to it) so the cookie
// jar, session establishment, preSign handshake and stuSignajax state machine
// run exactly as they do in production. Fixtures are synthetic; no real
// account or upstream request is involved.

const (
	parityMobile   = "13800000042"
	parityPassword = "parity-password"
	parityAESKey   = "1234567890123456"
	parityFID      = "fid-42"
	parityUID      = int64(42)
	parityActivity = int64(100)
	parityCourse   = int64(200)
	parityClass    = int64(300)
)

type parityRequest struct {
	Host        string
	Method      string
	Path        string
	Query       url.Values
	Form        url.Values
	ContentType string
}

type parityUpstream struct {
	t        *testing.T
	signType int

	mu         sync.Mutex
	requests   []parityRequest
	unexpected []string

	loginCalls    int
	preSignCalls  int
	signCalls     int
	faceCalls     int
	analysisCalls int

	preSignBody  func(call int) (int, string)
	signBody     func(call int) (int, string)
	facePayloads []map[string]interface{}

	shade  []byte
	cutout []byte
}

func newParityUpstream(t *testing.T, signType int) *parityUpstream {
	t.Helper()
	shade, cutout, _ := captchaTestImages(t)
	return &parityUpstream{
		t:        t,
		signType: signType,
		shade:    shade,
		cutout:   cutout,
		preSignBody: func(int) (int, string) {
			return http.StatusOK, ""
		},
		signBody: func(int) (int, string) {
			return http.StatusOK, "success"
		},
	}
}

func (u *parityUpstream) detailBody() string {
	openAddress := 0
	if u.signType != SignNormal {
		openAddress = 1
	}
	return fmt.Sprintf(`{"data":{
		"starttime":1700000000000,
		"endtime":1700000600000,
		"otherId":%d,
		"ifrefreshewm":0,
		"ifphoto":0,
		"openCheckFaceFlag":0,
		"ifNeedVCode":0,
		"ifopenAddress":%d,
		"locationLatitude":31.2304,
		"locationLongitude":121.4737,
		"locationRange":200,
		"signInId":101,
		"ext":{"source":"metadata"}
	}}`, u.signType, openAddress)
}

func (u *parityUpstream) activeListBody() string {
	return fmt.Sprintf(`{"data":{"ext":{"course":"list-ext"},"activeList":[
		{"id":%d,"type":2,"status":1,"userStatus":0,"startTime":1700000000000,"endTime":1700000600000,"otherId":%d,"nameOne":"签到","name":"签到","activeType":2,"ext":{"activity":"ext-token"}}
	]}}`, parityActivity, u.signType)
}

func (u *parityUpstream) record(r *http.Request, form url.Values) {
	u.requests = append(u.requests, parityRequest{
		Host:        r.Host,
		Method:      r.Method,
		Path:        r.URL.Path,
		Query:       cloneValues(r.URL.Query()),
		Form:        cloneValues(form),
		ContentType: r.Header.Get("Content-Type"),
	})
}

func cloneValues(src url.Values) url.Values {
	out := make(url.Values, len(src))
	for key, values := range src {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func writeParityBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (u *parityUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()

	form := url.Values{}
	if r.Method == http.MethodPost {
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
	}
	u.record(r, form)

	switch r.URL.Path {
	case "/fanyalogin":
		u.loginCalls++
		w.Header().Add("Set-Cookie", "UID=42; Domain=.chaoxing.com; Path=/")
		writeParityBody(w, http.StatusOK, `{"status":true}`)
	case "/base":
		w.Header().Add("Set-Cookie", "UID=42; Domain=.chaoxing.com; Path=/")
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<p class="user-name">张三</p><img class="icon-head" src="avatar.png">`)
	case "/ppt/activeAPI/taskactivelist":
		writeParityBody(w, http.StatusOK, u.activeListBody())
	case "/v2/apis/active/getPPTActiveInfo":
		writeParityBody(w, http.StatusOK, u.detailBody())
	case "/newsign/preSign":
		u.preSignCalls++
		status, body := u.preSignBody(u.preSignCalls)
		writeParityBody(w, status, body)
	case "/pptSign/analysis", "/pptSign/analysis2":
		u.analysisCalls++
		_, _ = io.WriteString(w, "")
	case "/widget/sign/pcStuSignController/checkSignCode":
		writeParityBody(w, http.StatusOK, `{"result":1}`)
	case "/pptSign/stuSignajax":
		u.signCalls++
		status, body := u.signBody(u.signCalls)
		writeParityBody(w, status, body)
	case "/pptSign/check-face-result":
		u.faceCalls++
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("faceResult")), &payload); err != nil {
			u.unexpected = append(u.unexpected, "faceResult is not JSON: "+err.Error())
		}
		u.facePayloads = append(u.facePayloads, payload)
		writeParityBody(w, http.StatusOK, `{"enc":"face-enc-result","code":"face-code-result"}`)
	case "/captcha/get/conf":
		writeParityBody(w, http.StatusOK, fmt.Sprintf(`cx_captcha_function({"t":%d})`, captchaTestConfT))
	case "/captcha/get/verification/image":
		writeParityBody(w, http.StatusOK, `cx_captcha_function({"token":"token-1","imageVerificationVo":{"shadeImage":"https://captcha.chaoxing.com/shade.png","cutoutImage":"https://captcha.chaoxing.com/cutout.png"}})`)
	case "/shade.png":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(u.shade)
	case "/cutout.png":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(u.cutout)
	case "/captcha/check/verification/result":
		writeParityBody(w, http.StatusOK, `cx_captcha_function({"result":true,"extraData":"{\"validate\":\"validate-ok\"}"})`)
	default:
		u.unexpected = append(u.unexpected, r.Method+" "+r.URL.String())
		writeParityBody(w, http.StatusInternalServerError, `{"error":"unexpected"}`)
	}
}

// parityRewriteTransport sends every hard-coded upstream URL to the local test
// server while preserving the original Host header, so the production client
// needs no URL injection seam.
type parityRewriteTransport struct {
	target string
}

func (t parityRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	originalHost := req.URL.Host
	clone.URL.Scheme = "http"
	clone.URL.Host = t.target
	clone.Host = originalHost
	clone.RequestURI = ""
	return (&http.Transport{Proxy: nil}).RoundTrip(clone)
}

func (u *parityUpstream) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(u)
	t.Cleanup(server.Close)
	return server
}

func (u *parityUpstream) client(t *testing.T) *Client {
	t.Helper()
	server := u.server(t)
	client := New(parityAESKey, "xbt-parity/1.0", parityFID, false, 20)
	client.http.Transport = parityRewriteTransport{target: strings.TrimPrefix(server.URL, "http://")}
	client.http.Timeout = 5 * time.Second
	return client
}

func (u *parityUpstream) assertNoUnexpected(t *testing.T) {
	t.Helper()
	if len(u.unexpected) > 0 {
		t.Fatalf("unexpected upstream requests: %v", u.unexpected)
	}
}

func (u *parityUpstream) find(path string) []parityRequest {
	var out []parityRequest
	for _, request := range u.requests {
		if request.Path == path {
			out = append(out, request)
		}
	}
	return out
}

func parityFixed(detail SignDetail) FixedParams {
	return FixedParams{
		ActiveID:      parityActivity,
		UID:           parityUID,
		CourseID:      parityCourse,
		ClassID:       parityClass,
		IfOpenAddress: detail.IfOpenAddress,
		Ext:           detail.Ext,
	}
}

func parityPositionSpecial() map[string]interface{} {
	return map[string]interface{}{
		"latitude":    "31.230400",
		"longitude":   "121.473700",
		"description": "教学楼",
	}
}

// TestSignParityReplaySignTypes replays discovery -> metadata -> preSign ->
// stuSignajax for every sign type and pins the type-specific parameter shape.
func TestSignParityReplaySignTypes(t *testing.T) {
	cases := []struct {
		name          string
		signType      int
		preSignCode   string
		preSignEnc    string
		special       map[string]interface{}
		wantEnc       string
		wantSignCode  string
		wantAddress   string
		wantIfTiJiao  bool
		wantLocation  bool
		wantLatitude  string
		wantLongitude string
	}{
		{
			name:          "normal",
			signType:      SignNormal,
			special:       map[string]interface{}{},
			wantLatitude:  "-1",
			wantLongitude: "-1",
		},
		{
			name:        "qr",
			signType:    SignQRCode,
			preSignCode: "qr-code",
			preSignEnc:  "qr-enc",
			special: map[string]interface{}{
				"enc": "qr-enc", "c": "qr-code",
				"latitude": "31.230400", "longitude": "121.473700", "description": "教学楼",
			},
			wantEnc:       "qr-enc",
			wantLocation:  true,
			wantLatitude:  "-1",
			wantLongitude: "-1",
		},
		{
			name:     "gesture",
			signType: SignGesture,
			special: map[string]interface{}{
				"sign_code": "2468",
				"latitude":  "31.230400", "longitude": "121.473700", "description": "教学楼",
			},
			wantSignCode:  "2468",
			wantLocation:  true,
			wantLatitude:  "31.230400",
			wantLongitude: "121.473700",
		},
		{
			name:          "location",
			signType:      SignLocation,
			special:       parityPositionSpecial(),
			wantAddress:   "教学楼",
			wantIfTiJiao:  true,
			wantLocation:  true,
			wantLatitude:  "31.230400",
			wantLongitude: "121.473700",
		},
		{
			name:     "sign code",
			signType: SignCode,
			special: map[string]interface{}{
				"sign_code": "1357",
				"latitude":  "31.230400", "longitude": "121.473700", "description": "教学楼",
			},
			wantSignCode:  "1357",
			wantLocation:  true,
			wantLatitude:  "31.230400",
			wantLongitude: "121.473700",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newParityUpstream(t, tc.signType)
			client := upstream.client(t)

			actives, err := client.GetActives(parityMobile, parityPassword, parityCourse, parityClass)
			if err != nil {
				t.Fatalf("GetActives error = %v", err)
			}
			if len(actives) != 1 {
				t.Fatalf("actives = %+v, want one sign activity", actives)
			}
			active := actives[0]
			if active.ActiveID != parityActivity || active.Type != ActivityKindSignIn || active.SignType != tc.signType {
				t.Fatalf("discovery lost activity identity: %+v", active)
			}
			if active.Ext != `{"activity":"ext-token"}` {
				t.Fatalf("discovery ext = %q", active.Ext)
			}

			detail, err := client.GetSignDetail(parityMobile, parityPassword, parityActivity, parityCourse, parityClass, parityUID)
			if err != nil {
				t.Fatalf("GetSignDetail error = %v", err)
			}
			if detail.SignType != tc.signType || detail.IfOpenAddress != (tc.signType != SignNormal) {
				t.Fatalf("metadata = %+v, want signType=%d ifOpenAddress=%v", detail, tc.signType, tc.signType != SignNormal)
			}
			if detail.Ext != `{"source":"metadata"}` {
				t.Fatalf("metadata ext = %q", detail.Ext)
			}

			fixed := parityFixed(detail)
			preSign, err := client.PreSign(parityMobile, parityPassword, fixed, tc.signType, tc.preSignCode, tc.preSignEnc)
			if err != nil {
				t.Fatalf("PreSign error = %v", err)
			}
			if preSign.Status != PreSignReady {
				t.Fatalf("preSign = %+v, want ready", preSign)
			}

			special := map[string]interface{}{}
			for key, value := range tc.special {
				special[key] = value
			}
			result, err := client.Sign(parityMobile, parityPassword, fixed, tc.signType, special)
			if err != nil {
				t.Fatalf("Sign error = %v", err)
			}
			if result.Kind != SignResultSuccess || result.Attempts != 1 {
				t.Fatalf("sign result = %+v, want success with one attempt", result)
			}

			preSignRequests := upstream.find("/newsign/preSign")
			if len(preSignRequests) != 1 {
				t.Fatalf("preSign requests = %d, want 1", len(preSignRequests))
			}
			pre := preSignRequests[0]
			if pre.Method != http.MethodPost {
				t.Fatalf("preSign method = %s, want POST", pre.Method)
			}
			if pre.ContentType != "application/x-www-form-urlencoded" {
				t.Fatalf("preSign content-type = %q", pre.ContentType)
			}
			if got := pre.Form.Get("ext"); got != `{"source":"metadata"}` {
				t.Fatalf("preSign ext = %q", got)
			}
			for key, want := range map[string]string{
				"courseId":        "200",
				"classId":         "300",
				"activePrimaryId": "100",
				"uid":             "42",
				"appType":         "15",
			} {
				if got := pre.Query.Get(key); got != want {
					t.Fatalf("preSign query %s = %q, want %q", key, got, want)
				}
			}
			wantRCode := ""
			if tc.signType == SignQRCode {
				wantRCode = "SIGNIN:aid=100&source=15&Code=qr-code&enc=qr-enc"
			}
			if got := pre.Query.Get("rcode"); got != wantRCode {
				t.Fatalf("preSign rcode = %q, want %q", got, wantRCode)
			}

			signRequests := upstream.find("/pptSign/stuSignajax")
			if len(signRequests) != 1 {
				t.Fatalf("stuSignajax requests = %d, want 1", len(signRequests))
			}
			query := signRequests[0].Query
			for key, want := range map[string]string{
				"activeId":   "100",
				"uid":        "42",
				"courseId":   "200",
				"appType":    "15",
				"fid":        parityFID,
				"name":       "张三",
				"deviceCode": client.deviceCode(parityUID),
			} {
				if got := query.Get(key); got != want {
					t.Fatalf("stuSignajax %s = %q, want %q", key, got, want)
				}
			}
			if got := query.Get("enc"); got != tc.wantEnc {
				t.Fatalf("stuSignajax enc = %q, want %q", got, tc.wantEnc)
			}
			if got := query.Get("signCode"); got != tc.wantSignCode {
				t.Fatalf("stuSignajax signCode = %q, want %q", got, tc.wantSignCode)
			}
			if got := query.Get("address"); got != tc.wantAddress {
				t.Fatalf("stuSignajax address = %q, want %q", got, tc.wantAddress)
			}
			if got := query.Get("ifTiJiao"); (got == "1") != tc.wantIfTiJiao {
				t.Fatalf("stuSignajax ifTiJiao = %q, want %v", got, tc.wantIfTiJiao)
			}
			if query.Has("validate") || query.Has("enc2") {
				t.Fatalf("first submission pre-seeded captcha state: %v", query)
			}

			if got := query.Get("latitude"); got != tc.wantLatitude {
				t.Fatalf("stuSignajax latitude = %q, want %q", got, tc.wantLatitude)
			}
			if got := query.Get("longitude"); got != tc.wantLongitude {
				t.Fatalf("stuSignajax longitude = %q, want %q", got, tc.wantLongitude)
			}

			if tc.wantLocation {
				var location map[string]interface{}
				if err := json.Unmarshal([]byte(query.Get("location")), &location); err != nil {
					t.Fatalf("location payload %q: %v", query.Get("location"), err)
				}
				if location["result"] != float64(1) || location["address"] != "教学楼" {
					t.Fatalf("location = %v", location)
				}
				if _, ok := location["mockData"]; ok {
					t.Fatalf("location must not embed mockData: %v", location)
				}
				var locationResult map[string]interface{}
				if err := json.Unmarshal([]byte(query.Get("locationResult")), &locationResult); err != nil {
					t.Fatalf("locationResult payload %q: %v", query.Get("locationResult"), err)
				}
				if locationResult["mockData"] != `{"strategy":0,"probability":-1}` {
					t.Fatalf("locationResult mockData = %v", locationResult["mockData"])
				}
			} else if query.Has("location") || query.Has("locationResult") {
				t.Fatalf("normal sign must not carry location payloads: %v", query)
			}

			if tc.signType == SignGesture {
				if got := len(upstream.find("/widget/sign/pcStuSignController/checkSignCode")); got != 1 {
					t.Fatalf("gesture code checks = %d, want 1", got)
				}
			}
			upstream.assertNoUnexpected(t)
		})
	}
}

// TestSignParityReplayCaptcha covers the validate_ state: the first submission
// carries no captcha state, exactly one challenge is solved, and the retry
// carries validate plus enc2.
func TestSignParityReplayCaptcha(t *testing.T) {
	upstream := newParityUpstream(t, SignNormal)
	upstream.signBody = func(call int) (int, string) {
		if call == 1 {
			return http.StatusOK, "validate_cap-enc"
		}
		return http.StatusOK, "success"
	}
	client := upstream.client(t)

	detail, err := client.GetSignDetail(parityMobile, parityPassword, parityActivity, parityCourse, parityClass, parityUID)
	if err != nil {
		t.Fatalf("GetSignDetail error = %v", err)
	}
	result, err := client.Sign(parityMobile, parityPassword, parityFixed(detail), SignNormal, nil)
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	if result.Kind != SignResultSuccess || result.Attempts != 2 {
		t.Fatalf("result = %+v, want success after two submissions", result)
	}
	if !result.CaptchaAttempted || !result.CaptchaPassed {
		t.Fatalf("captcha accounting = %+v", result)
	}

	signRequests := upstream.find("/pptSign/stuSignajax")
	if len(signRequests) != 2 {
		t.Fatalf("stuSignajax requests = %d, want 2", len(signRequests))
	}
	if got := signRequests[0].Query; got.Has("validate") || got.Has("enc2") {
		t.Fatalf("first submission pre-seeded captcha state: %v", got)
	}
	retry := signRequests[1].Query
	if retry.Get("validate") != "validate-ok" || retry.Get("enc2") != "cap-enc" {
		t.Fatalf("retry captcha state = %v", retry)
	}
	for _, path := range []string{"/captcha/get/conf", "/captcha/get/verification/image", "/captcha/check/verification/result"} {
		if got := len(upstream.find(path)); got != 1 {
			t.Fatalf("%s requests = %d, want 1", path, got)
		}
	}
	upstream.assertNoUnexpected(t)
}

// TestSignParityReplayPreSignStates covers the two non-ready preSign outcomes:
// a closed activity and an account that already signed upstream.
func TestSignParityReplayPreSignStates(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		want     PreSignStatus
		wantWord string
	}{
		{name: "deadline", body: "<html>下次早点哦</html>", want: PreSignExpired, wantWord: "截止"},
		{name: "official already signed", body: `<script>signstatus = 9;</script>`, want: PreSignAlreadySigned, wantWord: "学习通"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newParityUpstream(t, SignNormal)
			upstream.preSignBody = func(int) (int, string) {
				return http.StatusOK, tc.body
			}
			client := upstream.client(t)

			detail, err := client.GetSignDetail(parityMobile, parityPassword, parityActivity, parityCourse, parityClass, parityUID)
			if err != nil {
				t.Fatalf("GetSignDetail error = %v", err)
			}
			result, err := client.PreSign(parityMobile, parityPassword, parityFixed(detail), SignNormal, "", "")
			if err != nil {
				t.Fatalf("PreSign error = %v", err)
			}
			if result.Status != tc.want {
				t.Fatalf("preSign status = %q, want %q", result.Status, tc.want)
			}
			if !strings.Contains(result.Message, tc.wantWord) {
				t.Fatalf("preSign message = %q, want %q", result.Message, tc.wantWord)
			}
			if got := len(upstream.find("/pptSign/stuSignajax")); got != 0 {
				t.Fatalf("stuSignajax requests = %d, want none for %s", got, tc.want)
			}
			upstream.assertNoUnexpected(t)
		})
	}
}

// TestSignParityReplayQRExpired pins the QR rescan outcome.
func TestSignParityReplayQRExpired(t *testing.T) {
	upstream := newParityUpstream(t, SignQRCode)
	upstream.signBody = func(int) (int, string) {
		return http.StatusOK, "签到失败，请重新扫描。"
	}
	client := upstream.client(t)

	detail, err := client.GetSignDetail(parityMobile, parityPassword, parityActivity, parityCourse, parityClass, parityUID)
	if err != nil {
		t.Fatalf("GetSignDetail error = %v", err)
	}
	result, err := client.Sign(parityMobile, parityPassword, parityFixed(detail), SignQRCode, map[string]interface{}{"enc": "qr-enc"})
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	if result.Kind != SignResultQRExpired {
		t.Fatalf("result = %+v, want qr_expired", result)
	}
	if result.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (QR expiry must not be retried)", result.Attempts)
	}
}

// TestSignParityReplayForbiddenRelogin pins the 403 budget: one forced login
// plus one retry on the first 403, and a terminal ErrSessionExpired on the
// second. No third attempt is made.
func TestSignParityReplayForbiddenRelogin(t *testing.T) {
	t.Run("recovers once", func(t *testing.T) {
		upstream := newParityUpstream(t, SignNormal)
		upstream.preSignBody = func(call int) (int, string) {
			if call == 1 {
				return http.StatusForbidden, "forbidden"
			}
			return http.StatusOK, ""
		}
		client := upstream.client(t)
		fixed := FixedParams{ActiveID: parityActivity, UID: parityUID, CourseID: parityCourse, ClassID: parityClass, Ext: "ext-1"}

		result, err := client.PreSign(parityMobile, parityPassword, fixed, SignNormal, "", "")
		if err != nil {
			t.Fatalf("PreSign error = %v", err)
		}
		if result.Status != PreSignReady {
			t.Fatalf("status = %q, want ready", result.Status)
		}
		if upstream.loginCalls != 2 {
			t.Fatalf("login calls = %d, want initial login + one relogin", upstream.loginCalls)
		}
		if upstream.preSignCalls != 2 {
			t.Fatalf("preSign calls = %d, want exactly 2", upstream.preSignCalls)
		}
		if got := upstream.find("/newsign/preSign")[1].Form.Get("ext"); got != "ext-1" {
			t.Fatalf("retry lost ext: %q", got)
		}
	})

	t.Run("terminal second 403", func(t *testing.T) {
		upstream := newParityUpstream(t, SignNormal)
		upstream.preSignBody = func(int) (int, string) {
			return http.StatusForbidden, ""
		}
		client := upstream.client(t)
		fixed := FixedParams{ActiveID: parityActivity, UID: parityUID, CourseID: parityCourse, ClassID: parityClass}

		_, err := client.PreSign(parityMobile, parityPassword, fixed, SignNormal, "", "")
		if err == nil || !strings.Contains(err.Error(), ErrSessionExpired.Error()) {
			t.Fatalf("error = %v, want ErrSessionExpired", err)
		}
		if upstream.loginCalls != 2 || upstream.preSignCalls != 2 {
			t.Fatalf("login=%d preSign=%d, want 2/2 without a third attempt", upstream.loginCalls, upstream.preSignCalls)
		}
	})
}

// TestSignParityReplayFaceSignature covers S13: a decryptable clientId adds
// cxcid + signToken to faceResult, while a missing or unusable clientId keeps
// the unsigned payload.
func TestSignParityReplayFaceSignature(t *testing.T) {
	for _, tc := range []struct {
		name     string
		clientID string
		signed   bool
	}{
		{name: "decryptable clientId", clientID: clientIDFixture, signed: true},
		{name: "missing clientId", clientID: "", signed: false},
		{name: "unusable clientId", clientID: "not-a-valid-client-id", signed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.signed {
				// The upstream clientId is signed with a private key xbt does
				// not have; use the fixed test identity (device_test.go) so
				// this replay can assert the signed wire shape.
				original := faceResultKey
				faceResultKey = func() (*rsa.PublicKey, error) { return testClientIDPublicKey(t), nil }
				t.Cleanup(func() { faceResultKey = original })
			}
			upstream := newParityUpstream(t, SignNormal)
			client := upstream.client(t)

			result, err := client.CheckFaceResult(parityMobile, parityPassword, parityActivity, "face-object", tc.clientID)
			if err != nil {
				t.Fatalf("CheckFaceResult error = %v", err)
			}
			if result.CurrentFaceID != "face-object" || result.FaceEnc != "face-enc-result" {
				t.Fatalf("face result = %+v", result)
			}
			if upstream.faceCalls != 1 || len(upstream.facePayloads) != 1 {
				t.Fatalf("check-face-result calls = %d, payloads = %d", upstream.faceCalls, len(upstream.facePayloads))
			}
			payload := upstream.facePayloads[0]
			if payload["currentFaceId"] != "face-object" {
				t.Fatalf("faceResult currentFaceId = %v", payload["currentFaceId"])
			}
			cxtime, ok := payload["cxtime"].(string)
			if !ok || cxtime == "" {
				t.Fatalf("faceResult cxtime = %v", payload["cxtime"])
			}
			if !tc.signed {
				if _, ok := payload["cxcid"]; ok {
					t.Fatalf("cxcid present without a usable clientId: %v", payload)
				}
				if _, ok := payload["signToken"]; ok {
					t.Fatalf("signToken present without a usable clientId: %v", payload)
				}
				upstream.assertNoUnexpected(t)
				return
			}
			if payload["cxcid"] != "signed-cid" {
				t.Fatalf("cxcid = %v", payload["cxcid"])
			}
			want := signFaceToken("face-object", cxtime, "signed-cid", "signed-sc")
			if payload["signToken"] != want {
				t.Fatalf("signToken = %v, want %s", payload["signToken"], want)
			}
			upstream.assertNoUnexpected(t)
		})
	}
}
