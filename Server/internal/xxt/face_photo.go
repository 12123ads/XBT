package xxt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	profileFaceMetadataURL = "https://mobilelearn.chaoxing.com/v2/apis/sign/collectionfilephotoEnc?DB_STRATEGY=DEFAULT"
	profileFaceImageURL    = "https://p.cldisk.com/star4/%s/origin.jpg"
	// maxProfileFaceBytes bounds one download. Real profile photos are far
	// smaller; the cap only stops a malicious or broken response.
	maxProfileFaceBytes = 20 << 20
)

// ErrNoProfileFacePhoto means the account has no official 学习通 face photo.
// Callers map it to a readable user-facing message and never generate a
// placeholder.
var ErrNoProfileFacePhoto = errors.New("account has no official face photo")

// ProfileFaceObjectID returns the objectId of the account's official face
// photo. The request uses that account's own session.
func (c *Client) ProfileFaceObjectID(mobile, password string) (string, error) {
	return c.ProfileFaceObjectIDContext(context.Background(), mobile, password)
}

// ProfileFaceObjectIDContext is ProfileFaceObjectID bounded by ctx. Session
// establishment still uses the client's login timeout, as with the other
// request-scoped upstream calls.
func (c *Client) ProfileFaceObjectIDContext(ctx context.Context, mobile, password string) (string, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return "", err
	}
	cli := *c.http
	cli.Jar = s.Jar
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileFaceMetadataURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return "", fmt.Errorf("collectionfilephotoEnc request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("collectionfilephotoEnc read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("collectionfilephotoEnc returned http %d: %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	var payload struct {
		Data struct {
			OldObjectID string `json:"oldObjectId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("collectionfilephotoEnc decode failed: http %d: %w", resp.StatusCode, err)
	}
	objectID := strings.TrimSpace(payload.Data.OldObjectID)
	if objectID == "" {
		return "", fmt.Errorf("%w: collectionfilephotoEnc has no oldObjectId", ErrNoProfileFacePhoto)
	}
	if err := validateFaceObjectID(objectID); err != nil {
		return "", err
	}
	return objectID, nil
}

// DownloadProfileFace fetches the original bytes for objectID. Callers must
// transform the result in memory unless they are storing it as the base image.
func (c *Client) DownloadProfileFace(objectID string) ([]byte, error) {
	return c.DownloadProfileFaceContext(context.Background(), objectID)
}

// DownloadProfileFaceContext is DownloadProfileFace bounded by ctx.
func (c *Client) DownloadProfileFaceContext(ctx context.Context, objectID string) ([]byte, error) {
	if err := validateFaceObjectID(objectID); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(profileFaceImageURL, url.PathEscape(objectID)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download official face photo failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: official face photo is missing", ErrNoProfileFacePhoto)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download official face photo returned http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProfileFaceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read official face photo failed: %w", err)
	}
	if len(body) > maxProfileFaceBytes {
		return nil, fmt.Errorf("official face photo exceeds %d bytes", maxProfileFaceBytes)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: official face photo is empty", ErrNoProfileFacePhoto)
	}
	return body, nil
}

// FetchProfileFace resolves and downloads the account's official face photo.
// The returned bytes are the untouched original.
func (c *Client) FetchProfileFace(mobile, password string) ([]byte, error) {
	return c.FetchProfileFaceContext(context.Background(), mobile, password)
}

// FetchProfileFaceContext is FetchProfileFace bounded by ctx.
func (c *Client) FetchProfileFaceContext(ctx context.Context, mobile, password string) ([]byte, error) {
	objectID, err := c.ProfileFaceObjectIDContext(ctx, mobile, password)
	if err != nil {
		return nil, err
	}
	return c.DownloadProfileFaceContext(ctx, objectID)
}

func validateFaceObjectID(objectID string) error {
	trimmed := strings.TrimSpace(objectID)
	if trimmed == "" {
		return fmt.Errorf("%w: empty objectId", ErrNoProfileFacePhoto)
	}
	if len(trimmed) > 512 || strings.ContainsAny(trimmed, `/\?#`) {
		return fmt.Errorf("official face photo objectId is invalid")
	}
	return nil
}
