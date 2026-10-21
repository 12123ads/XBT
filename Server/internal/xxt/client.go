package xxt

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SignNormal   = 0
	SignQRCode   = 2
	SignGesture  = 3
	SignLocation = 4
	SignCode     = 5

	// Activity kinds reported by the discovery list (`type`). A value of 74
	// marks a sign-out activity; 2 is a regular sign-in activity.
	ActivityKindSignIn  = 2
	ActivityKindSignOut = 74
	captchaID           = "42sxgHoTPTKbt0uZxPJ7ssOvtXr3ZgZ1"
	captchaType         = "slide"

	signMetadataTTL          = 30 * time.Second
	signEndTimeSentinel      = int64(64060559999000)
	noSignOutPublishSentinel = int64(4999)
)

type signMetadataCacheEntry struct {
	detail    SignDetail
	expiresAt time.Time
}

type Client struct {
	aesKey         string
	mobileUA       string
	signFID        string
	activeFetchMax int
	http           *http.Client
	captchaModel   slideXPredictor
	sessionMu      sync.Mutex
	sessions       map[string]*Session
	metadataMu     sync.Mutex
	metadata       map[string]signMetadataCacheEntry
}

type Session struct {
	Mobile      string
	Password    string
	UID         int64
	Name        string
	Avatar      string
	Jar         *cookiejar.Jar
	LastLoginAt time.Time
}

type LoginResult struct {
	UID    int64
	Name   string
	Avatar string
}

type Course struct {
	Teacher  string
	Name     string
	CourseID int64
	ClassID  int64
	Icon     string
}

// Active is one entry from the course activity list. Type is the activity
// kind (2 = sign-in, 74 = sign-out announcement); zero means the upstream
// payload predates that field. Status/UserStatus are the upstream raw values.
// StartTime/EndTime/SignType/Ext let callers keep and mark an activity even
// when the metadata request fails (XBT-S8).
type Active struct {
	ActiveID   int64
	Name       string
	Type       int
	Status     int
	UserStatus int
	StartTime  int64
	EndTime    int64
	SignType   int
	Ext        string
}

type SignDetail struct {
	StartTime    int64
	EndTime      int64
	SignType     int
	IfRefreshEWM bool
	IfPhoto      bool
	IfFace       bool

	IfNeedVCode        bool
	IfOpenAddress      bool
	LocationLatitude   float64
	LocationLongitude  float64
	LocationRange      int
	SignInID           int64
	SignOutID          int64
	SignOutPublishTime int64
	Ext                string
}

type FixedParams struct {
	ActiveID     int64
	UID          int64
	CourseID     int64
	ClassID      int64
	IfRefreshEWM bool
	// IfOpenAddress mirrors the activity's ifopenAddress flag (XBT-S1 metadata).
	// When true, stuSignajax carries the location parameter set.
	IfOpenAddress bool
	// Ext is the activity's opaque ext field (getPPTActiveInfo). preSign must
	// POST it verbatim; empty means the request carries an empty ext.
	Ext string
}

// New builds the 学习通 client. signFID is the unit/school id sent as `fid` on
// stuSignajax submissions; keep it empty to preserve the historical "no unit"
// request shape.
func New(aesKey, mobileUA, signFID string, insecureTLS bool, activeFetchMax int) *Client {
	tr := &http.Transport{}
	if insecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if activeFetchMax <= 0 {
		activeFetchMax = 20
	}
	return &Client{
		aesKey:         aesKey,
		mobileUA:       mobileUA,
		signFID:        signFID,
		activeFetchMax: activeFetchMax,
		http: &http.Client{
			Timeout:   20 * time.Second,
			Transport: tr,
		},
		sessions: make(map[string]*Session),
	}
}

