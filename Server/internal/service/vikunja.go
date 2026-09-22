package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"xbt2/server/internal/config"
)

const vikunjaTimeout = 15 * time.Second

var (
	ErrVikunjaNotConfigured   = errors.New("服务端未配置 Vikunja 地址")
	ErrVikunjaRebindRequired  = errors.New("Vikunja 实例已变化或凭据不可用，请重新填写 Token")
	ErrVikunjaSettingsChanged = errors.New("Vikunja 设置已变化，请重新打开设置")
	ErrVikunjaSyncDisabled    = errors.New("Vikunja 同步未启用")
	vikunjaTransport          = func() *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		return transport
	}()
)

type VikunjaHTTPError struct {
	StatusCode int
	Method     string
	Path       string
}

func (e *VikunjaHTTPError) Error() string {
	return fmt.Sprintf("vikunja %s %s: status %d", e.Method, e.Path, e.StatusCode)
}

// VikunjaRequestError 保留取消等错误链，但不暴露 URL、Token 或响应正文。
type VikunjaRequestError struct {
	method string
	path   string
	cause  error
}

func (e *VikunjaRequestError) Error() string {
	return fmt.Sprintf("vikunja %s %s: request failed", e.method, e.path)
}

func (e *VikunjaRequestError) Unwrap() error { return e.cause }

// isVikunjaNotFound 判断错误是否为远端任务不存在（已被删除），据此重建映射。
func isVikunjaNotFound(err error) bool {
	var httpErr *VikunjaHTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

type VikunjaClient struct {
	baseURL       string
	token         string
	client        *http.Client
	beforeRequest func(context.Context) error
}

func NewVikunjaClient(baseURL, token string) (*VikunjaClient, error) {
	baseURL, err := config.NormalizeVikunjaBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if baseURL == "" {
		return nil, ErrVikunjaNotConfigured
	}
	return &VikunjaClient{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		client: &http.Client{
			Timeout:   vikunjaTimeout,
			Transport: vikunjaTransport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type VikunjaProject struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type VikunjaLabel struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type VikunjaTask struct {
	ID          int64          `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	DueDate     *time.Time     `json:"due_date,omitempty"`
	Done        bool           `json:"done"`
	ProjectID   int64          `json:"project_id"`
	Labels      []VikunjaLabel `json:"labels"`
}

func (c *VikunjaClient) do(ctx context.Context, method, path string, payload interface{}, out interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.token == "" {
		return ErrVikunjaRebindRequired
	}
	if c.beforeRequest != nil {
		if err := c.beforeRequest(ctx); err != nil {
			return err
		}
	}
	apiPath, _, _ := strings.Cut(path, "?")
	requestError := func(err error) error {
		return &VikunjaRequestError{method: method, path: apiPath, cause: err}
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return requestError(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v1"+path, body)
	if err != nil {
		return requestError(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return requestError(err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &VikunjaHTTPError{StatusCode: resp.StatusCode, Method: method, Path: apiPath}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return requestError(err)
		}
	}
	return nil
}

func (c *VikunjaClient) ListProjects(ctx context.Context) ([]VikunjaProject, error) {
	projects := make([]VikunjaProject, 0)
	// 拉两页足够覆盖个人项目场景，避免分页循环拖慢设置弹窗。
	for page := 1; page <= 2; page++ {
		var batch []VikunjaProject
		path := fmt.Sprintf("/projects?page=%d&per_page=50", page)
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		projects = append(projects, batch...)
		if len(batch) < 50 {
			break
		}
	}
	return projects, nil
}

func (c *VikunjaClient) GetProject(ctx context.Context, projectID int64) (*VikunjaProject, error) {
	path := fmt.Sprintf("/projects/%d", projectID)
	var project VikunjaProject
	if err := c.do(ctx, http.MethodGet, path, nil, &project); err != nil {
		return nil, err
	}
	if projectID <= 0 || project.ID != projectID {
		return nil, &VikunjaRequestError{method: http.MethodGet, path: path, cause: errors.New("invalid project identity")}
	}
	return &project, nil
}

func (c *VikunjaClient) CreateLabel(ctx context.Context, title string) (*VikunjaLabel, error) {
	var label VikunjaLabel
	if err := c.do(ctx, http.MethodPut, "/labels", map[string]string{"title": title}, &label); err != nil {
		return nil, err
	}
	if label.ID <= 0 {
		return nil, &VikunjaRequestError{method: http.MethodPut, path: "/labels", cause: errors.New("invalid label identity")}
	}
	return &label, nil
}

func (c *VikunjaClient) FindLabelByTitle(ctx context.Context, title string) (*VikunjaLabel, error) {
	labels := make([]VikunjaLabel, 0)
	path := "/labels?s=" + url.QueryEscape(title) + "&per_page=50"
	if err := c.do(ctx, http.MethodGet, path, nil, &labels); err != nil {
		return nil, err
	}
	for i := range labels {
		if labels[i].Title == title {
			return &labels[i], nil
		}
	}
	return nil, nil
}

// EnsureLabel 返回与课程名同名的标签 ID，不存在时自动创建。
func (c *VikunjaClient) EnsureLabel(ctx context.Context, title string) (int64, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return 0, nil
	}
	existing, err := c.FindLabelByTitle(ctx, title)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return existing.ID, nil
	}
	created, err := c.CreateLabel(ctx, title)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (c *VikunjaClient) CreateTask(ctx context.Context, projectID int64, task *VikunjaTask) (*VikunjaTask, error) {
	// Vikunja v1 API 的任务创建挂在项目下：PUT /projects/{id}/tasks。
	var created VikunjaTask
	path := fmt.Sprintf("/projects/%d/tasks", projectID)
	if err := c.do(ctx, http.MethodPut, path, task, &created); err != nil {
		return nil, err
	}
	if created.ID <= 0 || created.ProjectID != projectID {
		return nil, &VikunjaRequestError{method: http.MethodPut, path: path, cause: errors.New("invalid task identity")}
	}
	return &created, nil
}

func (c *VikunjaClient) GetTask(ctx context.Context, taskID int64) (map[string]json.RawMessage, error) {
	path := fmt.Sprintf("/tasks/%d", taskID)
	var task map[string]json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, &task); err != nil {
		return nil, err
	}
	var id int64
	if taskID <= 0 || json.Unmarshal(task["id"], &id) != nil || id != taskID {
		return nil, &VikunjaRequestError{method: http.MethodGet, path: path, cause: errors.New("invalid task identity")}
	}
	return task, nil
}

func (c *VikunjaClient) UpdateTask(ctx context.Context, taskID int64, task map[string]json.RawMessage) error {
	path := fmt.Sprintf("/tasks/%d", taskID)
	return c.do(ctx, http.MethodPost, path, task, nil)
}

func (c *VikunjaClient) AttachLabel(ctx context.Context, taskID, labelID int64) error {
	path := fmt.Sprintf("/tasks/%d/labels", taskID)
	return c.do(ctx, http.MethodPut, path, map[string]int64{"label_id": labelID}, nil)
}
