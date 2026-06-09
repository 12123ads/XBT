package qmx

import (
	"bytes"
	"crypto/cipher"
	"crypto/des"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	apiBase    = "https://sw.qmx.chaoxing.com"
	qmxReferer = "https://sw.qmx.chaoxing.com/mobile/?v=68cc80a69"
	desKey     = "QRCODENC"
	checkType  = "0"
)

type Client struct {
	http *http.Client
}

type CredentialInput struct {
	QMXURL string
	XToken string
	Cookie string
	Raw    string
}

type Preview struct {
	BatchName    string       `json:"batch_name"`
	CheckDate    string       `json:"check_date"`
	LateDate     string       `json:"late_date"`
	StartTime    string       `json:"start_time"`
	EndTime      string       `json:"end_time"`
	LateEndTime  string       `json:"late_end_time"`
	Cqfs         string       `json:"cqfs"`
	Locations    []Location   `json:"locations"`
	Requirements Requirements `json:"requirements"`
	Unsupported  []string     `json:"unsupported"`
	RawCode      any          `json:"raw_code,omitempty"`
	RawMessage   string       `json:"raw_message,omitempty"`
}

type Location struct {
	Name     string  `json:"name"`
	Lng      float64 `json:"lng"`
	Lat      float64 `json:"lat"`
	Range    int     `json:"range"`
	Distance float64 `json:"distance,omitempty"`
}

type Requirements struct {
	PhotoRequired     bool `json:"photo_required"`
	FaceRequired      bool `json:"face_required"`
	BluetoothRequired bool `json:"bluetooth_required"`
	SpecialSDK        bool `json:"special_sdk"`
}

type ExecuteInput struct {
	CredentialInput
	LocationIndex        int
	Longitude            float64
	Latitude             float64
	LocationName         string
	RequireLocationMatch bool
	UseProvidedLocation  bool
}

type ExecuteResult struct {
	Success      bool     `json:"success"`
	Code         any      `json:"code"`
	Message      string   `json:"message"`
	BatchName    string   `json:"batch_name"`
	CheckDate    string   `json:"check_date"`
	CheckTime    string   `json:"check_time"`
	LocationName string   `json:"location_name"`
	Longitude    float64  `json:"longitude"`
	Latitude     float64  `json:"latitude"`
	Unsupported  []string `json:"unsupported,omitempty"`
}

type credential struct {
	token  string
	cookie string
}

type apiResponse struct {
	Success bool            `json:"success"`
	Code    any             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type studentInfoData struct {
	Cqrq  string `json:"cqrq"`
	Wgrq  string `json:"wgrq"`
	Batch batch  `json:"batch"`
}

type batch struct {
	ID            string          `json:"id"`
	Pcmc          string          `json:"pcmc"`
	Cqfs          string          `json:"cqfs"`
	Qdwz          json.RawMessage `json:"qdwz"`
	Cqkssj        string          `json:"cqkssj"`
	Cqjssj        string          `json:"cqjssj"`
	Wgjssj        string          `json:"wgjssj"`
	Pzyq          json.RawMessage `json:"pzyq"`
	Lyjyyq        json.RawMessage `json:"lyjyyq"`
	Sfyxtsdk      string          `json:"sfyxtsdk"`
	BluetoothList json.RawMessage `json:"bluetoothList"`
	LdID          string          `json:"ldId"`
	CwID          string          `json:"cwId"`
	XsID          string          `json:"xsId"`
}

func New(insecureTLS bool) *Client {
	tr := &http.Transport{}
	if insecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		http: &http.Client{
			Timeout:   20 * time.Second,
			Transport: tr,
		},
	}
}

func (c *Client) Preview(input CredentialInput) (Preview, error) {
	cred, err := c.resolveCredential(input)
	if err != nil {
		return Preview{}, err
	}
	info, raw, err := c.getStudentInfo(cred)
	if err != nil {
		return Preview{}, err
	}
	preview, err := buildPreview(info, raw)
	if err != nil {
		return Preview{}, err
	}
	return preview, nil
}