func (c *Client) PreLogin(mobile, password string) (*LoginResult, error) {
	jar, _ := cookiejar.New(nil)
	cli := *c.http
	cli.Jar = jar

	form := url.Values{}
	form.Set("fid", "-1")
	form.Set("uname", encryptXXTByAES(mobile, c.aesKey))
	form.Set("password", encryptXXTByAES(password, c.aesKey))
	form.Set("refer", "https://i.chaoxing.com")
	form.Set("t", "true")
	form.Set("forbidotherlogin", "0")
	form.Set("validate", "")
	form.Set("doubleFactorLogin", "0")
	form.Set("independentId", "0")
	form.Set("independentNameId", "0")

	req, _ := http.NewRequest(http.MethodPost, "https://passport2.chaoxing.com/fanyalogin?"+form.Encode(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var lr struct {
		Status bool `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("login decode failed: %w", err)
	}
	if !lr.Status {
		return nil, fmt.Errorf("账号或密码错误")
	}

	req2, _ := http.NewRequest(http.MethodGet, "http://i.chaoxing.com/base", nil)
	req2.Header.Set("User-Agent", "Mozilla/5.0")
	resp2, err := cli.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	html, _ := io.ReadAll(resp2.Body)
	s := string(html)

	name := extractBetween(s, `<p class="user-name">`, `</p>`)
	avatar := extractBetween(s, `<img class="icon-head" src="`, `">`)
	uid := int64(0)
	for _, ck := range jar.Cookies(&url.URL{Scheme: "https", Host: "passport2.chaoxing.com"}) {
		if ck.Name == "UID" {
			uid, _ = strconv.ParseInt(ck.Value, 10, 64)
			break
		}
	}
	if uid == 0 {
		for _, ck := range jar.Cookies(&url.URL{Scheme: "https", Host: "i.chaoxing.com"}) {
			if ck.Name == "UID" {
				uid, _ = strconv.ParseInt(ck.Value, 10, 64)
				break
			}
		}
	}
	if uid == 0 {
		return nil, fmt.Errorf("登录后未获取到UID")
	}

	c.sessionMu.Lock()
	c.sessions[mobile] = &Session{
		Mobile:      mobile,
		Password:    password,
		UID:         uid,
		Name:        name,
		Avatar:      avatar,
		Jar:         jar,
		LastLoginAt: time.Now(),
	}
	c.sessionMu.Unlock()

	return &LoginResult{UID: uid, Name: name, Avatar: avatar}, nil
}

func (c *Client) ensureSession(mobile, password string) (*Session, error) {
	c.sessionMu.Lock()
	s, ok := c.sessions[mobile]
	c.sessionMu.Unlock()
	if ok && s.Password == password && time.Since(s.LastLoginAt) < 24*time.Hour {
		return s, nil
	}
	_, err := c.PreLogin(mobile, password)
	if err != nil {
		return nil, err
	}
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	return c.sessions[mobile], nil
}

func (c *Client) CookieHeader(mobile, password, rawURL string) (string, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	targets := []string{
		rawURL,
		"https://sw.qmx.chaoxing.com/",
		"https://i.chaoxing.com/",
		"https://passport2.chaoxing.com/",
	}
	seen := make(map[string]struct{})
	parts := make([]string, 0)
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			continue
		}
		parsed, err := url.Parse(target)
		if err != nil {
			continue
		}
		for _, ck := range s.Jar.Cookies(parsed) {
			if ck.Name == "" {
				continue
			}
			if _, ok := seen[ck.Name]; ok {
				continue
			}
			seen[ck.Name] = struct{}{}
			parts = append(parts, ck.Name+"="+ck.Value)
		}
	}
	return strings.Join(parts, "; "), nil
}

func (c *Client) GetCourses(mobile, password string) ([]Course, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return nil, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	u := "https://mooc1-api.chaoxing.com/mycourse/backclazzdata?view=json&getTchClazzType=1&mcode="
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		ChannelList []struct {
			Content map[string]interface{} `json:"content"`
		} `json:"channelList"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	courses := make([]Course, 0)
	for _, ch := range payload.ChannelList {
		content := ch.Content
		if content == nil {
			continue
		}
		if _, ok := content["folderName"]; ok {
			continue
		}
		if rt, ok := content["roletype"].(float64); ok && int(rt) == 1 {
			continue
		}
		courseMap, ok := content["course"].(map[string]interface{})
		if !ok {
			continue
		}
		dataArr, ok := courseMap["data"].([]interface{})
		if !ok {
			continue
		}
		for _, item := range dataArr {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			squareURL, _ := m["courseSquareUrl"].(string)
			u2, err := url.Parse(squareURL)
			if err != nil {
				continue
			}
			q := u2.Query()
			courseID, _ := strconv.ParseInt(q.Get("courseId"), 10, 64)
			classID, _ := strconv.ParseInt(q.Get("classId"), 10, 64)
			if courseID == 0 || classID == 0 {
				continue
			}
			key := fmt.Sprintf("%d_%d", courseID, classID)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			courses = append(courses, Course{
				Teacher:  strVal(m["teacherfactor"]),
				Name:     strVal(m["name"]),
				CourseID: courseID,
				ClassID:  classID,
				Icon:     strVal(m["imageurl"]),
			})
		}
	}
	return courses, nil
}

func (c *Client) GetActives(mobile, password string, courseID, classID int64) ([]Active, error) {
	return c.getActives(mobile, password, courseID, classID, c.activeFetchMax)
}

func (c *Client) HasActivityInCourse(mobile, password string, courseID, classID, activityID int64) (bool, error) {
	actives, err := c.getActives(mobile, password, courseID, classID, 0)
	if err != nil {
		return false, err
	}
	for _, active := range actives {
		if active.ActiveID == activityID {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) getActives(mobile, password string, courseID, classID int64, limit int) ([]Active, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return nil, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	u := fmt.Sprintf("https://mobilelearn.chaoxing.com/ppt/activeAPI/taskactivelist?courseId=%d&classId=%d", courseID, classID)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("get activities returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	activeList := findActiveList(payload)
	if len(activeList) == 0 {
		activeList = findBestActivityArray(payload)
	}
	if len(activeList) == 0 {
		log.Printf("GetActives empty source: course=%d class=%d body=%s", courseID, classID, truncateForLog(string(raw), 240))
	}
	listExt := findActivityListExt(payload)
	out := make([]Active, 0)
	seen := make(map[int64]struct{})
	for _, a := range activeList {
		kind := int(int64FromAny(firstNonNil(a["type"], a["atype"])))
		activeType := int(int64FromAny(a["activeType"]))
		name := strVal(firstNonNil(a["nameOne"], a["name"], a["activeName"], a["title"]))
		id := int64FromAny(firstNonNil(a["id"], a["activeId"], a["active_id"]))
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		signOut := kind == ActivityKindSignOut || isSignOutName(name)
		signIn := kind == ActivityKindSignIn || signOut
		if kind == 0 {
			// Legacy payloads carry no activity kind: fall back to the old
			// activeType/name heuristic instead of dropping the entry.
			signIn = activeType == ActivityKindSignIn || strings.Contains(name, "签到") || signOut
		}
		if !signIn {
			continue
		}
		if name == "" {
			name = fmt.Sprintf("活动 %d", id)
		}
		ext := signExtString(a["ext"])
		if ext == "" {
			ext = listExt
		}
		out = append(out, Active{
			ActiveID:   id,
			Name:       name,
			Type:       kind,
			Status:     int(int64FromAny(a["status"])),
			UserStatus: int(int64FromAny(firstNonNil(a["userStatus"], a["userstatus"]))),
			StartTime:  parseTimeMillis(firstNonNil(a["startTime"], a["starttime"])),
			EndTime:    parseTimeMillis(firstNonNil(a["endTime"], a["endtime"])),
			SignType:   int(int64FromAny(firstNonNil(a["otherId"], a["otherid"]))),
			Ext:        ext,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *Client) GetSignDetail(mobile, password string, activityID, courseID, classID, uid int64) (SignDetail, error) {
	cacheKey := signMetadataCacheKey(mobile, activityID)
	if detail, ok := c.loadSignMetadata(cacheKey); ok {
		return detail, nil
	}

	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return SignDetail{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	detail, err := c.fetchPPTActiveInfo(cli, activityID)
	if err == nil {
		if detail.EndTime == 0 {
			detail.EndTime = signEndTimeSentinel
		}
		c.storeSignMetadata(cacheKey, detail)
		return detail, nil
	}
	log.Printf("getPPTActiveInfo failed, falling back to signDetail: activity=%d err=%v", activityID, err)

	u := fmt.Sprintf("https://mobilelearn.chaoxing.com/newsign/signDetail?activePrimaryId=%d&type=1", activityID)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return SignDetail{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return SignDetail{}, err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return SignDetail{}, err
	}
	end := parseTimeMillis(payload["endTime"])
	if end == 0 {
		end = signEndTimeSentinel
	}

	ifFace := boolFromAny(deepFindFirst(payload, "ifFaceCheck", "needFace", "ifFace", "faceCheck", "openCheckFaceFlag"))
	if !ifFace {
		ifFace = c.GetFaceFlag(mobile, password, activityID, courseID, classID, uid)
	}

	detail = SignDetail{
		StartTime:    parseTimeMillis(payload["startTime"]),
		EndTime:      end,
		SignType:     int(int64FromAny(payload["otherId"])),
		IfRefreshEWM: boolFromAny(payload["ifRefreshEwm"]),
		IfPhoto:      boolFromAny(deepFindFirst(payload, "ifphoto", "ifPhoto")),
		IfFace:       ifFace,
	}
	c.storeSignMetadata(cacheKey, detail)
	return detail, nil
}

// GetPPTActiveInfo reads the activity metadata endpoint that returns all sign
// flags in one response. An error means the caller should use GetSignDetail,
// which keeps the legacy signDetail + preSign path as a fallback.
func (c *Client) GetPPTActiveInfo(mobile, password string, activityID int64) (SignDetail, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return SignDetail{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar
	return c.fetchPPTActiveInfo(cli, activityID)
}

func (c *Client) fetchPPTActiveInfo(cli http.Client, activityID int64) (SignDetail, error) {
	u := fmt.Sprintf("https://mobilelearn.chaoxing.com/v2/apis/active/getPPTActiveInfo?activeId=%d", activityID)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return SignDetail{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return SignDetail{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return SignDetail{}, fmt.Errorf("getPPTActiveInfo: upstream status %d: %s", resp.StatusCode, truncateForLog(string(raw), 200))
	}
	detail, ok := parsePPTActiveInfo(raw)
	if !ok {
		return SignDetail{}, fmt.Errorf("getPPTActiveInfo: no usable data for activity %d", activityID)
	}
	return detail, nil
}

func parsePPTActiveInfo(raw []byte) (SignDetail, bool) {
	var payload map[string]interface{}
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return SignDetail{}, false
	}
	data, ok := payload["data"].(map[string]interface{})
	if !ok || len(data) == 0 {
		return SignDetail{}, false
	}

	signOutPublishTime := int64FromAny(firstNonNil(data["signOutPublishTimeStamp"], data["signOutPublishTime"]))
	if signOutPublishTime == noSignOutPublishSentinel {
		signOutPublishTime = 0
	}
	center := parseActivityCenter(data)
	return SignDetail{
		StartTime:          parseTimeMillis(firstNonNil(data["starttime"], data["startTime"])),
		EndTime:            parseTimeMillis(firstNonNil(data["endtime"], data["endTime"])),
		SignType:           int(int64FromAny(firstNonNil(data["otherId"], data["otherid"]))),
		IfRefreshEWM:       boolFromAny(firstNonNil(data["ifrefreshewm"], data["ifRefreshEwm"])),
		IfPhoto:            boolFromAny(firstNonNil(data["ifphoto"], data["ifPhoto"])),
		IfFace:             boolFromAny(firstNonNil(data["openCheckFaceFlag"], data["ifFaceCheck"])),
		IfNeedVCode:        boolFromAny(firstNonNil(data["ifNeedVCode"], data["ifneedvcode"])),
		IfOpenAddress:      boolFromAny(firstNonNil(data["ifopenAddress"], data["ifOpenAddress"])),
		LocationLatitude:   center.Latitude,
		LocationLongitude:  center.Longitude,
		LocationRange:      int(int64FromAny(firstNonNil(data["locationRange"], data["locationrange"]))),
		SignInID:           int64FromAny(firstNonNil(data["signInId"], data["signinid"])),
		SignOutID:          int64FromAny(firstNonNil(data["signOutId"], data["signoutid"])),
		SignOutPublishTime: signOutPublishTime,
		Ext:                signExtString(data["ext"]),
	}, true
}

// parseActivityCenter extracts the activity's check-in circle center. The
// documented shape is locationLatitude/locationLongitude, but the endpoint can
// carry zero there while exposing the point in a nested `location` object or a
// JSON string, or under top-level latitude/longitude (XBT-S19). A (0,0) pair is
// missing, not a real center: submitting it makes upstream report a ~12,000 km
// distance. Only the first valid pair is used.
func parseActivityCenter(data map[string]interface{}) GeoPoint {
	if center, ok := centerFromMap(data, []string{"locationLatitude", "locationlatitude"}, []string{"locationLongitude", "locationlongitude"}); ok {
		return center
	}
	for _, key := range []string{"location", "address", "locationInfo"} {
		if center, ok := centerFromValue(data[key]); ok {
			return center
		}
	}
	if center, ok := centerFromMap(data, []string{"latitude", "lat"}, []string{"longitude", "lng", "lon"}); ok {
		return center
	}
	return GeoPoint{}
}

func centerFromValue(v interface{}) (GeoPoint, bool) {
	switch value := v.(type) {
	case map[string]interface{}:
		return centerFromMap(value, []string{"latitude", "lat", "locationLatitude"}, []string{"longitude", "lng", "lon", "locationLongitude"})
	case string:
		trimmed := strings.TrimSpace(value)
		if !strings.HasPrefix(trimmed, "{") {
			return GeoPoint{}, false
		}
		var nested map[string]interface{}
		if json.Unmarshal([]byte(trimmed), &nested) != nil {
			return GeoPoint{}, false
		}
		return centerFromMap(nested, []string{"latitude", "lat", "locationLatitude"}, []string{"longitude", "lng", "lon", "locationLongitude"})
	default:
		return GeoPoint{}, false
	}
}

func centerFromMap(data map[string]interface{}, latitudeKeys, longitudeKeys []string) (GeoPoint, bool) {
	latitude, ok := firstMapValue(data, latitudeKeys...)
	if !ok {
		return GeoPoint{}, false
	}
	longitude, ok := firstMapValue(data, longitudeKeys...)
	if !ok {
		return GeoPoint{}, false
	}
	center := GeoPoint{Latitude: float64FromAny(latitude), Longitude: float64FromAny(longitude)}
	return center, ValidGeoPoint(center)
}

func firstMapValue(data map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, ok := data[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func signExtString(v interface{}) string {
	switch ext := v.(type) {
	case nil:
		return ""
	case string:
		return ext
	default:
		raw, err := json.Marshal(ext)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func signMetadataCacheKey(mobile string, activityID int64) string {
	return mobile + ":" + strconv.FormatInt(activityID, 10)
}

func (c *Client) loadSignMetadata(key string) (SignDetail, bool) {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	entry, ok := c.metadata[key]
	if !ok {
		return SignDetail{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.metadata, key)
		return SignDetail{}, false
	}
	return entry.detail, true
}

func (c *Client) storeSignMetadata(key string, detail SignDetail) {
	now := time.Now()
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	if c.metadata == nil {
		c.metadata = make(map[string]signMetadataCacheEntry)
	}
	for k, entry := range c.metadata {
		if now.After(entry.expiresAt) {
			delete(c.metadata, k)
		}
	}
	c.metadata[key] = signMetadataCacheEntry{detail: detail, expiresAt: now.Add(signMetadataTTL)}
}

func (c *Client) GetFaceFlag(mobile, password string, activityID, courseID, classID, uid int64) bool {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return false
	}
	cli := *c.http
	cli.Jar = s.Jar

	vals := url.Values{}
	vals.Set("courseId", strconv.FormatInt(courseID, 10))
	vals.Set("classId", strconv.FormatInt(classID, 10))
	vals.Set("activePrimaryId", strconv.FormatInt(activityID, 10))
	vals.Set("general", "1")
	vals.Set("sys", "1")
	vals.Set("ls", "1")
	vals.Set("appType", "15")
	vals.Set("uid", strconv.FormatInt(uid, 10))
	vals.Set("isTeacherViewOpen", "0")

	req, _ := http.NewRequest(http.MethodGet, "https://mobilelearn.chaoxing.com/newsign/preSign?"+vals.Encode(), nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	re := regexp.MustCompile(`openCheckFaceFlag\s*=\s*["']?(\d+)["']?`)
	if m := re.FindSubmatch(body); len(m) == 2 {
		return string(m[1]) == "1"
	}
	return false
}

// PreSign performs the pre-sign handshake for every sign type. The handshake
// is a POST carrying the activity's opaque `ext`; QR activities additionally
// carry an `rcode` built from the scanned code/enc pair.
//
// A 403 means the cached session cookie was rejected: the client logs in again
// exactly once and retries exactly once. A second 403 (or a failed re-login)
// returns [ErrSessionExpired]; there is no further retry.
func (c *Client) PreSign(mobile, password string, fixed FixedParams, signType int, code, enc string) (PreSignResult, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return PreSignResult{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	resp, err := c.sendPreSign(&cli, fixed, signType, code, enc)
	if err != nil {
		return PreSignResult{}, err
	}
	if resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if _, err := c.PreLogin(mobile, password); err != nil {
			return PreSignResult{}, fmt.Errorf("%w: %v", ErrSessionExpired, err)
		}
		s, err = c.ensureSession(mobile, password)
		if err != nil {
			return PreSignResult{}, fmt.Errorf("%w: %v", ErrSessionExpired, err)
		}
		cli = *c.http
		cli.Jar = s.Jar
		resp, err = c.sendPreSign(&cli, fixed, signType, code, enc)
		if err != nil {
			return PreSignResult{}, err
		}
		if resp.StatusCode == http.StatusForbidden {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return PreSignResult{}, ErrSessionExpired
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return PreSignResult{}, err
	}
	if resp.StatusCode == http.StatusFound || strings.Contains(string(body), preSignNoPermissionMark) {
		return PreSignResult{Status: PreSignNoPermission, Message: "当前账号无权签到该活动"}, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return PreSignResult{}, fmt.Errorf("preSign: upstream status %d: %s", resp.StatusCode, truncateForLog(string(body), 200))
	}

	c.postSignAnalysis(&cli, fixed.ActiveID)
	return ParsePreSignBody(string(body)), nil
}

func (c *Client) sendPreSign(cli *http.Client, fixed FixedParams, signType int, code, enc string) (*http.Response, error) {
	vals := url.Values{}
	vals.Set("courseId", strconv.FormatInt(fixed.CourseID, 10))
	vals.Set("classId", strconv.FormatInt(fixed.ClassID, 10))
	vals.Set("activePrimaryId", strconv.FormatInt(fixed.ActiveID, 10))
	vals.Set("general", "1")
	vals.Set("sys", "1")
	vals.Set("ls", "1")
	vals.Set("appType", "15")
	vals.Set("uid", strconv.FormatInt(fixed.UID, 10))
	vals.Set("isTeacherViewOpen", "0")
	if signType == SignQRCode && (code != "" || enc != "") {
		rcode := fmt.Sprintf("SIGNIN:aid=%d&source=15&Code=%s&enc=%s", fixed.ActiveID, code, enc)
		vals.Set("rcode", rcode)
	}

	form := url.Values{}
	form.Set("ext", fixed.Ext)
	req, _ := http.NewRequest(http.MethodPost, "https://mobilelearn.chaoxing.com/newsign/preSign?"+vals.Encode(), strings.NewReader(form.Encode()))
	req.Header.Set("User-Agent", c.mobileUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return cli.Do(req)
}

// postSignAnalysis is best effort: it never changes the preSign outcome.
func (c *Client) postSignAnalysis(cli *http.Client, activeID int64) {
	a1 := fmt.Sprintf("https://mobilelearn.chaoxing.com/pptSign/analysis?vs=1&DB_STRATEGY=RANDOM&aid=%d", activeID)
	req, _ := http.NewRequest(http.MethodGet, a1, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`code='\\+'(.*?)'`).FindSubmatch(body)
	if len(m) < 2 {
		return
	}
	code := string(m[1])
	a2 := "https://mobilelearn.chaoxing.com/pptSign/analysis2?DB_STRATEGY=RANDOM&code=" + url.QueryEscape(code)
	req2, _ := http.NewRequest(http.MethodGet, a2, nil)
	req2.Header.Set("User-Agent", c.mobileUA)
	resp2, err := cli.Do(req2)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp2.Body)
		resp2.Body.Close()
	}
}

func (c *Client) GetPanUploadToken(mobile, password string) (string, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	cli := *c.http
	cli.Jar = s.Jar

	req, _ := http.NewRequest(http.MethodGet, "https://pan-yz.chaoxing.com/api/token/uservalid", nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("pan token decode failed: %w", err)
	}
	token := strings.TrimSpace(strVal(deepFindFirst(payload, "_token", "token")))
	if token == "" {
		return "", fmt.Errorf("pan token missing: %s", truncateForLog(string(body), 200))
	}
	return token, nil
}

func (c *Client) UploadPanFile(mobile, password, filename, contentType string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("photo file is empty")
	}
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	token, err := c.GetPanUploadToken(mobile, password)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(filename) == "" {
		filename = "photo.jpg"
	}
	filename = sanitizeMultipartFilename(filename)
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeMultipartFilename(filename)))
	partHeader.Set("Content-Type", contentType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := writer.WriteField("puid", strconv.FormatInt(s.UID, 10)); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	cli := *c.http
	cli.Jar = s.Jar
	uploadURL := "https://pan-yz.chaoxing.com/upload?_from=mobilelearn&_token=" + url.QueryEscape(token)
	req, _ := http.NewRequest(http.MethodPost, uploadURL, &body)
	req.Header.Set("User-Agent", c.mobileUA)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("pan upload failed: http %d %s", resp.StatusCode, truncateForLog(string(respBody), 200))
	}

	var payload interface{}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", fmt.Errorf("pan upload decode failed: %w", err)
	}
	objectID := strings.TrimSpace(strVal(deepFindFirst(payload, "objectId", "objectid")))
	if objectID == "" {
		return "", fmt.Errorf("pan upload objectId missing: %s", truncateForLog(string(respBody), 200))
	}
	return objectID, nil
}

type FaceCheckResult struct {
	CurrentFaceID string
	FaceEnc       string
	FaceCode      string
}

// ErrFaceRejected marks a completed check-face-result request that did not
// return an enc, i.e. the upstream rejected this photo. Transport, HTTP and
// decoding failures are deliberately not wrapped in it.
var ErrFaceRejected = errors.New("face verification rejected")

// FaceRejectedError carries the upstream rejection facts without exposing the
// whole response in the error string. Code is the upstream `code` field
// (e.g. CFR004), Message is the readable upstream message, and Response is the
// raw body for bounded diagnostic logging.
type FaceRejectedError struct {
	Code     string
	Message  string
	Response string
}

func (e *FaceRejectedError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = truncateForLog(strings.TrimSpace(e.Response), 200)
	}
	if msg == "" {
		msg = "no readable upstream message"
	}
	return fmt.Sprintf("%s: 人脸识别失败: %s", ErrFaceRejected.Error(), msg)
}

// Unwrap makes errors.Is(err, ErrFaceRejected) work for callers that only need
// to know the photo was rejected.
func (e *FaceRejectedError) Unwrap() error { return ErrFaceRejected }

func (c *Client) UploadFaceTemp(mobile, password, filename, contentType string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("face photo is empty")
	}
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	token, err := c.GetPanUploadToken(mobile, password)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(filename) == "" {
		filename = "face.jpg"
	}
	filename = sanitizeMultipartFilename(filename)
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeMultipartFilename(filename)))
	partHeader.Set("Content-Type", contentType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := writer.WriteField("puid", strconv.FormatInt(s.UID, 10)); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	cli := *c.http
	cli.Jar = s.Jar
	uploadParams := url.Values{}
	uploadParams.Set("_from", "mobilelearn")
	uploadParams.Set("uploadtype", "face_temp")
	uploadParams.Set("_token", token)
	uploadURL := "https://pan-yz.chaoxing.com/upload?" + uploadParams.Encode()
	req, _ := http.NewRequest(http.MethodPost, uploadURL, &body)
	req.Header.Set("User-Agent", c.mobileUA)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("face_temp upload failed: http %d %s", resp.StatusCode, truncateForLog(string(respBody), 200))
	}

	var payload interface{}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", fmt.Errorf("face_temp upload decode failed: %w", err)
	}
	objectID := strings.TrimSpace(strVal(deepFindFirst(payload, "objectId", "objectid")))
	if objectID == "" {
		return "", fmt.Errorf("face_temp upload objectId missing: %s", truncateForLog(string(respBody), 200))
	}
	return objectID, nil
}

func (c *Client) CheckFaceResult(mobile, password string, activeID int64, faceObjectID, clientID string) (FaceCheckResult, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return FaceCheckResult{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	cxtime := strconv.FormatInt(time.Now().UnixMilli(), 10)
	faceResult := buildFaceResult(faceObjectID, cxtime, clientID)
	faceResultJSON, err := json.Marshal(faceResult)
	if err != nil {
		return FaceCheckResult{}, fmt.Errorf("marshal faceResult failed: %w", err)
	}

	params := url.Values{}
	params.Set("DB_STRATEGY", "PRIMARY_KEY")
	params.Set("STRATEGY_PARA", "activeId")
	params.Set("activeId", strconv.FormatInt(activeID, 10))
	params.Set("faceResult", string(faceResultJSON))

	reqURL := "https://mobilelearn.chaoxing.com/pptSign/check-face-result?" + params.Encode()
	req, _ := http.NewRequest(http.MethodGet, reqURL, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return FaceCheckResult{}, fmt.Errorf("check-face-result request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return FaceCheckResult{}, fmt.Errorf("check-face-result read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FaceCheckResult{}, fmt.Errorf("check-face-result returned http %d: %s", resp.StatusCode, truncateForLog(string(respBody), 200))
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return FaceCheckResult{}, fmt.Errorf("check-face-result decode failed: http %d: %w", resp.StatusCode, err)
	}

	faceEnc := strings.TrimSpace(strVal(deepFindFirst(payload, "enc", "faceEnc", "face_enc")))
	if faceEnc == "" {
		msg := strVal(firstNonNil(payload["errorMsg"], payload["msg"], payload["message"], payload["code"]))
		if msg == "" {
			msg = truncateForLog(string(respBody), 200)
		}
		return FaceCheckResult{}, &FaceRejectedError{
			Code:     strings.TrimSpace(strVal(payload["code"])),
			Message:  msg,
			Response: string(respBody),
		}
	}

	faceCode := strings.TrimSpace(strVal(payload["code"]))

	return FaceCheckResult{
		CurrentFaceID: faceObjectID,
		FaceEnc:       faceEnc,
		FaceCode:      faceCode,
	}, nil
}

func (c *Client) SignPhoto(mobile, password string, fixed FixedParams, objectID string) (SignResult, error) {
	objectID = strings.TrimSpace(objectID)
	if objectID == "" {
		return SignResult{}, fmt.Errorf("missing photo object_id")
	}
	return c.Sign(mobile, password, fixed, SignNormal, map[string]interface{}{"object_id": objectID})
}

func (c *Client) Sign(mobile, password string, fixed FixedParams, signType int, special map[string]interface{}) (SignResult, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return SignResult{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	if signType == SignQRCode && strVal(special["enc"]) == "" {
		return SignResult{}, fmt.Errorf("缺少二维码 enc 参数")
	}
	if (signType == SignGesture || signType == SignCode) && strVal(special["sign_code"]) == "" {
		return SignResult{}, fmt.Errorf("缺少 sign_code 参数")
	}
	if signType == SignGesture {
		if err := c.checkGestureCode(&cli, fixed.ActiveID, strVal(special["sign_code"])); err != nil {
			return SignResult{}, err
		}
	}

	// 参数集由 sign_params.go 统一构造；enc2/validate 只透传状态机写入的值，
	// 二维码首发不再预置 validate。
	params := buildStuSignParams(stuSignParams{
		SignType:      signType,
		ActivityID:    fixed.ActiveID,
		CourseID:      fixed.CourseID,
		UID:           fixed.UID,
		Name:          s.Name,
		FID:           c.signFID,
		DeviceCode:    c.deviceCode(fixed.UID),
		IfOpenAddress: fixed.IfOpenAddress,
		Position:      PositionFromSpecial(special),
		Enc:           strVal(special["enc"]),
		SignCode:      strVal(special["sign_code"]),
		ObjectID:      strings.TrimSpace(strVal(firstNonNil(special["object_id"], special["objectId"]))),
		CurrentFaceID: strings.TrimSpace(strVal(special["current_face_id"])),
		FaceEnc:       strings.TrimSpace(strVal(special["face_enc"])),
		Enc2:          strings.TrimSpace(strVal(special["enc2"])),
		Validate:      strings.TrimSpace(strVal(special["validate"])),
	})

	result, err := c.doStuSignRequest(&cli, params, 1)
	if err != nil {
		return SignResult{}, err
	}
	if result.Kind != SignResultCaptchaRequired {
		return result, nil
	}

	// Captcha challenge: save enc2, solve the captcha once, and retry the
	// same submission exactly once with both validate and enc2.
	validate, err := c.fetchCaptchaValidate(&cli, fixed)
	if err != nil {
		log.Printf("captcha fetch failed: activity=%d uid=%d err=%v", fixed.ActiveID, fixed.UID, err)
		return result, nil
	}
	if result.Enc2 != "" {
		params.Set("enc2", result.Enc2)
	}
	params.Set("validate", validate)
	retried, err := c.doStuSignRequest(&cli, params, result.Attempts+1)
	if err != nil {
		return SignResult{}, err
	}
	retried.CaptchaAttempted = true
	switch retried.Kind {
	case SignResultSuccess, SignResultAlreadySigned:
		retried.CaptchaPassed = true
	}
	return retried, nil
}

func (c *Client) doStuSignRequest(cli *http.Client, params url.Values, attempt int) (SignResult, error) {
	u := "https://mobilelearn.chaoxing.com/pptSign/stuSignajax?" + params.Encode()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return SignResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SignResult{}, err
	}
	result := ParseSignResult(string(body))
	result.Attempts = attempt
	result.HTTPStatus = resp.StatusCode
	if resp.StatusCode == http.StatusForbidden {
		result.Kind = SignResultSessionExpired
		result.Message = "上游返回 HTTP 403"
	} else if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		result.Kind = SignResultUpstreamError
		result.Message = fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
	}
	return result, nil
}

// checkGestureCode verifies a gesture/order code before submitting the sign.
// Transport failures are non-fatal (the sign request itself decides); an explicit
// upstream rejection aborts with the upstream message.
func (c *Client) checkGestureCode(cli *http.Client, activeID int64, signCode string) error {
	checkURL := fmt.Sprintf("https://mobilelearn.chaoxing.com/widget/sign/pcStuSignController/checkSignCode?activeId=%d&signCode=%s", activeID, url.QueryEscape(signCode))
	req, _ := http.NewRequest(http.MethodGet, checkURL, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var check struct {
		Result   int    `json:"result"`
		ErrorMsg string `json:"errorMsg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&check)
	if check.Result != 1 {
		if check.ErrorMsg == "" {
			check.ErrorMsg = "手势码校验失败"
		}
		return fmt.Errorf("%s", check.ErrorMsg)
	}
	return nil
}

func sanitizeMultipartFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "photo.jpg"
	}
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\x00", "")
	return filename
}

func escapeMultipartFilename(filename string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(filename)
}
