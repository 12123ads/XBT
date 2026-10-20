package im

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultTokenURL       = "https://a1-vip6.easemob.com/cx-dev/cxstudy/token"
	DefaultAPIBaseURL     = "https://a1-vip6.easemob.com/cx-dev/cxstudy"
	DefaultMessageBaseURL = "https://a1-vip6.easecdn.com/cx-dev/cxstudy"

	// SourceTaskActiveList and SourceIMGroup are the additive activity
	// provenance markers returned by /sign/activities.
	SourceTaskActiveList = "taskactivelist"
	SourceIMGroup        = "im_group"

	// Result codes are the only error detail written to logs or returned by
	// /sign/im/status. Upstream response bodies and credentials never appear.
	CodeAuthFailed     = "im_auth_failed"
	CodeNotConfigured  = "im_not_configured"
	CodeUnauthorized   = "im_unauthorized"
	CodeTokenFailed    = "im_token_failed"
	CodeGroupsFailed   = "im_groups_failed"
	CodeMessagesFailed = "im_messages_failed"
	CodeParseFailed    = "im_parse_failed"
	CodeNetwork        = "im_network"
	CodeTimeout        = "im_timeout"
	CodeDisabled       = "im_disabled"

	userAgent          = "Easemob-SDK(Android) 4.9.0.1"
	maxResponseBytes   = int64(8 << 20)
	defaultRequestTime = 15 * time.Second
)

// ErrUnauthorized matches any 401/408 response from a token-bound endpoint.
var ErrUnauthorized = errors.New("im authorization expired")

// Error carries a stable result code. It intentionally has no response body,
// URL, username, or token to make log redaction mechanical.
type Error struct {
	Code   string
	Status int
	Err    error
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s (status %d)", e.Code, e.Status)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool {
	return target == ErrUnauthorized && e.Code == CodeUnauthorized
}

// Group is one course chat group from joined_chatgroups.
type Group struct {
	ID         string
	Name       string
	CourseName string
}

// Token is the Easemob access token plus the account name used in paths.
type Token struct {
	AccessToken string
	Username    string
	UUID        string
	ExpiresAt   time.Time
}

// Client speaks the Easemob subset used by 学习通 course groups.
type Client struct {
	tokenURL       string
	apiBaseURL     string
	messageBaseURL string
	http           *http.Client
}

func New(tokenURL, apiBaseURL, messageBaseURL string) *Client {
	return &Client{
		tokenURL:       strings.TrimRight(strings.TrimSpace(tokenURL), "/"),
		apiBaseURL:     strings.TrimRight(strings.TrimSpace(apiBaseURL), "/"),
		messageBaseURL: strings.TrimRight(strings.TrimSpace(messageBaseURL), "/"),
		http:           &http.Client{Timeout: defaultRequestTime},
	}
}

// NewWithHTTPClient lets callers (and tests) supply transport settings.
func NewWithHTTPClient(tokenURL, apiBaseURL, messageBaseURL string, client *http.Client) *Client {
	c := New(tokenURL, apiBaseURL, messageBaseURL)
	if client != nil {
		c.http = client
	}
	return c
}

