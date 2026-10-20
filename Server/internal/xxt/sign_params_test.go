package xxt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signParamsTestBase(deviceCode string) stuSignParams {
	return stuSignParams{
		ActivityID:    100,
		CourseID:      200,
		UID:           7,
		Name:          "张三",
		FID:           "1234",
		DeviceCode:    deviceCode,
		IfOpenAddress: true,
		Position: &SignPosition{
			Latitude:  30.123456789,
			Longitude: 120.987654321,
			Address:   "教学楼A",
		},
		Enc:      "enc-abc",
		SignCode: "1234",
	}
}

func TestBuildStuSignParamsPerSignType(t *testing.T) {
	client := &Client{aesKey: "1234567890123456"}
	deviceCode := client.deviceCode(7)
	base := signParamsTestBase(deviceCode)

	for _, tc := range []struct {
		name        string
		signType    int
		want        map[string]string
		noLocation  bool
		wantAddress bool
		wantEnc     bool
		wantCode    bool
	}{
		{name: "normal/photo", signType: SignNormal, noLocation: true, want: map[string]string{"latitude": "-1", "longitude": "-1"}},
		{name: "qrcode", signType: SignQRCode, wantEnc: true, want: map[string]string{"latitude": "-1", "longitude": "-1"}},
		{name: "gesture", signType: SignGesture, wantCode: true, want: map[string]string{"latitude": "30.123457", "longitude": "120.987654"}},
		{name: "location", signType: SignLocation, wantAddress: true, want: map[string]string{"latitude": "30.123457", "longitude": "120.987654", "address": "教学楼A", "ifTiJiao": "1"}},
		{name: "sign code", signType: SignCode, wantCode: true, want: map[string]string{"latitude": "30.123457", "longitude": "120.987654"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			p.SignType = tc.signType
			params := buildStuSignParams(p)
			q := params

			for key, want := range map[string]string{
				"activeId":   "100",
				"uid":        "7",
				"courseId":   "200",
				"appType":    "15",
				"name":       "张三",
				"fid":        "1234",
				"deviceCode": deviceCode,
			} {
				if got := q.Get(key); got != want {
					t.Fatalf("%s = %q, want %q (params=%v)", key, got, want, q)
				}
			}
			for key, want := range tc.want {
				if got := q.Get(key); got != want {
					t.Fatalf("%s = %q, want %q", key, got, want)
				}
			}

			if tc.wantEnc {
				if q.Get("enc") != "enc-abc" {
					t.Fatalf("enc = %q, want enc-abc", q.Get("enc"))
				}
			} else if q.Has("enc") {
				t.Fatalf("unexpected enc for type %d: %v", tc.signType, q)
			}

			if tc.wantCode {
				if q.Get("signCode") != "1234" {
					t.Fatalf("signCode = %q, want 1234", q.Get("signCode"))
				}
			} else if q.Has("signCode") {
				t.Fatalf("unexpected signCode for type %d: %v", tc.signType, q)
			}

			if !tc.wantAddress {
				if q.Has("address") || q.Has("ifTiJiao") {
					t.Fatalf("unexpected address/ifTiJiao for type %d: %v", tc.signType, q)
				}
			}

			if tc.noLocation {
				if q.Has("location") || q.Has("locationResult") {
					t.Fatalf("unexpected location params for type %d: %v", tc.signType, q)
				}
				return
			}

			assertLocationParam(t, q.Get("location"), false)
			assertLocationParam(t, q.Get("locationResult"), true)
		})
	}
}

func assertLocationParam(t *testing.T, raw string, wantMockData bool) {
	t.Helper()
	if raw == "" {
		t.Fatal("location parameter missing")
	}
	var location map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &location); err != nil {
		t.Fatalf("location is not JSON: %v (%s)", err, raw)
	}
	if got := location["result"]; got != float64(1) {
		t.Fatalf("location.result = %v, want 1", got)
	}
	if got := location["latitude"]; got != 30.123457 {
		t.Fatalf("location.latitude = %v, want 30.123457", got)
	}
	if got := location["longitude"]; got != 120.987654 {
		t.Fatalf("location.longitude = %v, want 120.987654", got)
	}
	if got := location["address"]; got != "教学楼A" {
		t.Fatalf("location.address = %v, want 教学楼A", got)
	}
	mock, hasMock := location["mockData"]
	if !wantMockData {
		if hasMock {
			t.Fatalf("location must not embed mockData: %v", location)
		}
		return
	}
	encoded, ok := mock.(string)
	if !ok {
		t.Fatalf("mockData must be an encoded JSON string, got %T (%v)", mock, mock)
	}
	if encoded != `{"strategy":0,"probability":-1}` {
		t.Fatalf("mockData = %q", encoded)
	}
}

