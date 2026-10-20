package xxt

import (
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
