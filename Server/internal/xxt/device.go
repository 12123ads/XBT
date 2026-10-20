package xxt

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// userInfoURL returns the account metadata used for the face device signature.
// The server only reads msg.clientId from it.
const userInfoURL = "https://sso.chaoxing.com/apis/login/userLogin4Uname.do"

// deviceInfoPublicKey is the upstream protocol key that encrypts the device
// profile sent to userInfoURL. It is public material, not a credential.
const deviceInfoPublicKey = "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC79d8Ot0hCbxxSISC6x8SCwTBspFSzlLKHJUYqoFNu1TSRaw4hEYkOnvEaL1VyoxV6HXcDrzwYvaFZaZaPQPFnfCHZy5dQwxcmifgSHqS+oKXw40Ys4cVIqnU5d90S7EWSRdBglX489jlqVaNcQSkDx2TYmC+DbAq9FV/BU09ISQIDAQAB"

// deviceInfoDomain separates the synthetic device profile from other
// derivations of the same account identifiers. Bump it only when the generated
// shape must change, because a stored profile is reused first.
const deviceInfoDomain = "xbt-face-device:v1:"

// DeviceInfo is the synthetic device profile sent to the upstream account-info
// endpoint. Every value is derived from the account or a fixed protocol
// constant; no host hardware identifier is ever collected.
type DeviceInfo struct {
	DeviceUniqueID string `json:"deviceUniqueId"`
	CDID           string `json:"cdid"`
	DeviceID       string `json:"device_id"`
	AndroidID      string `json:"android_id"`
	MediaDRMID     string `json:"mediaDrmId"`
	OAID           string `json:"oaid"`
	Platform       string `json:"platform"`
	OSName         string `json:"os_name"`
	OSVer          string `json:"os_ver"`
	OSLang         string `json:"os_lang"`
	Brand          string `json:"brand"`
	Board          string `json:"board"`
	Hardware       string `json:"hardware"`
	Model          string `json:"model"`
	CPUArch        string `json:"cpu_ar"`
	AppName        string `json:"app_name"`
	AppVer         string `json:"app_ver"`
}

// Valid reports whether every required profile field is present and the three
// unique-id aliases agree. Stored profiles that fail this check are regenerated.
func (d DeviceInfo) Valid() bool {
	return d.DeviceUniqueID != "" &&
		d.DeviceUniqueID == d.CDID && d.CDID == d.DeviceID &&
		d.AndroidID != "" && d.MediaDRMID != "" &&
		d.Platform != "" && d.OSName != "" && d.OSVer != "" && d.OSLang != "" &&
		d.Brand != "" && d.Board != "" && d.Hardware != "" && d.Model != "" &&
		d.CPUArch != "" && d.AppName != "" && d.AppVer != ""
}

// GenerateDeviceInfo derives a stable synthetic profile for one account.
// Deterministic derivation keeps concurrent first logins consistent even if
// only one of them wins the database write.
func GenerateDeviceInfo(uid int64, mobile string) DeviceInfo {
	seed := sha256.Sum256([]byte(deviceInfoDomain + mobile + ":" + strconv.FormatInt(uid, 10)))
	unique := sha256.Sum256(seed[:])
	androidID := hex.EncodeToString(seed[:8])
	mediaDRM := base64.StdEncoding.EncodeToString(seed[16:32])
	return DeviceInfo{
		DeviceUniqueID: hex.EncodeToString(unique[:]),
		CDID:           hex.EncodeToString(unique[:]),
		DeviceID:       hex.EncodeToString(unique[:]),
		AndroidID:      androidID,
		MediaDRMID:     mediaDRM,
		OAID:           "",
		Platform:       "android",
		OSName:         "android",
		OSVer:          "13",
		OSLang:         "zh-CN",
		Brand:          "Xiaomi",
		Board:          "golden",
		Hardware:       "qcom",
		Model:          "2201123C",
		CPUArch:        "arm64-v8a,armeabi-v7a,armeabi",
		AppName:        "com.chaoxing.mobile",
		AppVer:         "6.7.5",
	}
}

