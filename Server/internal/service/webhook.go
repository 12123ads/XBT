package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const enterpriseWechatWebhookTimeout = 5 * time.Second

type EnterpriseWechatWebhookNotifier struct {
	url    string
	client *http.Client
}

func NewEnterpriseWechatWebhookNotifier(url string) *EnterpriseWechatWebhookNotifier {
	return &EnterpriseWechatWebhookNotifier{
		url: strings.TrimSpace(url),
		client: &http.Client{
			Timeout: enterpriseWechatWebhookTimeout,
		},
	}
}

func (n *EnterpriseWechatWebhookNotifier) Enabled() bool {
	return n != nil && strings.TrimSpace(n.url) != ""
}

func (n *EnterpriseWechatWebhookNotifier) SendMarkdownAsync(label, content string) {
	if !n.Enabled() {
		return
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), enterpriseWechatWebhookTimeout)
		defer cancel()

		if err := n.SendMarkdown(ctx, content); err != nil {
			log.Printf("%s webhook failed: %v", label, err)
		}
	}()
}

func (n *EnterpriseWechatWebhookNotifier) SendMarkdown(ctx context.Context, content string) error {
	if !n.Enabled() {
		return nil
	}
	payload := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func webhookText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return strings.ReplaceAll(value, "\n", " ")
}
