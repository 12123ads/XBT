package xxt

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestParsePPTActiveInfoFixtures(t *testing.T) {
	full := `{"data":{
		"starttime":1700000000000,
		"endtime":1700000600000,
		"otherId":4,
		"ifrefreshewm":1,
		"ifphoto":1,
		"openCheckFaceFlag":1,
		"ifNeedVCode":1,
		"ifopenAddress":1,
		"locationLatitude":31.2304,
		"locationLongitude":121.4737,
		"locationRange":200,
		"signInId":101,
		"signOutId":102,
		"signOutPublishTimeStamp":1700000700000,
		"ext":{"source":"test","flag":true}
	}}`
	partial := `{"data":{"starttime":"1700000000000","otherId":"5","ifopenAddress":1}}`

	for _, tc := range []struct {
		name  string
		raw   string
		want  SignDetail
		valid bool
	}{
		{
			name:  "complete",
			raw:   full,
			valid: true,
			want: SignDetail{
				StartTime:          1700000000000,
				EndTime:            1700000600000,
				SignType:           SignLocation,
				IfRefreshEWM:       true,
				IfPhoto:            true,
				IfFace:             true,
				IfNeedVCode:        true,
				IfOpenAddress:      true,
				LocationLatitude:   31.2304,
				LocationLongitude:  121.4737,
				LocationRange:      200,
				SignInID:           101,
				SignOutID:          102,
				SignOutPublishTime: 1700000700000,
			},
		},
		{
			name:  "missing fields",
			raw:   partial,
			valid: true,
			want: SignDetail{
				StartTime:     1700000000000,
				SignType:      SignCode,
				IfOpenAddress: true,
			},
		},
		{name: "empty response", raw: `{}`, valid: false},
		{name: "empty data", raw: `{"data":{}}`, valid: false},
		{name: "null data", raw: `{"data":null}`, valid: false},
		{name: "empty body", raw: ``, valid: false},
		{name: "invalid json", raw: `<html>login</html>`, valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePPTActiveInfo([]byte(tc.raw))
			if ok != tc.valid {
				t.Fatalf("valid=%v, want %v", ok, tc.valid)
			}
			if !tc.valid {
				if got != (SignDetail{}) {
					t.Fatalf("invalid response parsed to %+v", got)
				}
				return
			}
			if tc.name == "complete" {
				var ext map[string]interface{}
				if err := json.Unmarshal([]byte(got.Ext), &ext); err != nil || ext["source"] != "test" || ext["flag"] != true {
					t.Fatalf("ext=%q err=%v", got.Ext, err)
				}
				got.Ext = ""
			}
			if got != tc.want {
				t.Fatalf("parsed %+v, want %+v", got, tc.want)
			}
		})
	}
}

type countingTransport struct {
	mu     sync.Mutex
	counts map[string]int
	handle func(*http.Request) (*http.Response, error)
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.counts[req.URL.Path]++
	t.mu.Unlock()
	return t.handle(req)
}

func (t *countingTransport) count(path string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[path]
}

func jsonResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestGetSignDetailUsesPPTActiveInfoAndCachesIt(t *testing.T) {
	full := `{"data":{"starttime":1700000000000,"endtime":1700000600000,"otherId":4,
		"ifNeedVCode":1,"ifopenAddress":1,"openCheckFaceFlag":1,
		"locationLatitude":31.23,"locationLongitude":121.47,"locationRange":150}}`
	transport := &countingTransport{counts: map[string]int{}, handle: func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v2/apis/active/getPPTActiveInfo" || req.URL.Query().Get("activeId") != "100" {
			t.Fatalf("unexpected request: %s", req.URL)
		}
		return jsonResponse(req, http.StatusOK, full), nil
	}}
	client := activityScopeTestClient(t, transport)

	first, err := client.GetSignDetail("13800000001", "password", 100, 10, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.GetSignDetail("13800000001", "password", 100, 10, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := transport.count("/v2/apis/active/getPPTActiveInfo"); got != 1 {
		t.Fatalf("upstream calls=%d, want 1", got)
	}
	if first != second {
		t.Fatalf("cached detail mismatch: %+v vs %+v", first, second)
	}
	want := SignDetail{
		StartTime:          1700000000000,
		EndTime:            1700000600000,
		SignType:           SignLocation,
		IfFace:             true,
		IfNeedVCode:        true,
		IfOpenAddress:      true,
		LocationLatitude:   31.23,
		LocationLongitude:  121.47,
		LocationRange:      150,
		SignOutPublishTime: 0,
	}
	if first != want {
		t.Fatalf("detail=%+v, want %+v", first, want)
	}
}

func TestGetSignDetailFallsBackToLegacyEndpoints(t *testing.T) {
	transport := &countingTransport{counts: map[string]int{}, handle: func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v2/apis/active/getPPTActiveInfo":
			return jsonResponse(req, http.StatusServiceUnavailable, `{"msg":"unavailable"}`), nil
		case "/newsign/signDetail":
			return jsonResponse(req, http.StatusOK, `{"startTime":1700000000000,"endTime":1700000600000,"otherId":2,"ifRefreshEwm":1,"ifphoto":1}`), nil
		case "/newsign/preSign":
			return jsonResponse(req, http.StatusOK, `<script>openCheckFaceFlag = 1;</script>`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	}}
	client := activityScopeTestClient(t, transport)

	detail, err := client.GetSignDetail("13800000001", "password", 200, 10, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if detail.StartTime != 1700000000000 || detail.EndTime != 1700000600000 || detail.SignType != SignQRCode {
		t.Fatalf("legacy fields lost: %+v", detail)
	}
	if !detail.IfRefreshEWM || !detail.IfPhoto || !detail.IfFace {
		t.Fatalf("legacy flags lost: %+v", detail)
	}
	if detail.IfNeedVCode || detail.IfOpenAddress || detail.LocationRange != 0 {
		t.Fatalf("fallback fabricated metadata: %+v", detail)
	}
	if got := transport.count("/v2/apis/active/getPPTActiveInfo"); got != 1 {
		t.Fatalf("getPPTActiveInfo calls=%d, want 1", got)
	}
	if got := transport.count("/newsign/signDetail"); got != 1 {
		t.Fatalf("signDetail calls=%d, want 1", got)
	}
	if got := transport.count("/newsign/preSign"); got != 1 {
		t.Fatalf("preSign calls=%d, want 1", got)
	}
}