func TestBuildStuSignParamsLocationToggle(t *testing.T) {
	client := &Client{aesKey: "1234567890123456"}
	base := signParamsTestBase(client.deviceCode(7))

	for _, signType := range []int{SignQRCode, SignGesture, SignLocation, SignCode} {
		t.Run(strconv.Itoa(signType), func(t *testing.T) {
			p := base
			p.SignType = signType

			p.IfOpenAddress = false
			off := buildStuSignParams(p)
			if off.Has("location") || off.Has("locationResult") {
				t.Fatalf("if_open_address=false still emitted location params: %v", off)
			}

			p.IfOpenAddress = true
			on := buildStuSignParams(p)
			if !on.Has("location") || !on.Has("locationResult") {
				t.Fatalf("if_open_address=true missing location params: %v", on)
			}

			if signType == SignLocation {
				for _, params := range []url.Values{off, on} {
					if params.Get("address") != "教学楼A" || params.Get("ifTiJiao") != "1" {
						t.Fatalf("location sign lost address/ifTiJiao: %v", params)
					}
				}
			}
		})
	}
}

func TestBuildStuSignParamsNeverInventsStateMachineFields(t *testing.T) {
	client := &Client{aesKey: "1234567890123456"}
	base := signParamsTestBase(client.deviceCode(7))
	base.SignType = SignQRCode

	for i := 0; i < 3; i++ {
		params := buildStuSignParams(base)
		if params.Has("enc2") {
			t.Fatalf("enc2 was generated without state: %v", params)
		}
		if params.Has("validate") {
			t.Fatalf("validate was pre-seeded on first send: %v", params)
		}
	}

	withState := base
	withState.Enc2 = "abc"
	withState.Validate = "validate_abc"
	params := buildStuSignParams(withState)
	if params.Get("enc2") != "abc" || params.Get("validate") != "validate_abc" {
		t.Fatalf("state machine values not passed through: %v", params)
	}
}

func TestDeviceCodeIsStablePerAccount(t *testing.T) {
	client := &Client{aesKey: "1234567890123456"}
	first := client.deviceCode(7)
	if first == "" {
		t.Fatal("deviceCode must not be empty")
	}
	if second := client.deviceCode(7); second != first {
		t.Fatalf("deviceCode changed for the same account: %q != %q", first, second)
	}
	if other := client.deviceCode(8); other == first {
		t.Fatalf("deviceCode collided across accounts: %q", first)
	}
	if len(first) != 64 || strings.Trim(first, "0123456789abcdef") != "" {
		t.Fatalf("deviceCode must be 64 lowercase hex chars: %q", first)
	}

	replay := &Client{aesKey: "1234567890123456"}
	if replay.deviceCode(7) != first {
		t.Fatal("deviceCode is not reproducible from the same configuration")
	}
	rotated := &Client{aesKey: "abcdefghijklmnop"}
	if rotated.deviceCode(7) == first {
		t.Fatal("deviceCode must depend on the configured key")
	}
}

type signTransport func(*http.Request) (*http.Response, error)

func (f signTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSignTransmitsBuiltParams(t *testing.T) {
	const mobile = "13800000001"
	const password = "password"
	var requests []*url.URL
	client := &Client{
		aesKey:   "1234567890123456",
		mobileUA: "sign-params-test",
		http: &http.Client{Transport: signTransport(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, req.URL)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("success")), Request: req}, nil
		}), Timeout: 5 * time.Second},
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.sessions = map[string]*Session{
		mobile: {Mobile: mobile, Password: password, UID: 1, Name: "张三", Jar: jar, LastLoginAt: time.Now()},
	}
	fixed := FixedParams{ActiveID: 100, UID: 1, CourseID: 200, ClassID: 300, IfOpenAddress: true}

	special := map[string]interface{}{
		"enc":         "enc-abc",
		"latitude":    "30.123456",
		"longitude":   "120.654321",
		"description": "教学楼A",
	}
	if result, err := client.Sign(mobile, password, fixed, SignQRCode, special); err != nil || result.Kind != SignResultSuccess {
		t.Fatalf("sign failed: result=%+v err=%v", result, err)
	}
	if len(requests) != 1 {
		t.Fatalf("qrcode first send must be a single request, got %d: %v", len(requests), requests)
	}
	q := requests[0].Query()
	if q.Has("validate") || q.Has("enc2") {
		t.Fatalf("state machine fields leaked into the first send: %v", q)
	}
	if q.Get("deviceCode") != client.deviceCode(1) || q.Get("courseId") != "200" || q.Get("enc") != "enc-abc" {
		t.Fatalf("built params missing from request: %v", q)
	}
	if !q.Has("location") || !q.Has("locationResult") {
		t.Fatalf("if_open_address position missing from request: %v", q)
	}

	requests = nil
	special["enc2"] = "abc"
	special["validate"] = "validate_abc"
	if _, err := client.Sign(mobile, password, fixed, SignQRCode, special); err != nil {
		t.Fatalf("state machine sign failed: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected a single request, got %d", len(requests))
	}
	q = requests[0].Query()
	if q.Get("enc2") != "abc" || q.Get("validate") != "validate_abc" {
		t.Fatalf("state machine values not submitted: %v", q)
	}
}
