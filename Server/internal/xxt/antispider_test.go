package xxt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSubmitLearningCaptchaRequiresSuccessfulSubmissionAndProbe(t *testing.T) {
	for _, tc := range []struct {
		name           string
		submitStatus   int
		probeStatus    int
		wantError      bool
		wantNotBlocked bool
		wantProbes     int
	}{
		{name: "verified", submitStatus: http.StatusOK, probeStatus: http.StatusOK, wantProbes: 1},
		{name: "submit unavailable", submitStatus: http.StatusServiceUnavailable, wantError: true},
		{name: "submit endpoint not blocked", submitStatus: http.StatusNotFound, wantError: true, wantNotBlocked: true},
		{name: "probe unavailable", submitStatus: http.StatusOK, probeStatus: http.StatusServiceUnavailable, wantError: true, wantProbes: 1},
		{name: "probe server error", submitStatus: http.StatusOK, probeStatus: http.StatusInternalServerError, wantError: true, wantProbes: 1},
		{name: "probe not found", submitStatus: http.StatusOK, probeStatus: http.StatusNotFound, wantError: true, wantProbes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submitBody := strings.NewReader("submission response")
			submitClosed := false
			probeAfterClose := false
			probes := 0
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				cookie, err := req.Cookie("learning_session")
				if err != nil || cookie.Value != "existing" {
					return nil, errors.New("request lost the existing learning session")
				}
				switch req.URL.Path {
				case "/html/processVerify.ac":
					resp := learningTestResponse(req, tc.submitStatus, "")
					resp.Body = &learningObservedBody{
						ReadCloser: io.NopCloser(submitBody),
						onClose:    func() { submitClosed = true },
					}
					resp.Header.Set("Set-Cookie", "verification=accepted; Domain=.chaoxing.com; Path=/; Secure")
					return resp, nil
				case "/visit/stucoursemiddle":
					probes++
					probeAfterClose = submitClosed && submitBody.Len() == 0
					cookie, err := req.Cookie("verification")
					if err != nil || cookie.Value != "accepted" {
						return nil, errors.New("probe did not reuse the submission cookie jar")
					}
					return learningTestResponse(req, tc.probeStatus, "<html>course</html>"), nil
				default:
					return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
				}
			})
			err := c.SubmitLearningCaptcha(learningTestMobile, learningTestPassword, "aB12")
			if (err != nil) != tc.wantError || errors.Is(err, ErrNotBlocked) != tc.wantNotBlocked {
				t.Fatalf("captcha submission error = %v, wantError %v, wantNotBlocked %v", err, tc.wantError, tc.wantNotBlocked)
			}
			if probes != tc.wantProbes {
				t.Fatalf("probe requests = %d, want %d", probes, tc.wantProbes)
			}
			if !submitClosed || submitBody.Len() != 0 {
				t.Fatal("submission response was not drained and closed")
			}
			if probes != 0 && !probeAfterClose {
				t.Fatal("probe started before the submission response was drained and closed")
			}
		})
	}
}

func TestSubmitLearningCaptchaRejectsRemainingChallenge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       string
		wantProbes int
		status     int
	}{
		{"submission redirected", "/html/processVerify.ac", 0, http.StatusAccepted},
		{"probe redirected", "/visit/stucoursemiddle", 1, http.StatusAccepted},
		{"challenge page not found", "/html/processVerify.ac", 0, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probes := 0
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/visit/stucoursemiddle" {
					probes++
				}
				if req.URL.Path == tc.path {
					return learningTestCaptchaRedirect(req), nil
				}
				switch req.URL.Path {
				case "/antispiderShowVerify.ac":
					return learningTestResponse(req, tc.status, "captcha"), nil
				case "/html/processVerify.ac", "/visit/stucoursemiddle":
					return learningTestResponse(req, http.StatusOK, "<html></html>"), nil
				default:
					return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
				}
			})
			err := c.SubmitLearningCaptcha(learningTestMobile, learningTestPassword, "aB12")
			if err == nil || errors.Is(err, ErrNotBlocked) {
				t.Fatalf("remaining captcha was treated as verified: %v", err)
			}
			if probes != tc.wantProbes {
				t.Fatalf("probe requests = %d, want %d", probes, tc.wantProbes)
			}
		})
	}
}

func TestGetLearningCaptchaImageAndNotBlocked(t *testing.T) {
	const imageBytes = "\x89PNG\r\n"
	for _, tc := range []struct {
		name           string
		status         int
		contentType    string
		wantError      bool
		wantNotBlocked bool
	}{
		{name: "image", status: http.StatusOK, contentType: "image/png"},
		{name: "not blocked", status: http.StatusNotFound, wantError: true, wantNotBlocked: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, contentType: "image/png", wantError: true},
		{name: "not an image", status: http.StatusOK, contentType: "text/html", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/processVerifyPng.ac" {
					return nil, fmt.Errorf("unexpected image request: %s", req.URL)
				}
				cookie, err := req.Cookie("learning_session")
				if err != nil || cookie.Value != "existing" {
					return nil, errors.New("image request lost the existing learning session")
				}
				resp := learningTestResponse(req, tc.status, imageBytes)
				resp.Header.Set("Content-Type", tc.contentType)
				return resp, nil
			})
			image, err := c.GetLearningCaptcha(learningTestMobile, learningTestPassword)
			if (err != nil) != tc.wantError || errors.Is(err, ErrNotBlocked) != tc.wantNotBlocked {
				t.Fatalf("captcha image error = %v, wantError %v, wantNotBlocked %v", err, tc.wantError, tc.wantNotBlocked)
			}
			if tc.wantError {
				if image != "" {
					t.Fatalf("failed image request returned usable data: %q", image)
				}
			} else if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(imageBytes)); image != want {
				t.Fatalf("captcha image = %q, want %q", image, want)
			}
		})
	}
}
