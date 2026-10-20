package xxt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// imUserInfoURL is the account-info endpoint that carries the Easemob account
// password. It is a variable so tests can point it at a local server.
var imUserInfoURL = "https://sso.chaoxing.com/apis/login/userLogin4Uname.do"

// ErrIMAccountMissing means the upstream account has no Easemob IM account.
var ErrIMAccountMissing = errors.New("im account missing")

// IMAccount is the upstream Easemob identity. Password is the DES-encrypted
// hex value returned by 学习通; callers must decrypt it before use.
type IMAccount struct {
	UID      int64
	Username string
	Password string
}

// GetIMAccount reads msg.accountInfo.imAccount from the userInfo endpoint using
// the existing 学习通 session. It never logs or returns the upstream body.
func (c *Client) GetIMAccount(mobile, password string) (IMAccount, error) {
	return c.GetIMAccountContext(context.Background(), mobile, password)
}

// GetIMAccountContext is GetIMAccount with cancellation for the request-time
// fetch timeout. Session establishment still uses the client's login timeout.
func (c *Client) GetIMAccountContext(ctx context.Context, mobile, password string) (IMAccount, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return IMAccount{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imUserInfoURL, nil)
	if err != nil {
		return IMAccount{}, err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return IMAccount{}, fmt.Errorf("userInfo request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return IMAccount{}, fmt.Errorf("userInfo returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return IMAccount{}, fmt.Errorf("userInfo read failed: %w", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return IMAccount{}, fmt.Errorf("userInfo decode failed: %w", err)
	}
	msg, _ := payload["msg"].(map[string]interface{})
	accountInfo, _ := msg["accountInfo"].(map[string]interface{})
	im, _ := accountInfo["imAccount"].(map[string]interface{})
	if im == nil {
		return IMAccount{}, ErrIMAccountMissing
	}
	imPassword := strings.TrimSpace(strVal(im["password"]))
	if imPassword == "" {
		return IMAccount{}, ErrIMAccountMissing
	}
	uid := int64FromAny(msg["uid"])
	if uid == 0 {
		uid = s.UID
	}
	return IMAccount{
		UID:      uid,
		Username: strings.TrimSpace(strVal(im["username"])),
		Password: imPassword,
	}, nil
}
