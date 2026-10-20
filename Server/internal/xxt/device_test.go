package xxt

import (
	"crypto/md5"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The upstream clientId is a private-key (type 1) PKCS#1 v1.5 block. These
// fixtures are signed by the fixed test key below, contain no real account
// data, and decrypt to {"cid":"signed-cid","sc":"signed-sc"}.
const (
	clientIDFixture   = "IGgr0eBRVhMNvwCKO/RwtVD+b34L9LMvX7X+KGR8m1gx+7HzHdtciwtd31KS2uIkI5Pr+WOqwXb3HMqFNqznvmfcdvA9b45QNLZqSBMi2TzVAuYM9laNJcAAmYK/1Y8RVj3RWoPU83alRPlYrw58nX0Q+ufHbZXSbC/U/tIitdY="
	emptyCIDClientID  = "ukSyWM/M7PQSPlIfGt6mYvdEaCKVqHd94z3qER1MXGWUxt6ADIW1hci72vWA43WDkQgEy3xCmM1CCEp2EAf10FPO/d+zhGi1qzqMGG8Zh0Q6ylLN4s7TMiaUzV4BehqclWqem6MJKrxEF140GcaYmD+zubdNelrWG+Z0J8CthqY="
	notJSONClientID   = "D1jmmU69KNA9IKJgu7/mB9Rm2Nihmsftyt5b5+D/slRgpwRzcJYiugx6jAHqYtLEweugwKuasXjjD4TAzpfJnGuoSPHxPnBIkz5/SNKq/80ohwRNOA97QIDHQ8QT4X7LyPCCv7OvqzfwCrceYSXIxRhpjW7/jMR7IyBx8RvHWqY="
	missingSCClientID = "euwIyDbt0RUtgjU5hXr2Ou2v0IE+/sDXvAbJhQkKi3mv4C45+KyzrBhQE4YHP2UXlB2SsTpD9hQm4OmwWrIrkgKHV/nmKnbplEVacFziY4DDylPDt4HSZo93IpI0xDCfixtEDrykG4xtGSHScr96T3BU06C4BlTyncWbmNOQyyE="
)

// testClientIDPrivateKey is a test-only RSA key. It signs fixture blocks; it is
// not the upstream key and protects nothing.
const testClientIDPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQDLc6mb4ubWmbg8rkg+Ij2dr9tS6zX7X4KJN1RaVhKDA0xh7a/V
ZCa93qACMBMrxsCio6yP4vV3RCK88pppy4JGakANJ4+IFi+RdPJVzNVPhywZyxKC
iwHsXBib4HnsM6FusBOGFAuDWZxUd5r8ZhWCnCzhs0KkIGQE94NwM/0azQIDAQAB
AoGAIyoCUHBzC/flLU3FLEIQSsMkogwH2ELthahjwrpaOD03dDyX9ca54HrMRpyH
D7uqO2IVNwhX+01yQJyg3jS/tvuImWq7CJV1X4lwoDlkP51PbzkGWlN+BKM/Kxhz
tB3FW5vIRkAZPC/yAn41OiMAY+fkwbIgTH1hKY2Hpyz0dUkCQQDpcom9EHloSnoB
hmLtctCqIP6rRxyaTonE2WgfVRwiQPocIfzNMpn4ijkpFeq5Shxa5bm0Syj0gkSn
XPzO2m/FAkEA3xtLKuOCRN84pMtNNiXunYM8ydIugryL8BZwuM2bks3TsavY5pTV
NzATfk2lKOjTw6lUiFq+tten8g9qlYlnaQJBANpj+pNS3CFCu0y96cJ1TNw7rcOR
7SI2IxByysO1HXK2EsVKznMwyXYH6V2UUVgyCsDGdKpEPfYwAAYKKb9h/nkCQGjf
VYgFhXiGTQP5pIOtdOqJhAuCRuahZk4m3jmgPF7XbYj4tG4+y7KzYJZgPSBrwQ8a
C2hE75zW5lT8NiewfPECQCYz3cm9weglW4OYevuSxf9iGDtzERy/eS7wBSku3fo7
9MddJFxKieqC+TTeb+aQQzyTdNJ5JhaDz82gei3O3H0=
-----END RSA PRIVATE KEY-----`

func testClientIDPublicKey(t *testing.T) *rsa.PublicKey {
	t.Helper()
	block, _ := pem.Decode([]byte(testClientIDPrivateKey))
	if block == nil {
		t.Fatal("test key is not PEM")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return &privateKey.PublicKey
}

func TestDecryptClientIDFixedFixture(t *testing.T) {
	identity, ok := decryptClientIDWithKey(clientIDFixture, testClientIDPublicKey(t))
	if !ok {
		t.Fatal("fixture did not decrypt")
	}
	if identity.CID != "signed-cid" || identity.SC != "signed-sc" {
		t.Fatalf("identity = %+v, want signed-cid/signed-sc", identity)
	}
}

func TestDecryptClientIDRejectsInvalidSamples(t *testing.T) {
	key := testClientIDPublicKey(t)
	signed := map[string]string{
		"empty cid":  emptyCIDClientID,
		"not json":   notJSONClientID,
		"missing sc": missingSCClientID,
	}
	for name, value := range signed {
		t.Run(name, func(t *testing.T) {
			if identity, ok := decryptClientIDWithKey(value, key); ok {
				t.Fatalf("accepted invalid clientId: %+v", identity)
			}
		})
	}
	for name, value := range map[string]string{
		"empty":        "",
		"not base64":   "!!!not-base64!!!",
		"wrong length": "AAAA",
		"all zero":     base64.StdEncoding.EncodeToString(make([]byte, 128)),
	} {
		t.Run(name, func(t *testing.T) {
			if identity, ok := DecryptClientID(value); ok {
				t.Fatalf("accepted invalid clientId: %+v", identity)
			}
		})
	}
}

func TestDeviceInfoPublicKey(t *testing.T) {
	key, err := deviceInfoRSAKey()
	if err != nil {
		t.Fatal(err)
	}
	if key.Size() != 128 || key.E != 65537 {
		t.Fatalf("protocol key size=%d exponent=%d", key.Size(), key.E)
	}
}

func TestSignFaceTokenExpectedValue(t *testing.T) {
	const want = "143b3a864ba477038ebf79abb9c50a50"
	got := signFaceToken("face-object-id", "1700000000000", "fixture-cid", "fixture-sc")
	if got != want {
		t.Fatalf("signToken = %q, want %q", got, want)
	}
	sum := md5.Sum([]byte("LiveDetectionStatus1collectStatus1currentFaceIdface-object-idcxcidfixture-cidcxtime1700000000000fixture-sc"))
	if hex.EncodeToString(sum[:]) != want {
		t.Fatal("test expectation no longer matches the documented concatenation")
	}
}

func TestFaceSignatureIgnoresFieldInsertionOrder(t *testing.T) {
	first := map[string]string{
		"currentFaceId":       "face-object-id",
		"LiveDetectionStatus": "1",
		"collectStatus":       "1",
		"cxtime":              "1700000000000",
		"cxcid":               "fixture-cid",
	}
	second := map[string]string{
		"cxcid":               "fixture-cid",
		"cxtime":              "1700000000000",
		"collectStatus":       "1",
		"LiveDetectionStatus": "1",
		"currentFaceId":       "face-object-id",
	}
	if got, want := faceSignature(first, "fixture-sc"), faceSignature(second, "fixture-sc"); got != want {
		t.Fatalf("signature depends on insertion order: %q != %q", got, want)
	}
}

func TestBuildFaceResultSignsOnlyWithUsableClientID(t *testing.T) {
	signed := buildFaceResultWithKey("face-object-id", "1700000000000", clientIDFixture, testClientIDPublicKey(t))
	if signed["cxcid"] != "signed-cid" {
		t.Fatalf("cxcid = %v", signed["cxcid"])
	}
	expected := signFaceToken("face-object-id", "1700000000000", "signed-cid", "signed-sc")
	if signed["signToken"] != expected {
		t.Fatalf("signToken = %v, want %s", signed["signToken"], expected)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"currentFaceId", "LiveDetectionStatus", "collectStatus", "cxtime", "cxcid", "signToken"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("signed faceResult missing %s: %s", key, raw)
		}
	}

	for name, value := range map[string]string{"empty": "", "invalid": notJSONClientID} {
		t.Run(name, func(t *testing.T) {
			plain := buildFaceResult("face-object-id", "1700000000000", value)
			if _, ok := plain["cxcid"]; ok {
				t.Fatal("cxcid present without clientId")
			}
			if _, ok := plain["signToken"]; ok {
				t.Fatal("signToken present without clientId")
			}
			if len(plain) != 4 {
				t.Fatalf("faceResult = %v, want the four legacy fields", plain)
			}
		})
	}
}

func TestGenerateDeviceInfoIsStableAndPerAccount(t *testing.T) {
	first := GenerateDeviceInfo(7, "13800000007")
	second := GenerateDeviceInfo(7, "13800000007")
	if first != second {
		t.Fatalf("device profile is not stable: %+v != %+v", first, second)
	}
	other := GenerateDeviceInfo(8, "13800000008")
	if first.DeviceUniqueID == other.DeviceUniqueID || first.AndroidID == other.AndroidID || first.MediaDRMID == other.MediaDRMID {
		t.Fatal("different accounts share a device identifier")
	}
	if !first.Valid() {
		t.Fatalf("generated profile is invalid: %+v", first)
	}

	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"deviceUniqueId", "cdid", "device_id", "android_id", "mediaDrmId", "oaid",
		"platform", "os_name", "os_ver", "os_lang", "brand", "board", "hardware",
		"model", "cpu_ar", "app_name", "app_ver",
	} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("device JSON missing %s: %s", key, raw)
		}
	}
}

func TestFetchClientIDReadsMessageFieldAndNeverLeaksBody(t *testing.T) {
	var body string
	transport := activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/apis/login/userLogin4Uname.do") {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		encrypted, err := base64.StdEncoding.DecodeString(form.Get("data"))
		if err != nil || len(encrypted) == 0 || len(encrypted)%128 != 0 {
			t.Fatalf("data is not RSA-encrypted device info: len=%d err=%v", len(encrypted), err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	client := activityScopeTestClient(t, transport)
	jar, _ := cookiejar.New(nil)
	client.sessions["13800000007"] = &Session{Mobile: "13800000007", Password: "pw", UID: 7, Jar: jar, LastLoginAt: time.Now()}
	info := GenerateDeviceInfo(7, "13800000007")

	body = `{"msg":{"clientId":"client-id-value","accountInfo":{"imAccount":{"password":"must-not-return"}}}}`
	clientID, err := client.FetchClientID("13800000007", "pw", info)
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "client-id-value" {
		t.Fatalf("clientId = %q", clientID)
	}

	body = `{"msg":{}}`
	clientID, err = client.FetchClientID("13800000007", "pw", info)
	if err != nil || clientID != "" {
		t.Fatalf("missing clientId: got %q err=%v", clientID, err)
	}

	body = `{"msg":{"clientId":"secret-value"}}`
	transport = activityScopeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})
	client = activityScopeTestClient(t, transport)
	client.sessions["13800000007"] = &Session{Mobile: "13800000007", Password: "pw", UID: 7, Jar: jar, LastLoginAt: time.Now()}
	if _, err := client.FetchClientID("13800000007", "pw", info); err == nil {
		t.Fatal("expected an HTTP error")
	} else if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error leaked the response body: %v", err)
	}
}

func TestCheckFaceResultWithoutClientIDKeepsLegacyPayload(t *testing.T) {
	var faceResult string
	transport := activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/pptSign/check-face-result") {
			t.Fatalf("unexpected request: %s", req.URL)
		}
		faceResult = req.URL.Query().Get("faceResult")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"enc":"face-enc","code":"ok"}`)),
			Header:     make(http.Header),
		}, nil
	})
	client := activityScopeTestClient(t, transport)
	jar, _ := cookiejar.New(nil)
	client.sessions["13800000007"] = &Session{Mobile: "13800000007", Password: "pw", UID: 7, Jar: jar, LastLoginAt: time.Now()}

	result, err := client.CheckFaceResult("13800000007", "pw", 100, "face-object", notJSONClientID)
	if err != nil {
		t.Fatal(err)
	}
	if result.FaceEnc != "face-enc" || result.FaceCode != "ok" {
		t.Fatalf("result = %+v", result)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(faceResult), &payload); err != nil {
		t.Fatalf("faceResult is not JSON: %v (%s)", err, faceResult)
	}
	if _, ok := payload["cxcid"]; ok {
		t.Fatalf("cxcid present without a usable clientId: %s", faceResult)
	}
	if _, ok := payload["signToken"]; ok {
		t.Fatalf("signToken present without a usable clientId: %s", faceResult)
	}
	if len(payload) != 4 || payload["currentFaceId"] != "face-object" {
		t.Fatalf("faceResult = %s, want the four legacy fields", faceResult)
	}
}
