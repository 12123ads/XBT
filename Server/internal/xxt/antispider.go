package xxt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
)

// ErrCaptchaRequired 表示学习通反爬验证被触发（请求被 302 到 antispiderShowVerify.ac），
// 需要用户完成图片验证码后重试。验证状态绑定在服务端会话上，不产生新 Cookie。
var ErrCaptchaRequired = errors.New("chaoxing antispider verification required")

// ErrNotBlocked 表示当前会话并未触发反爬（验证码相关端点仅对被拦会话可用，未触发时返回 404）。
var ErrNotBlocked = errors.New("antispider not triggered for this session")

const (
	antispiderVerifyPage   = "https://mooc1.chaoxing.com/antispiderShowVerify.ac"
	antispiderVerifyImage  = "https://mooc1.chaoxing.com/processVerifyPng.ac"
	antispiderVerifySubmit = "https://mooc1.chaoxing.com/html/processVerify.ac"
)

// isAntiSpiderBlocked 判断响应最终是否落在反爬验证页。
// Go 的 http.Client 会自动跟随 302，因此被拦请求的最终响应就是验证页的 202。
func isAntiSpiderBlocked(resp *http.Response) bool {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	return strings.Contains(resp.Request.URL.Path, "antispiderShowVerify")
}

// GetLearningCaptcha 拉取当前账号的反爬验证码图片，返回 data URL 供前端直接展示。
func (c *Client) GetLearningCaptcha(mobile, password string) (string, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	cli := *c.http
	cli.Jar = s.Jar

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s?t=%d", antispiderVerifyImage, rand.Int31()), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	req.Header.Set("Referer", antispiderVerifyPage)
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotBlocked
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "image") {
		return "", fmt.Errorf("获取验证码图片失败: HTTP %d", resp.StatusCode)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(body), nil
}

// SubmitLearningCaptcha 提交用户输入的验证码，并通过探测请求确认封拦是否解除。
func (c *Client) SubmitLearningCaptcha(mobile, password, code string) error {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return err
	}
	cli := *c.http
	cli.Jar = s.Jar

	req, err := http.NewRequest(http.MethodGet, antispiderVerifySubmit+"?app=0&ucode="+url.QueryEscape(code), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	req.Header.Set("Referer", antispiderVerifyPage)
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if isAntiSpiderBlocked(resp) {
		return errors.New("验证码不正确或已过期，请重试")
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotBlocked
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("提交验证码失败: HTTP %d", resp.StatusCode)
	}
	if readErr != nil {
		return readErr
	}

	// 提交响应已关闭；探测必须成功且未跳转到验证页，才能确认封拦解除。
	canaryReq, err := http.NewRequest(http.MethodGet, "https://mooc1.chaoxing.com/visit/stucoursemiddle?ismooc2=1", nil)
	if err != nil {
		return err
	}
	canaryReq.Header.Set("User-Agent", c.mobileUA)
	canary, err := cli.Do(canaryReq)
	if err != nil {
		return err
	}
	defer canary.Body.Close()
	if isAntiSpiderBlocked(canary) {
		return errors.New("验证码不正确或已过期，请重试")
	}
	if canary.StatusCode < http.StatusOK || canary.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("验证状态探测失败: HTTP %d", canary.StatusCode)
	}
	_, err = io.Copy(io.Discard, canary.Body)
	return err
}
