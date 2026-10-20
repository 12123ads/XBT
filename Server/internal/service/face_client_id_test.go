package service

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

// runFaceSign executes one face sign and removes the resulting record so the
// next call goes through the same pre-sign path again.
func runFaceSign(t *testing.T, svc *SignService, database *gorm.DB) SignExecuteResult {
	t.Helper()
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
		Context: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("face sign did not continue: %+v", result)
	}
	if err := database.Where("user_uid = ? AND activity_id = ?", 2, 100).Delete(&model.SignRecord{}).Error; err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFaceSignBackfillsClientIDOnce(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, []byte("face-bytes"))

	fetches := 0
	var checked []string
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, _ string, clientID string) (xxt.FaceCheckResult, error) {
			checked = append(checked, clientID)
			return xxt.FaceCheckResult{CurrentFaceID: "face-object", FaceEnc: "face-enc"}, nil
		},
		fetchClientID: func(ctx context.Context, _ string, _ string, _ xxt.DeviceInfo) (string, error) {
			fetches++
			if _, ok := ctx.Deadline(); !ok {
				t.Error("clientId backfill ran without a deadline")
			}
			return "backfilled-client-id", nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	runFaceSign(t, svc, database)
	runFaceSign(t, svc, database)

	if fetches != 1 {
		t.Fatalf("clientId fetches = %d, want one for both signs", fetches)
	}
	if len(checked) != 2 || checked[0] != "backfilled-client-id" || checked[1] != checked[0] {
		t.Fatalf("face checks received clientIds %v, want the backfilled value twice", checked)
	}
	if !strings.Contains(logs.String(), `"has_cxcid":true`) {
		t.Fatalf("has_cxcid did not reflect the backfilled signature: %s", logs.String())
	}

	var user model.User
	if err := database.Where("uid = ?", 2).Take(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.DeviceInfoCipher == "" {
		t.Fatal("backfill did not persist the device profile")
	}
	stored, err := cc.Decrypt(user.ClientIDCipher)
	if err != nil || stored != "backfilled-client-id" {
		t.Fatalf("persisted clientId = %q err=%v", stored, err)
	}
}

func TestFaceSignContinuesWhenBackfillFails(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, []byte("face-bytes"))

	var checked string
	signs := 0
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			signs++
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, _ string, clientID string) (xxt.FaceCheckResult, error) {
			checked = clientID
			return xxt.FaceCheckResult{CurrentFaceID: "face-object", FaceEnc: "face-enc"}, nil
		},
		fetchClientID: func(context.Context, string, string, xxt.DeviceInfo) (string, error) {
			return "", errors.New("userInfo unavailable")
		},
	}
	svc := NewSignService(database, upstream, cc, nil)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	runFaceSign(t, svc, database)

	if signs != 1 {
		t.Fatalf("sign attempts = %d, want the sign to continue", signs)
	}
	if checked != "" {
		t.Fatalf("face check received clientId %q after a failed backfill", checked)
	}
	logged := logs.String()
	if !strings.Contains(logged, "face clientId backfill failed") || !strings.Contains(logged, "userInfo unavailable") {
		t.Fatalf("backfill failure was not logged with its reason: %s", logged)
	}
	if !strings.Contains(logged, `"has_cxcid":false`) {
		t.Fatalf("has_cxcid did not reflect the unsigned request: %s", logged)
	}
	var user model.User
	if err := database.Where("uid = ?", 2).Take(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.ClientIDCipher != "" {
		t.Fatal("failed backfill persisted a clientId")
	}
}

func TestFaceSignSkipsBackfillWithStoredClientID(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	cipher, err := cc.Encrypt("persisted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", 2).Update("client_id_cipher", cipher).Error; err != nil {
		t.Fatal(err)
	}
	seedFacePhoto(t, database, 2, []byte("face-bytes"))

	fetches := 0
	var checked string
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, _ string, clientID string) (xxt.FaceCheckResult, error) {
			checked = clientID
			return xxt.FaceCheckResult{CurrentFaceID: "face-object", FaceEnc: "face-enc"}, nil
		},
		fetchClientID: func(context.Context, string, string, xxt.DeviceInfo) (string, error) {
			fetches++
			return "must-not-be-used", nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	runFaceSign(t, svc, database)

	if fetches != 0 {
		t.Fatalf("clientId fetches = %d, want none with a stored value", fetches)
	}
	if checked != "persisted-client-id" {
		t.Fatalf("face check received clientId %q", checked)
	}
}

func TestFaceSignBackfillCooldownAfterFailure(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, []byte("face-bytes"))

	fetches := 0
	var checked []string
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, _ string, clientID string) (xxt.FaceCheckResult, error) {
			checked = append(checked, clientID)
			return xxt.FaceCheckResult{CurrentFaceID: "face-object", FaceEnc: "face-enc"}, nil
		},
		fetchClientID: func(context.Context, string, string, xxt.DeviceInfo) (string, error) {
			fetches++
			return "", errors.New("userInfo unavailable")
		},
	}
	svc := NewSignService(database, upstream, cc, nil)

	runFaceSign(t, svc, database)
	runFaceSign(t, svc, database)

	if fetches != 1 {
		t.Fatalf("clientId fetches = %d, want the failure to enter cooldown", fetches)
	}
	if len(checked) != 2 || checked[0] != "" || checked[1] != "" {
		t.Fatalf("face checks received clientIds %v, want unsigned requests", checked)
	}
}
