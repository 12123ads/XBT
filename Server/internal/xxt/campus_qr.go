package xxt

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	campusQRFID       = "75096"
	campusQRMAppID    = "6619490"
	campusQRAppID     = "a1ffad56c5d94efb888714e936a92490"
	campusQRClientID  = "65041f0609214b06ad7e9199f6df459c"
	campusQRChannel   = "swut_cx"
	campusQRPortalURL = "https://portal.swut.cn/"
)

type CampusQR struct {
	FullName      string `json:"full_name"`
	EffectAccount string `json:"effect_account"`
	BarContent    string `json:"bar_content"`
	QRContent     string `json:"qr_content"`
	ExpireTime    string `json:"expire_time"`
}

func (c *Client) GetCampusQR(mobile, password string) (CampusQR, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return CampusQR{}, err
	}
	jar, _ := cookiejar.New(nil)
	copyCookies(jar, s.Jar, []string{
		"https://i.chaoxing.com/",
		"https://auth.chaoxing.com/",
		"https://passport2.chaoxing.com/",
	})
	cli := &http.Client{
		Timeout:   c.http.Timeout,
		Transport: c.http.Transport,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	authURL, err := c.campusQRRedirect(cli)
	if err != nil {
		return CampusQR{}, err
	}
	portalURL, err := c.campusQRRedirectURL(cli, authURL, "https://i.chaoxing.com/")
	if err != nil {
		return CampusQR{}, err
	}
	code, state, appID, err := parseCampusQRPortalURL(portalURL)
	if err != nil {
		return CampusQR{}, err
	}
	if appID == "" {
		appID = campusQRAppID
	}
	ssoToken := ensureCampusQRSSOToken(jar)
	access, err := c.campusQRLogin(cli, ssoToken, code, state, appID)
	if err != nil {
		return CampusQR{}, err
	}
	if err := c.campusQRGo(cli, access.AccessToken, firstNonEmpty(access.AssociatedApplication, campusQRClientID), firstNonEmpty(access.VisitChannel, campusQRChannel)); err != nil {
		return CampusQR{}, err
	}
	return c.campusQRShow(cli, access.AccessToken)
}

func (c *Client) campusQRRedirect(cli *http.Client) (string, error) {
	u := "https://i.chaoxing.com/wfw/space/redirectUrl?fid=" + campusQRFID + "&type=3&appCenter=1&mAppId=" + campusQRMAppID
	return c.campusQRRedirectURL(cli, u, "https://i.chaoxing.com/")
}

func (c *Client) campusQRRedirectURL(cli *http.Client, rawURL, referer string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("campus qr redirect failed: status=%d body=%s", resp.StatusCode, truncateForLog(string(body), 160))
	}
	location := resp.Header.Get("Location")
	if strings.TrimSpace(location) == "" {
		return "", fmt.Errorf("campus qr redirect missing location")
	}
	return location, nil
}

type campusQRLoginResult struct {
	AccessToken           string `json:"accessToken"`
	AssociatedApplication string `json:"associatedApplication"`
	VisitChannel          string `json:"visitChannel"`
}

func (c *Client) campusQRLogin(cli *http.Client, token, code, state, appID string) (campusQRLoginResult, error) {
	payload := map[string]string{
		"code":  code,
		"state": state,
		"appId": appID,
	}
	var out struct {
		State   bool                `json:"state"`
		Success bool                `json:"success"`
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Data    campusQRLoginResult `json:"data"`
	}
	if err := c.campusQRJSON(cli, http.MethodPost, "https://main.swut.cn/sso/xinLogin", token, payload, &out); err != nil {
		return campusQRLoginResult{}, err
	}
	if !campusQROK(out.State, out.Success, out.Code) || out.Data.AccessToken == "" {
		return campusQRLoginResult{}, fmt.Errorf("campus qr login failed: %s", firstNonEmpty(out.Message, out.Code))
	}
	return out.Data, nil
}

func (c *Client) campusQRGo(cli *http.Client, token, clientID, channel string) error {
	payload := map[string]string{
		"client_id": clientID,
		"channel":   channel,
		"portal":    "pc",
	}
	var out struct {
		State   bool   `json:"state"`
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := c.campusQRJSON(cli, http.MethodPost, "https://main.swut.cn/sso/go", token, payload, &out); err != nil {
		return err
	}
	if !campusQROK(out.State, out.Success, out.Code) {
		return fmt.Errorf("campus qr sso go failed: %s", firstNonEmpty(out.Message, out.Code))
	}
	return nil
}

func (c *Client) campusQRShow(cli *http.Client, token string) (CampusQR, error) {
	var out struct {
		State   bool     `json:"state"`
		Success bool     `json:"success"`
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Data    CampusQR `json:"data"`
	}
	if err := c.campusQRJSON(cli, http.MethodGet, "https://main.swut.cn/qr/campusCard/show", token, nil, &out); err != nil {
		return CampusQR{}, err
	}
	if !campusQROK(out.State, out.Success, out.Code) || out.Data.QRContent == "" {
		return CampusQR{}, fmt.Errorf("campus qr show failed: %s", firstNonEmpty(out.Message, out.Code))
	}
	return out.Data, nil
}

func campusQROK(state, success bool, code string) bool {
	return state || success || strings.EqualFold(strings.TrimSpace(code), "success")
}

func (c *Client) campusQRJSON(cli *http.Client, method, rawURL, token string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, rawURL, body)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", campusQRPortalURL[:len(campusQRPortalURL)-1])
	req.Header.Set("Referer", campusQRPortalURL)
	if token != "" {
		req.Header.Set("token", token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("campus qr request failed: status=%d body=%s", resp.StatusCode, truncateForLog(string(raw), 160))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func parseCampusQRPortalURL(rawURL string) (code, state, appID string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", err
	}
	fragment := u.Fragment
	if idx := strings.Index(fragment, "?"); idx >= 0 {
		q, err := url.ParseQuery(fragment[idx+1:])
		if err != nil {
			return "", "", "", err
		}
		code = q.Get("code")
		state = q.Get("state")
		appID = q.Get("appId")
	}
	if code == "" || state == "" {
		q := u.Query()
		code = firstNonEmpty(code, q.Get("code"))
		state = firstNonEmpty(state, q.Get("state"))
		appID = firstNonEmpty(appID, q.Get("appId"))
	}
	if code == "" || state == "" {
		return "", "", "", fmt.Errorf("campus qr oauth result missing code/state")
	}
	return code, state, appID, nil
}

func ensureCampusQRSSOToken(jar *cookiejar.Jar) string {
	portalURL, _ := url.Parse(campusQRPortalURL)
	for _, ck := range jar.Cookies(portalURL) {
		if ck.Name == "ssoToken" && ck.Value != "" {
			return ck.Value
		}
	}
	token := randomCampusQRToken()
	jar.SetCookies(portalURL, []*http.Cookie{{
		Name:    "ssoToken",
		Value:   token,
		Path:    "/",
		Expires: time.Now().Add(10 * time.Minute),
	}})
	return token
}

func randomCampusQRToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconvFormatCampusQRToken(time.Now().UnixNano())
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
	if len(encoded) > 12 {
		return encoded[:12]
	}
	return encoded
}

func strconvFormatCampusQRToken(v int64) string {
	return strings.ToLower(fmt.Sprintf("%x", v))
}

func copyCookies(dst http.CookieJar, src http.CookieJar, rawURLs []string) {
	for _, rawURL := range rawURLs {
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		dst.SetCookies(u, src.Cookies(u))
	}
}
