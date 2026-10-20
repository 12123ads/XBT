package xxt

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCheckFaceResultRejectsNon2xxBeforeParsing(t *testing.T) {
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/pptSign/check-face-result" {
			return nil, fmt.Errorf("unexpected request: %s", req.URL)
		}
		return learningTestResponse(req, http.StatusBadGateway, `{"enc":"must-not-be-used"}`), nil
	})
	client := newLearningTestClient(t, transport)

	_, err := client.CheckFaceResult(learningTestMobile, learningTestPassword, 100, "face-object", "")
	if err == nil {
		t.Fatal("non-2xx check-face-result must fail")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error %q does not carry the HTTP status", err)
	}
}

func TestCheckFaceResultWithoutEncReturnsReadableError(t *testing.T) {
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		return learningTestResponse(req, http.StatusOK, `{"code":"500","msg":"人脸比对失败"}`), nil
	})
	client := newLearningTestClient(t, transport)

	_, err := client.CheckFaceResult(learningTestMobile, learningTestPassword, 100, "face-object", "")
	if err == nil {
		t.Fatal("missing enc must fail")
	}
	if !strings.Contains(err.Error(), "人脸识别失败") || !strings.Contains(err.Error(), "人脸比对失败") {
		t.Fatalf("error %q is not readable", err)
	}
}

func TestCheckFaceResultCarriesUpstreamCodeAndResponse(t *testing.T) {
	const body = `{"code":"CFR004","msg":"人脸比对失败","detail":"photo mismatch"}`
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		return learningTestResponse(req, http.StatusOK, body), nil
	})
	client := newLearningTestClient(t, transport)

	_, err := client.CheckFaceResult(learningTestMobile, learningTestPassword, 100, "face-object", "")
	if err == nil {
		t.Fatal("missing enc must fail")
	}
	if !errors.Is(err, ErrFaceRejected) {
		t.Fatalf("error %v is not ErrFaceRejected", err)
	}
	var rejected *FaceRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("error %T does not expose upstream rejection facts", err)
	}
	if rejected.Code != "CFR004" {
		t.Fatalf("code = %q, want CFR004", rejected.Code)
	}
	if rejected.Response != body {
		t.Fatalf("response = %q, want the raw body", rejected.Response)
	}
	if !strings.Contains(rejected.Error(), "人脸比对失败") {
		t.Fatalf("error %q lost the upstream message", rejected.Error())
	}
}

func TestFaceUploadShapeSelectsEndpointQuery(t *testing.T) {
	for _, tc := range []struct {
		name       string
		upload     func(*Client) (string, error)
		wantFaceTm bool
	}{
		{
			name: "face_temp",
			upload: func(client *Client) (string, error) {
				return client.UploadFaceTemp(learningTestMobile, learningTestPassword, "face.jpg", "image/jpeg", []byte("jpeg"))
			},
			wantFaceTm: true,
		},
		{
			name: "generic",
			upload: func(client *Client) (string, error) {
				return client.UploadPanFile(learningTestMobile, learningTestPassword, "face.jpg", "image/jpeg", []byte("jpeg"))
			},
			wantFaceTm: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var uploadURL string
			transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/api/token/uservalid":
					return learningTestResponse(req, http.StatusOK, `{"_token":"token-value"}`), nil
				case "/upload":
					uploadURL = req.URL.String()
					return learningTestResponse(req, http.StatusOK, `{"objectId":"object-1"}`), nil
				}
				return nil, fmt.Errorf("unexpected request: %s", req.URL)
			})
			client := newLearningTestClient(t, transport)

			objectID, err := tc.upload(client)
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			if objectID != "object-1" {
				t.Fatalf("objectId = %q", objectID)
			}
			if uploadURL == "" {
				t.Fatal("upload endpoint was not called")
			}
			hasType := strings.Contains(uploadURL, "uploadtype=face_temp")
			if hasType != tc.wantFaceTm {
				t.Fatalf("upload URL %q uploadtype=face_temp presence = %v, want %v", uploadURL, hasType, tc.wantFaceTm)
			}
		})
	}
}