func (c *Client) Execute(input ExecuteInput) (ExecuteResult, error) {
	cred, err := c.resolveCredential(input.CredentialInput)
	if err != nil {
		return ExecuteResult{}, err
	}
	info, raw, err := c.getStudentInfo(cred)
	if err != nil {
		return ExecuteResult{}, err
	}
	preview, err := buildPreview(info, raw)
	if err != nil {
		return ExecuteResult{}, err
	}
	if len(preview.Unsupported) > 0 {
		return ExecuteResult{
			BatchName:   preview.BatchName,
			CheckDate:   preview.CheckDate,
			Unsupported: preview.Unsupported,
		}, fmt.Errorf("unsupported room check requirements: %s", strings.Join(preview.Unsupported, ", "))
	}

	loc, err := selectLocation(preview.Locations, input)
	if err != nil {
		return ExecuteResult{
			BatchName: preview.BatchName,
			CheckDate: preview.CheckDate,
		}, err
	}
	checkTime := time.Now().Format("2006-01-02 15:04:05")
	cqfs := info.Batch.Cqfs
	if cqfs == "" {
		cqfs = checkType
	}
	payload := map[string]string{
		"jg":   "1",
		"sj":   checkTime,
		"rq":   info.Cqrq,
		"pcId": info.Batch.ID,
		"ldId": info.Batch.LdID,
		"cwId": info.Batch.CwID,
		"xsId": info.Batch.XsID,
		"cqfs": cqfs,
		"dkwz": loc.Name,
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return ExecuteResult{}, err
	}
	jsonStr, err := desECBPKCS7EncryptHex(plain, []byte(desKey))
	if err != nil {
		return ExecuteResult{}, err
	}
	reqBody, err := json.Marshal(map[string]string{"jsonStr": jsonStr})
	if err != nil {
		return ExecuteResult{}, err
	}
	body, err := c.doJSON(http.MethodPost, apiBase+"/housemaster/sg/roomCheckPunch/clockIn", cred, reqBody)
	if err != nil {
		return ExecuteResult{}, err
	}
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ExecuteResult{}, fmt.Errorf("decode QMX clockIn response failed: %w", err)
	}
	return ExecuteResult{
		Success:      resp.Success,
		Code:         resp.Code,
		Message:      resp.Message,
		BatchName:    preview.BatchName,
		CheckDate:    preview.CheckDate,
		CheckTime:    checkTime,
		LocationName: loc.Name,
		Longitude:    loc.Lng,
		Latitude:     loc.Lat,
	}, nil
}

func (c *Client) getStudentInfo(cred credential) (studentInfoData, apiResponse, error) {
	body, err := c.doJSON(http.MethodGet, apiBase+"/housemaster/sg/roomCheckPunch/getStudentInfo?cqfs=1", cred, nil)
	if err != nil {
		return studentInfoData{}, apiResponse{}, err
	}
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return studentInfoData{}, apiResponse{}, fmt.Errorf("decode QMX preview response failed: %w", err)
	}
	if !resp.Success {
		return studentInfoData{}, resp, fmt.Errorf("QMX preview failed: code=%v message=%s", resp.Code, resp.Message)
	}
	var info studentInfoData
	if err := json.Unmarshal(resp.Data, &info); err != nil {
		return studentInfoData{}, resp, fmt.Errorf("decode QMX student info failed: %w", err)
	}
	return info, resp, nil
}