// Token exchanges uid + plaintext IM password for an access token.
func (c *Client) Token(ctx context.Context, uid int64, password string) (Token, error) {
	request := struct {
		GrantType string `json:"grant_type"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}{
		GrantType: "password",
		Username:  strconv.FormatInt(uid, 10),
		Password:  password,
	}
	var payload struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   flexInt `json:"expires_in"`
		User        struct {
			Username flexString `json:"username"`
			UUID     string     `json:"uuid"`
		} `json:"user"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.tokenURL, request, "", &payload, CodeTokenFailed, "application/x-www-form-urlencoded", true); err != nil {
		return Token{}, err
	}
	accessToken := strings.TrimSpace(payload.AccessToken)
	if accessToken == "" {
		return Token{}, &Error{Code: CodeParseFailed}
	}
	username := strings.TrimSpace(string(payload.User.Username))
	if username == "" {
		username = strconv.FormatInt(uid, 10)
	}
	expiresIn := int64(payload.ExpiresIn)
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return Token{
		AccessToken: accessToken,
		Username:    username,
		UUID:        strings.TrimSpace(payload.User.UUID),
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

// Groups lists joined chat groups for the token's account.
func (c *Client) Groups(ctx context.Context, token Token) ([]Group, error) {
	endpoint := c.apiBaseURL + "/users/" + url.PathEscape(token.Username) +
		"/joined_chatgroups?detail=true&version=v3&pagenum=1&pagesize=200"
	var payload struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, token.AccessToken, &payload, CodeGroupsFailed, "", false); err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(payload.Data))
	for _, raw := range payload.Data {
		var item struct {
			ID          flexString      `json:"id"`
			Name        string          `json:"name"`
			Description json.RawMessage `json:"description"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		group := Group{
			ID:   strings.TrimSpace(string(item.ID)),
			Name: strings.TrimSpace(item.Name),
		}
		if group.ID == "" {
			continue
		}
		var description struct {
			CourseInfo *struct {
				CourseName string `json:"coursename"`
			} `json:"courseInfo"`
		}
		rawDescription := bytes.TrimSpace(item.Description)
		if len(rawDescription) > 0 && !bytes.Equal(rawDescription, []byte("null")) {
			if rawDescription[0] == '"' {
				var inner string
				if err := json.Unmarshal(rawDescription, &inner); err == nil {
					rawDescription = []byte(inner)
				}
			}
			if err := json.Unmarshal(rawDescription, &description); err == nil && description.CourseInfo != nil {
				group.CourseName = strings.TrimSpace(description.CourseInfo.CourseName)
			}
		}
		if group.Name == "" {
			group.Name = group.CourseName
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// Messages fetches roaming history for one group and returns its sign cards.
func (c *Client) Messages(ctx context.Context, token Token, groupID string) ([]SignActivity, error) {
	endpoint := c.messageBaseURL + "/users/" + url.PathEscape(token.Username) + "/messageroaming"
	request := struct {
		End   string `json:"end"`
		Queue string `json:"queue"`
		Start string `json:"start"`
	}{
		End:   "-1",
		Queue: groupID + "@conference.easemob.com",
		Start: "-1",
	}
	var payload struct {
		Data struct {
			Msgs []struct {
				Msg flexString `json:"msg"`
			} `json:"msgs"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, endpoint, request, token.AccessToken, &payload, CodeMessagesFailed, "text/plain;charset=UTF-8", false); err != nil {
		return nil, err
	}
	activities := make([]SignActivity, 0, len(payload.Data.Msgs))
	for _, message := range payload.Data.Msgs {
		activity, ok := DecodeRoamingMessage(string(message.Msg))
		if ok {
			activities = append(activities, activity)
		}
	}
	return activities, nil
}

// Discover lists groups and every sign card in their roaming history.
func (c *Client) Discover(ctx context.Context, token Token) ([]Group, []SignActivity, error) {
	groups, err := c.Groups(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int64]SignActivity)
	for _, group := range groups {
		activities, err := c.Messages(ctx, token, group.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, activity := range activities {
			existing, ok := byID[activity.ActiveID]
			if !ok {
				byID[activity.ActiveID] = activity
				continue
			}
			if activity.MessageTimeMS >= existing.MessageTimeMS {
				if activity.CourseID == 0 {
					activity.CourseID = existing.CourseID
				}
				if activity.ClassID == 0 {
					activity.ClassID = existing.ClassID
				}
				if activity.CourseName == "" {
					activity.CourseName = existing.CourseName
				}
				byID[activity.ActiveID] = activity
			}
		}
	}
	activities := make([]SignActivity, 0, len(byID))
	for _, activity := range byID {
		activities = append(activities, activity)
	}
	sort.Slice(activities, func(i, j int) bool {
		if activities[i].MessageTimeMS == activities[j].MessageTimeMS {
			return activities[i].ActiveID > activities[j].ActiveID
		}
		return activities[i].MessageTimeMS > activities[j].MessageTimeMS
	})
	return groups, activities, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body any, accessToken string, out any, failureCode, contentType string, tokenRequest bool) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return &Error{Code: CodeParseFailed, Err: err}
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return &Error{Code: CodeNetwork, Err: err}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Code: CodeNetwork, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return &Error{Code: CodeNetwork, Err: err}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		switch {
		case tokenRequest && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden):
			return &Error{Code: CodeAuthFailed, Status: resp.StatusCode}
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusRequestTimeout:
			return &Error{Code: CodeUnauthorized, Status: resp.StatusCode}
		default:
			return &Error{Code: failureCode, Status: resp.StatusCode}
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Code: CodeParseFailed, Err: err}
	}
	return nil
}
