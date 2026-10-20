package xxt

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

func facePhotoTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client := New("1234567890123456", "face-photo-test", "", false, 5)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.sessions["13800000101"] = &Session{Mobile: "13800000101", Password: "password", UID: 101, Jar: jar, LastLoginAt: time.Now()}
	client.http.Transport = transport
	return client
}

func TestFetchProfileFaceReturnsOriginalBytes(t *testing.T) {
	client := facePhotoTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v2/apis/sign/collectionfilephotoEnc":
			if req.URL.Query().Get("DB_STRATEGY") != "DEFAULT" {
				t.Fatalf("metadata query = %s", req.URL.RawQuery)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"oldObjectId":"abc123"}}`)), Request: req}, nil
		case "/star4/abc123/origin.jpg":
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("original-bytes")), Request: req}, nil
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return nil, nil
	}))
	raw, err := client.FetchProfileFace("13800000101", "password")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "original-bytes" {
		t.Fatalf("raw = %q", raw)
	}
}

func TestProfileFaceObjectIDWithoutPhotoIsReadable(t *testing.T) {
	client := facePhotoTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"oldObjectId":""}}`)), Request: req}, nil
	}))
	_, err := client.ProfileFaceObjectID("13800000101", "password")
	if !errors.Is(err, ErrNoProfileFacePhoto) {
		t.Fatalf("err = %v, want ErrNoProfileFacePhoto", err)
	}
}

func TestProfileFaceObjectIDRejectsUnsafeID(t *testing.T) {
	client := facePhotoTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"oldObjectId":"../../etc/passwd"}}`)), Request: req}, nil
	}))
	_, err := client.ProfileFaceObjectID("13800000101", "password")
	if err == nil || errors.Is(err, ErrNoProfileFacePhoto) {
		t.Fatalf("err = %v, want invalid objectId error", err)
	}
}

func TestProfileFaceObjectIDRejectsHTTPFailure(t *testing.T) {
	client := facePhotoTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("login required")), Request: req}, nil
	}))
	if _, err := client.ProfileFaceObjectID("13800000101", "password"); err == nil {
		t.Fatal("non-2xx metadata response became success")
	}
}

func TestDownloadProfileFaceMissingIsReadable(t *testing.T) {
	client := facePhotoTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); !strings.HasSuffix(got, "/star4/face-id/origin.jpg") {
			t.Fatalf("download URL = %s", got)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}))
	if _, err := client.DownloadProfileFace("face-id"); !errors.Is(err, ErrNoProfileFacePhoto) {
		t.Fatalf("err = %v, want ErrNoProfileFacePhoto", err)
	}
}

func TestDownloadProfileFaceEscapesObjectID(t *testing.T) {
	client := facePhotoTestClient(t, activityScopeTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "..") {
			t.Fatalf("path traversal reached the request: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("bytes")), Request: req}, nil
	}))
	// A slash is rejected before any request is made.
	if _, err := client.DownloadProfileFace("a/b"); err == nil {
		t.Fatal("objectId with slash was accepted")
	}
}
