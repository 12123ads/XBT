package xxt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
)

// deviceCodeDomain separates the device-code derivation from other HMAC uses of
// the same configured secret. Bump the version only if the derived format must
// change, because an account's deviceCode is expected to stay stable.
const deviceCodeDomain = "xbt-device-code:v1:"

// SignPosition is the point a user (or a configured preset) selected for a
// check-in. It is always supplied by the caller; the builder never invents one.
type SignPosition struct {
	Latitude  float64
	Longitude float64
	Address   string
}

// stuSignParams is the complete input of the stuSignajax parameter builder.
type stuSignParams struct {
	SignType      int
	ActivityID    int64
	CourseID      int64
	UID           int64
	Name          string
	FID           string
	DeviceCode    string
	IfOpenAddress bool
	Position      *SignPosition
	Enc           string
	SignCode      string
	ObjectID      string
	CurrentFaceID string
	FaceEnc       string
	// Enc2 and Validate are only ever written by the response state machine
	// (XBT-S3). The builder must not fabricate them: empty means "omit".
	Enc2     string
	Validate string
}

// buildStuSignParams renders the query for https://mobilelearn.chaoxing.com/pptSign/stuSignajax.
//
// Every sign type carries activeId/uid/courseId/appType/name/deviceCode plus the
// configured fid. Location parameters follow CSF: when the activity opens
// address, types QR/gesture/location/code get `location` (no mockData) and
// `locationResult` (with mockData); the location type additionally carries
// `address` and `ifTiJiao=1`. `enc2`/`validate` are passthrough only.
func buildStuSignParams(p stuSignParams) url.Values {
	params := url.Values{}
	params.Set("activeId", strconv.FormatInt(p.ActivityID, 10))
	params.Set("uid", strconv.FormatInt(p.UID, 10))
	params.Set("courseId", strconv.FormatInt(p.CourseID, 10))
	params.Set("clientip", "")
	params.Set("appType", "15")
	params.Set("fid", p.FID)
	params.Set("name", p.Name)
	params.Set("deviceCode", p.DeviceCode)

	if p.ObjectID != "" {
		params.Set("objectId", p.ObjectID)
		params.Set("useragent", "")
		params.Set("latitude", "-1")
		params.Set("longitude", "-1")
	}
	if p.CurrentFaceID != "" {
		params.Set("currentFaceId", p.CurrentFaceID)
		params.Set("ifCFP", "0")
		params.Set("vpProbability", "-1")
		params.Set("vpStrategy", "")
		// CSF sends both fields with an empty value for every face
		// submission. The upstream response's `code` is deliberately not
		// echoed here: staging must prove a returned faceCode is required
		// before xbt sends it.
		params.Set("faceCode", "")
		params.Set("faceEncAid", "")
	}
	if p.FaceEnc != "" {
		params.Set("faceEnc", p.FaceEnc)
	}

	switch p.SignType {
	case SignQRCode:
		params.Set("enc", p.Enc)
		params.Set("useragent", "")
		params.Set("latitude", "-1")
		params.Set("longitude", "-1")
	case SignGesture, SignCode:
		params.Set("signCode", p.SignCode)
		params.Set("latitude", positionLatitude(p))
		params.Set("longitude", positionLongitude(p))
	case SignLocation:
		if p.Position != nil {
			params.Set("address", p.Position.Address)
		}
		params.Set("latitude", positionLatitude(p))
		params.Set("longitude", positionLongitude(p))
		params.Set("ifTiJiao", "1")
	default:
		if p.Position != nil {
			params.Set("latitude", positionLatitude(p))
			params.Set("longitude", positionLongitude(p))
		} else {
			params.Set("latitude", "-1")
			params.Set("longitude", "-1")
		}
	}

	if p.IfOpenAddress && p.Position != nil {
		switch p.SignType {
		case SignNormal, SignQRCode, SignGesture, SignLocation, SignCode:
			params.Set("location", p.positionJSON(false))
			params.Set("locationResult", p.positionJSON(true))
		}
	}

	if p.Enc2 != "" {
		params.Set("enc2", p.Enc2)
	}
	if p.Validate != "" {
		params.Set("validate", p.Validate)
	}
	return params
}

// positionJSON mirrors CSF's addLocationDataParameter output. `location` omits
// mockData; `locationResult` embeds it as a JSON-encoded string
// ("{\"strategy\":0,\"probability\":-1}"), which is what the upstream app sends.
func (p stuSignParams) positionJSON(withMockData bool) string {
	location := map[string]interface{}{
		"result":    1,
		"latitude":  roundCoordinate(p.Position.Latitude),
		"longitude": roundCoordinate(p.Position.Longitude),
		"address":   p.Position.Address,
	}
	if withMockData {
		location["mockData"] = `{"strategy":0,"probability":-1}`
	}
	raw, err := json.Marshal(location)
	if err != nil {
		return ""
	}
	return string(raw)
}

func positionLatitude(p stuSignParams) string {
	if p.Position == nil {
		return "-1"
	}
	return formatCoordinate(p.Position.Latitude)
}

func positionLongitude(p stuSignParams) string {
	if p.Position == nil {
		return "-1"
	}
	return formatCoordinate(p.Position.Longitude)
}

func formatCoordinate(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func roundCoordinate(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// deviceCode derives a stable pseudo device identifier for one account.
//
// It is never read from the host: it is HMAC-SHA256 over
// "xbt-device-code:v1:<uid>" keyed with the configured chaoxing_aes_key, hex
// encoded (64 chars, same shape as a hashed device id). Consequences:
//   - the same account always gets the same code, so repeated sign attempts look
//     like one device;
//   - different uids produce unrelated codes (no shared hardware identity);
//   - the value is reproducible from configuration alone and never touches real
//     hardware identifiers.
//
// Rotating chaoxing_aes_key changes every account's code, so keep that key stable
// for the lifetime of the deployment's sign records.
func (c *Client) deviceCode(uid int64) string {
	mac := hmac.New(sha256.New, []byte(c.aesKey))
	mac.Write([]byte(deviceCodeDomain))
	mac.Write([]byte(strconv.FormatInt(uid, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// DeviceCode exposes the stable per-account device code to integrations that
// must reproduce the same sign identity, such as the CSF export link. It is the
// exactly same value stuSignajax sends as `deviceCode`.
func (c *Client) DeviceCode(uid int64) string {
	return c.deviceCode(uid)
}
