package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	mathrand "math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"xbt2/server/internal/xxt"
)

func TestFaceCheckFailureLogHasAttributionFieldsWithoutSecrets(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	base := jpegFixture(t, 320, 240)
	photo := seedFacePhoto(t, database, 2, base)
	const clientID = "client-id-must-not-be-logged"
	responseBody := `{"code":"CFR004","msg":"人脸比对失败","padding":"` + strings.Repeat("x", 260) + `"}`
	upstream := &signAuthorizationUpstream{
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object-1", nil
		},
		checkFace: func(string, string, int64, string, string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{}, &xxt.FaceRejectedError{
				Code:     "CFR004",
				Message:  "人脸比对失败",
				Response: responseBody,
			}
		},
		fetchClientID: func(_ context.Context, _ string, _ string, _ xxt.DeviceInfo) (string, error) {
			return clientID, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	svc.SetRand(mathrand.New(mathrand.NewPCG(3, 9)))

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	result, err := svc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatalf("ExecuteOne error = %v", err)
	}
	if result.ErrorCode != SignErrorFaceRequired {
		t.Fatalf("error code = %q, want %q", result.ErrorCode, SignErrorFaceRequired)
	}

	line := logLineContaining(t, logs.String(), "face_check_failed")
	baseHash := sha256.Sum256(base)
	for _, want := range []string{
		"activity_id=100",
		"uid=2",
		"photo_id=" + strconv.Itoa(int(photo.ID)),
		"photo_source=upload",
		"base_sha256=" + hex.EncodeToString(baseHash[:])[:12],
		"transform={crop_w:",
		"object_id=face-object-1",
		"has_cxcid=true",
		"code=CFR004",
		"upload_mode=face_temp",
		`response="`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("face_check_failed line missing %q:\n%s", want, line)
		}
	}
	for _, secret := range []string{clientID, "password"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("face logs leaked %q:\n%s", secret, logs.String())
		}
	}

	response := logFieldValue(t, line, "response=")
	decoded, err := strconv.Unquote(response)
	if err != nil {
		t.Fatalf("response field %q is not a quoted string: %v", response, err)
	}
	if utf8.RuneCountInString(decoded) != 200 {
		t.Fatalf("response length = %d, want exactly 200 runes", utf8.RuneCountInString(decoded))
	}
	if !strings.HasSuffix(decoded, "...") {
		t.Fatalf("truncated response %q lost the ellipsis", decoded)
	}
}

func TestFaceTransformMildLogAndPixels(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	base := jpegFixture(t, 320, 240)
	seedFacePhoto(t, database, 2, base)
	var uploaded []byte
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploaded = append([]byte(nil), data...)
			return "face-object-1", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	svc.SetRand(mathrand.New(mathrand.NewPCG(3, 9)))
	svc.SetFaceDiagnostics(true, faceUploadModeFaceTemp)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	runFaceSign(t, svc, database)

	line := logLineContaining(t, logs.String(), "face photo transform:")
	pattern := regexp.MustCompile(`mild=true crop_w=([0-9.]+) crop_h=([0-9.]+) angle=(-?[0-9.]+) quality=(\d+)`)
	match := pattern.FindStringSubmatch(line)
	if match == nil {
		t.Fatalf("mild transform log fields missing:\n%s", line)
	}
	cropW, _ := strconv.ParseFloat(match[1], 64)
	cropH, _ := strconv.ParseFloat(match[2], 64)
	angle, _ := strconv.ParseFloat(match[3], 64)
	quality, _ := strconv.Atoi(match[4])
	if cropW < 0.99 || cropW > 1.0 || cropH < 0.99 || cropH > 1.0 {
		t.Fatalf("mild crop ratios = %v/%v, want [0.99,1.0]", cropW, cropH)
	}
	if angle != 0 {
		t.Fatalf("mild angle = %v, want 0", angle)
	}
	if quality != 92 {
		t.Fatalf("mild quality = %d, want 92", quality)
	}
	if bytes.Equal(uploaded, base) {
		t.Fatal("mild diagnostics skipped the transform")
	}
}

func TestFaceUploadModeSelectsEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diagnostic string
		wantFace   bool
		wantPan    bool
	}{
		{name: "default keeps face_temp", wantFace: true},
		{name: "generic uses pan upload", diagnostic: faceUploadModeGeneric, wantPan: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			seedFacePhoto(t, database, 2, jpegFixture(t, 320, 240))
			faceUploads, panUploads := 0, 0
			upstream := &signAuthorizationUpstream{
				sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
				uploadFace: func(string, string, string, string, []byte) (string, error) {
					faceUploads++
					return "face-object", nil
				},
				uploadPan: func(string, string, string, string, []byte) (string, error) {
					panUploads++
					return "pan-object", nil
				},
				checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
					return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			svc.SetRand(mathrand.New(mathrand.NewPCG(5, 7)))
			svc.SetFaceDiagnostics(false, tc.diagnostic)

			runFaceSign(t, svc, database)

			if (faceUploads > 0) != tc.wantFace || (panUploads > 0) != tc.wantPan {
				t.Fatalf("uploads face=%d pan=%d, want face=%v pan=%v", faceUploads, panUploads, tc.wantFace, tc.wantPan)
			}
		})
	}
}

func logLineContaining(t *testing.T, logs, marker string) string {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("log does not contain %q:\n%s", marker, logs)
	return ""
}

func logFieldValue(t *testing.T, line, field string) string {
	t.Helper()
	index := strings.Index(line, field)
	if index < 0 {
		t.Fatalf("line does not contain field %q: %s", field, line)
	}
	return strings.TrimSpace(line[index+len(field):])
}