func (c *Client) doJSON(method, endpoint string, cred credential, reqBody []byte) ([]byte, error) {
	var reader io.Reader
	if reqBody != nil {
		reader = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Token", cred.token)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Client-Ignore-Error-Status", "true")
	req.Header.Set("Referer", qmxReferer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	if cred.cookie != "" {
		req.Header.Set("Cookie", cred.cookie)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("QMX http status %d: %s", resp.StatusCode, truncate(string(body), 240))
	}
	return body, nil
}

func (c *Client) resolveCredential(input CredentialInput) (credential, error) {
	combined := strings.Join([]string{input.Raw, input.Cookie, input.XToken, input.QMXURL}, "\n")
	token := strings.TrimSpace(input.XToken)
	cookie := strings.TrimSpace(input.Cookie)

	if token == "" {
		token = firstMatch(combined, `(?mi)^X-Token:\s*([^\r\n]+)`)
	}
	if cookie == "" {
		cookie = firstMatch(combined, `(?mi)^Cookie:\s*([^\r\n]+)`)
	}
	if cookie == "" && looksLikeCookie(input.Raw) {
		cookie = strings.TrimSpace(input.Raw)
	}
	if token == "" {
		token = cookieValue(cookie, "cx_qmx_token")
	}
	if token == "" {
		token = firstMatch(combined, `cx_qmx_token=([^;\s]+)`)
	}
	if token == "" && looksLikeJWT(input.Raw) {
		token = strings.TrimSpace(input.Raw)
	}
	var loginErr error
	if token == "" && cookie != "" {
		loginToken, err := c.ermLogin(cookie)
		if err == nil {
			token = loginToken
		} else {
			loginErr = err
		}
	}
	if token == "" && cookie == "" && strings.TrimSpace(input.QMXURL) != "" {
		token = tokenFromQMXURL(input.QMXURL)
	}
	if token == "" {
		if loginErr != nil {
			return credential{}, loginErr
		}
		return credential{}, errors.New("missing QMX X-Token or cx_qmx_token cookie")
	}
	return credential{token: token, cookie: cookie}, nil
}

func (c *Client) ermLogin(cookie string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, apiBase+"/pedestal/user/ermLogin", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", qmxReferer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("QMX ermLogin http status %d", resp.StatusCode)
	}
	var payload struct {
		Success bool   `json:"success"`
		Code    any    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if !payload.Success || payload.Data.Token == "" {
		return "", fmt.Errorf("QMX ermLogin failed: code=%v message=%s", payload.Code, payload.Message)
	}
	return payload.Data.Token, nil
}

func tokenFromQMXURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	if token := parsed.Query().Get("wfw_token"); token != "" {
		return token
	}
	fragment := parsed.Fragment
	if idx := strings.Index(fragment, "?"); idx >= 0 && idx+1 < len(fragment) {
		values, err := url.ParseQuery(fragment[idx+1:])
		if err == nil {
			return values.Get("wfw_token")
		}
	}
	return ""
}

func buildPreview(info studentInfoData, raw apiResponse) (Preview, error) {
	locations, err := parseLocations(info.Batch.Qdwz)
	if err != nil {
		return Preview{}, err
	}
	reqs := detectRequirements(info.Batch)
	unsupported := make([]string, 0)
	if reqs.PhotoRequired {
		unsupported = append(unsupported, "photo")
	}
	if reqs.FaceRequired {
		unsupported = append(unsupported, "face")
	}
	if reqs.BluetoothRequired {
		unsupported = append(unsupported, "bluetooth")
	}
	if reqs.SpecialSDK {
		unsupported = append(unsupported, "special_sdk")
	}
	return Preview{
		BatchName:   info.Batch.Pcmc,
		CheckDate:   info.Cqrq,
		LateDate:    info.Wgrq,
		StartTime:   info.Batch.Cqkssj,
		EndTime:     info.Batch.Cqjssj,
		LateEndTime: info.Batch.Wgjssj, Cqfs: info.Batch.Cqfs, Locations: locations,
		Requirements: reqs,
		Unsupported:  unsupported,
		RawCode:      raw.Code,
		RawMessage:   raw.Message,
	}, nil
}

func parseLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("QMX response has no location list")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		text = string(raw)
	}
	var locations []Location
	if err := json.Unmarshal([]byte(text), &locations); err != nil {
		return nil, fmt.Errorf("decode QMX location list failed: %w", err)
	}
	return locations, nil
}

func detectRequirements(b batch) Requirements {
	return Requirements{
		PhotoRequired:     hasJSONStatus(b.Pzyq, "1"),
		FaceRequired:      hasJSONStatus(b.Pzyq, "2"),
		BluetoothRequired: hasMeaningfulJSON(b.Lyjyyq) || hasMeaningfulJSON(b.BluetoothList),
		SpecialSDK:        strings.TrimSpace(b.Sfyxtsdk) == "1",
	}
}