// FetchClientID calls the upstream account-info endpoint with the account's
// device profile and returns msg.clientId. An empty clientId is not an error;
// callers must keep working without a face signature.
func (c *Client) FetchClientID(mobile, password string, info DeviceInfo) (string, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	data, err := encryptDeviceInfo(info)
	if err != nil {
		return "", err
	}

	cli := *c.http
	cli.Jar = s.Jar
	cli.Timeout = 10 * time.Second

	form := url.Values{}
	form.Set("data", data)
	req, err := http.NewRequest(http.MethodPost, userInfoURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cli.Do(req)
	if err != nil {
		return "", fmt.Errorf("userInfo request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("userInfo read failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("userInfo: upstream status %d", resp.StatusCode)
	}

	var payload struct {
		Msg struct {
			ClientID string `json:"clientId"`
		} `json:"msg"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("userInfo decode failed: %w", err)
	}
	return strings.TrimSpace(payload.Msg.ClientID), nil
}

func encryptDeviceInfo(info DeviceInfo) (string, error) {
	plain, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("device info encode failed: %w", err)
	}
	key, err := deviceInfoRSAKey()
	if err != nil {
		return "", err
	}
	blockSize := key.Size()
	chunkSize := blockSize - 11 // PKCS#1 v1.5 overhead
	var out []byte
	for offset := 0; offset < len(plain); offset += chunkSize {
		end := offset + chunkSize
		if end > len(plain) {
			end = len(plain)
		}
		block, err := rsa.EncryptPKCS1v15(rand.Reader, key, plain[offset:end])
		if err != nil {
			return "", fmt.Errorf("device info encrypt failed: %w", err)
		}
		out = append(out, block...)
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func deviceInfoRSAKey() (*rsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(deviceInfoPublicKey)
	if err != nil {
		return nil, fmt.Errorf("device info key decode failed: %w", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("device info key parse failed: %w", err)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("device info key is not RSA")
	}
	return key, nil
}

// ClientIdentity is the decrypted clientId payload used for face signing.
// CID becomes faceResult.cxcid and SC is appended to the signToken input.
type ClientIdentity struct {
	CID string
	SC  string
}

// DecryptClientID reverses the upstream clientId obfuscation: each RSA-sized
// block is raised to the public exponent, PKCS#1 v1.5 padding is stripped, and
// the concatenated bytes are parsed as JSON. Any malformed input returns false.
//
// Block type 1 is the upstream signature form. Any other leading block is
// rejected. This is weak obfuscation (the exponent is public), not a security
// boundary.
func DecryptClientID(clientID string) (ClientIdentity, bool) {
	key, err := deviceInfoRSAKey()
	if err != nil {
		return ClientIdentity{}, false
	}
	return decryptClientIDWithKey(clientID, key)
}

func decryptClientIDWithKey(clientID string, key *rsa.PublicKey) (ClientIdentity, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ClientIdentity{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(clientID)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(clientID)
		if err != nil {
			return ClientIdentity{}, false
		}
	}
	blockSize := key.Size()
	if len(raw) == 0 || len(raw)%blockSize != 0 {
		return ClientIdentity{}, false
	}
	exponent := big.NewInt(int64(key.E))
	var plain []byte
	for offset := 0; offset < len(raw); offset += blockSize {
		value := new(big.Int).SetBytes(raw[offset : offset+blockSize])
		if value.Cmp(key.N) >= 0 {
			return ClientIdentity{}, false
		}
		decoded := new(big.Int).Exp(value, exponent, key.N).Bytes()
		if len(decoded) > blockSize {
			return ClientIdentity{}, false
		}
		block := make([]byte, blockSize)
		copy(block[blockSize-len(decoded):], decoded)
		if len(block) < 3 || block[0] != 0 || block[1] != 1 {
			return ClientIdentity{}, false
		}
		separator := bytes.IndexByte(block[2:], 0)
		if separator < 0 {
			return ClientIdentity{}, false
		}
		separator += 2
		if separator <= 2 {
			return ClientIdentity{}, false
		}
		plain = append(plain, block[separator+1:]...)
	}

	var payload struct {
		CID string `json:"cid"`
		SC  string `json:"sc"`
	}
	if err := json.Unmarshal(plain, &payload); err != nil {
		return ClientIdentity{}, false
	}
	if strings.TrimSpace(payload.CID) == "" || strings.TrimSpace(payload.SC) == "" {
		return ClientIdentity{}, false
	}
	return ClientIdentity{CID: payload.CID, SC: payload.SC}, true
}

// faceResultKey resolves the protocol key used to verify clientId before a
// faceResult is signed. Tests replace it to exercise both signed and unsigned
// payloads without the upstream private key.
var faceResultKey = deviceInfoRSAKey

// buildFaceResult assembles the check-face-result payload. The device signature
// is additive: without a usable clientId the payload keeps its original shape.
func buildFaceResult(currentFaceID, cxtime, clientID string) map[string]interface{} {
	key, err := faceResultKey()
	if err != nil {
		return faceResultBase(currentFaceID, cxtime)
	}
	return buildFaceResultWithKey(currentFaceID, cxtime, clientID, key)
}

func buildFaceResultWithKey(currentFaceID, cxtime, clientID string, key *rsa.PublicKey) map[string]interface{} {
	result := faceResultBase(currentFaceID, cxtime)
	identity, ok := decryptClientIDWithKey(clientID, key)
	if !ok {
		return result
	}
	result["cxcid"] = identity.CID
	result["signToken"] = signFaceToken(currentFaceID, cxtime, identity.CID, identity.SC)
	return result
}

func faceResultBase(currentFaceID, cxtime string) map[string]interface{} {
	return map[string]interface{}{
		"currentFaceId":       currentFaceID,
		"LiveDetectionStatus": 1,
		"collectStatus":       1,
		"cxtime":              cxtime,
	}
}

// signFaceToken signs the lexical key order of exactly the five faceResult
// fields plus sc. LiveDetectionStatus and collectStatus are always "1" on the
// wire, matching the signed values.
func signFaceToken(currentFaceID, cxtime, cxcid, sc string) string {
	fields := map[string]string{
		"currentFaceId":       currentFaceID,
		"LiveDetectionStatus": "1",
		"collectStatus":       "1",
		"cxtime":              cxtime,
		"cxcid":               cxcid,
	}
	return faceSignature(fields, sc)
}

func faceSignature(fields map[string]string, sc string) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var raw strings.Builder
	for _, key := range keys {
		raw.WriteString(key)
		raw.WriteString(fields[key])
	}
	raw.WriteString(sc)
	sum := md5.Sum([]byte(raw.String()))
	return hex.EncodeToString(sum[:])
}
