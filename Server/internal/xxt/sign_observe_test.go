package xxt

import (
	"net/http"
	"strings"
	"testing"
)

func TestSignRecordsAttemptAndUpstreamStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantKind   SignResultKind
		wantStatus int
	}{
		{name: "success", status: http.StatusOK, body: "success", wantKind: SignResultSuccess, wantStatus: 200},
		{name: "forbidden", status: http.StatusForbidden, body: "", wantKind: SignResultSessionExpired, wantStatus: 403},
		{name: "upstream 500", status: http.StatusInternalServerError, body: "oops", wantKind: SignResultUpstreamError, wantStatus: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
				if !strings.Contains(req.URL.Path, "/pptSign/stuSignajax") {
					t.Fatalf("unexpected request %s", req.URL.Path)
				}
				return learningTestResponse(req, tc.status, tc.body), nil
			})
			client := newLearningTestClient(t, transport)
			result, err := client.Sign(learningTestMobile, learningTestPassword, FixedParams{
				ActiveID: 1, UID: 2, CourseID: 3, ClassID: 4,
			}, SignNormal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Kind != tc.wantKind || result.Attempts != 1 || result.HTTPStatus != tc.wantStatus {
				t.Fatalf("result = %+v, want kind=%s attempts=1 status=%d", result, tc.wantKind, tc.wantStatus)
			}
		})
	}
}