func hasJSONStatus(raw json.RawMessage, status string) bool {
	if !hasMeaningfulJSON(raw) {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	return fmt.Sprint(m["status"]) == status
}

func hasMeaningfulJSON(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "{}" && s != "[]"
}

func selectLocation(locations []Location, input ExecuteInput) (Location, error) {
	if input.UseProvidedLocation {
		if strings.TrimSpace(input.LocationName) == "" || input.Longitude == 0 || input.Latitude == 0 {
			return Location{}, errors.New("provided QMX location is incomplete")
		}
		return Location{
			Name: input.LocationName,
			Lng:  input.Longitude,
			Lat:  input.Latitude,
		}, nil
	}
	if len(locations) == 0 {
		return Location{}, errors.New("no allowed QMX locations")
	}
	if input.LocationName != "" {
		for _, loc := range locations {
			if loc.Name == input.LocationName {
				return loc, nil
			}
		}
	}
	if input.LocationIndex >= 0 && input.LocationIndex < len(locations) {
		return locations[input.LocationIndex], nil
	}
	if input.Longitude != 0 && input.Latitude != 0 {
		best := locations[0]
		best.Distance = distanceMeters(input.Longitude, input.Latitude, best.Lng, best.Lat)
		for _, loc := range locations[1:] {
			d := distanceMeters(input.Longitude, input.Latitude, loc.Lng, loc.Lat)
			if d < best.Distance {
				best = loc
				best.Distance = d
			}
		}
		if best.Range > 0 && best.Distance > float64(best.Range) {
			return Location{}, fmt.Errorf("current location is %.0fm from nearest allowed point, outside %dm range", best.Distance, best.Range)
		}
		return best, nil
	}
	if input.RequireLocationMatch {
		return Location{}, errors.New("saved QMX location is not available, please choose again")
	}
	return locations[0], nil
}

func distanceMeters(lng1, lat1, lng2, lat2 float64) float64 {
	const earthRadius = 6371000
	rad := func(v float64) float64 { return v * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(a))
}

func desECBPKCS7EncryptHex(src, key []byte) (string, error) {
	block, err := des.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(src, block.BlockSize())
	dst := make([]byte, len(padded))
	ecb := newECBEncrypter(block)
	ecb.CryptBlocks(dst, padded)
	return hex.EncodeToString(dst), nil
}

func pkcs7Pad(src []byte, blockSize int) []byte {
	padding := blockSize - len(src)%blockSize
	out := make([]byte, len(src)+padding)
	copy(out, src)
	for i := len(src); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

type ecbEncrypter struct {
	b         cipher.Block
	blockSize int
}

func newECBEncrypter(b cipher.Block) cipher.BlockMode {
	return &ecbEncrypter{b: b, blockSize: b.BlockSize()}
}

func (x *ecbEncrypter) BlockSize() int { return x.blockSize }

func (x *ecbEncrypter) CryptBlocks(dst, src []byte) {
	if len(src)%x.blockSize != 0 {
		panic("input not full blocks")
	}
	if len(dst) < len(src) {
		panic("output smaller than input")
	}
	for len(src) > 0 {
		x.b.Encrypt(dst[:x.blockSize], src[:x.blockSize])
		src = src[x.blockSize:]
		dst = dst[x.blockSize:]
	}
}

func firstMatch(s, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func cookieValue(cookie, key string) string {
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		name, value, ok := strings.Cut(part, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

func looksLikeCookie(s string) bool {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "\n") || !strings.Contains(s, "=") || !strings.Contains(s, ";") {
		return false
	}
	return strings.Contains(s, "UID=") || strings.Contains(s, "vc3=") || strings.Contains(s, "cx_qmx_token=") || strings.Contains(s, "p_auth_token=")
}

func looksLikeJWT(s string) bool {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " \r\n\t;=") {
		return false
	}
	return strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
