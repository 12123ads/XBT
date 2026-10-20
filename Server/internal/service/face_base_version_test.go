package service

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/facephoto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

func captureFaceLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

func seedOfficialFacePhoto(t *testing.T, database *gorm.DB, uid int64, data []byte, baseVersion int) model.FacePhoto {
	t.Helper()
	photo := model.FacePhoto{UserUID: uid, Data: data, Source: "official", BaseVersion: baseVersion}
	if err := database.Create(&photo).Error; err != nil {
		t.Fatal(err)
	}
	return photo
}

func clearSignRecord(t *testing.T, database *gorm.DB, uid, activityID int64) {
	t.Helper()
	if err := database.Where("user_uid = ? AND activity_id = ?", uid, activityID).Delete(&model.SignRecord{}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestFacePhotoBaseVersionMigrationDefaultsAndImport(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	if !database.Migrator().HasColumn(&model.FacePhoto{}, "base_version") {
		t.Fatal("base_version column was not created by the migration")
	}

	// A legacy row written without the column keeps the migration default.
	if err := database.Exec(`INSERT INTO face_photos (user_uid, data, source) VALUES (2, '\x00'::bytea, 'official')`).Error; err != nil {
		t.Fatal(err)
	}
	var legacy model.FacePhoto
	if err := database.Where("user_uid = ? AND source = ?", 2, "official").Take(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if legacy.BaseVersion != facephoto.BaseVersionLegacy {
		t.Fatalf("legacy base_version = %d, want 1", legacy.BaseVersion)
	}

	// A create that leaves BaseVersion at its zero value also gets version 1.
	zero := model.FacePhoto{UserUID: 3, Data: []byte("zero-value"), Source: "upload"}
	if err := database.Create(&zero).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("id = ?", zero.ID).Take(&zero).Error; err != nil {
		t.Fatal(err)
	}
	if zero.BaseVersion != facephoto.BaseVersionLegacy {
		t.Fatalf("zero-value base_version = %d, want 1", zero.BaseVersion)
	}

	upstream := &fakeProfileFaceClient{data: jpegFixture(t, 80, 100)}
	svc := NewFacePhotoService(database, cc, upstream)
	imported, err := svc.ImportOfficial(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if imported.BaseVersion != facephoto.BaseVersionOriginal {
		t.Fatalf("imported base_version = %d, want 2", imported.BaseVersion)
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", imported.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.BaseVersion != facephoto.BaseVersionOriginal {
		t.Fatalf("stored imported base_version = %d, want 2", stored.BaseVersion)
	}
	if uploaded, err := svc.Add(1, 2, []byte("upload"), "upload"); err != nil {
		t.Fatal(err)
	} else if uploaded.BaseVersion != facephoto.BaseVersionLegacy {
		t.Fatalf("uploaded base_version = %d, want 1", uploaded.BaseVersion)
	}
}

func TestSignRefreshesLegacyOfficialBase(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	legacyBytes := jpegFixture(t, 120, 160)
	newOriginal := jpegFixture(t, 240, 320)
	photo := seedOfficialFacePhoto(t, database, 2, legacyBytes, facephoto.BaseVersionLegacy)

	upstreamFace := &fakeProfileFaceClient{data: newOriginal}
	photoSvc := NewFacePhotoService(database, cc, upstreamFace)

	var uploads [][]byte
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploads = append(uploads, append([]byte(nil), data...))
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
	logs := captureFaceLog(t)
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetFacePhotoService(photoSvc)
	signSvc.SetRand(fixedPhotoRand{})

	result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if upstreamFace.calls != 1 {
		t.Fatalf("official re-download calls = %d, want 1", upstreamFace.calls)
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", photo.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.BaseVersion != facephoto.BaseVersionOriginal {
		t.Fatalf("base_version = %d, want 2", stored.BaseVersion)
	}
	if !bytes.Equal(stored.Data, newOriginal) {
		t.Fatal("stored base is not the refreshed official original")
	}
	if len(uploads) != 1 || bytes.Equal(uploads[0], newOriginal) || bytes.Equal(uploads[0], legacyBytes) {
		t.Fatalf("sign uploaded %d transform(s); want one transform of the refreshed base", len(uploads))
	}
	logged := logs.String()
	if !strings.Contains(logged, "face_base_refresh") || !strings.Contains(logged, "base_version=1->2") || !strings.Contains(logged, "result=ok") {
		t.Fatalf("missing refresh log: %s", logged)
	}
}

func TestLegacyBaseRefreshFailureRespectsCooldown(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	legacyBytes := jpegFixture(t, 120, 160)
	photo := seedOfficialFacePhoto(t, database, 2, legacyBytes, facephoto.BaseVersionLegacy)

	upstreamFace := &fakeProfileFaceClient{err: errors.New("official photo unavailable")}
	photoSvc := NewFacePhotoService(database, cc, upstreamFace)
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
	logs := captureFaceLog(t)
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetFacePhotoService(photoSvc)
	signSvc.SetRand(fixedPhotoRand{})

	sign := func() {
		t.Helper()
		result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
			ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
			Special: map[string]interface{}{"if_face": true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Success {
			t.Fatalf("fallback sign failed: %+v", result)
		}
		clearSignRecord(t, database, 2, 100)
	}
	sign()
	sign()

	if upstreamFace.calls != 1 {
		t.Fatalf("official re-download calls = %d, want one attempt inside the cooldown", upstreamFace.calls)
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", photo.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.BaseVersion != facephoto.BaseVersionLegacy || !bytes.Equal(stored.Data, legacyBytes) {
		t.Fatalf("failed refresh changed the base: version=%d", stored.BaseVersion)
	}
	logged := logs.String()
	if strings.Count(logged, "face_base_refresh") != 1 || !strings.Contains(logged, "result=failed") {
		t.Fatalf("refresh log = %s", logged)
	}
}

func TestLegacyUploadBaseIsNotRefreshed(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	base := jpegFixture(t, 120, 160)
	seedFacePhoto(t, database, 2, base)

	upstreamFace := &fakeProfileFaceClient{data: jpegFixture(t, 240, 320)}
	photoSvc := NewFacePhotoService(database, cc, upstreamFace)
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetFacePhotoService(photoSvc)
	signSvc.SetRand(fixedPhotoRand{})

	result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if upstreamFace.calls != 0 {
		t.Fatalf("upload base triggered an official refresh: calls=%d", upstreamFace.calls)
	}
}

func TestFaceRejectionRetriesOnceWithMildTransform(t *testing.T) {
	for _, code := range []string{"CFR002", "CFR004"} {
		t.Run(code, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			base := jpegFixture(t, 120, 160)
			seedFacePhoto(t, database, 2, base)

			var uploads [][]byte
			checks := 0
			upstream := &signAuthorizationUpstream{
				sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
				uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
					uploads = append(uploads, append([]byte(nil), data...))
					return "face-object-" + code, nil
				},
				checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
					checks++
					if checks == 1 {
						return xxt.FaceCheckResult{}, &xxt.FaceRejectedError{Code: code, Message: "no match"}
					}
					return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
				},
			}
			logs := captureFaceLog(t)
			signSvc := NewSignService(database, upstream, cc, nil)
			signSvc.SetRand(fixedPhotoRand{})

			result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
				ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
				Special: map[string]interface{}{"if_face": true},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success {
				t.Fatalf("result = %+v", result)
			}
			if len(uploads) != 2 || checks != 2 {
				t.Fatalf("uploads=%d checks=%d, want one retry", len(uploads), checks)
			}
			wantDefault, err := facephoto.Transform(base, facephoto.Options{Rand: fixedPhotoRand{}})
			if err != nil {
				t.Fatal(err)
			}
			wantMild, err := facephoto.Transform(base, facephoto.Options{Rand: fixedPhotoRand{}, Mild: true})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(uploads[0], wantDefault.JPEG) {
				t.Fatal("first upload was not the default transform")
			}
			if !bytes.Equal(uploads[1], wantMild.JPEG) {
				t.Fatal("retry upload was not the mild transform")
			}
			var stored model.FacePhoto
			if err := database.Where("user_uid = ?", 2).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if stored.UseCount != 1 || stored.IsFailureBefore {
				t.Fatalf("stored = %+v, want one counted success", stored)
			}
			logged := logs.String()
			if !strings.Contains(logged, "face_check_retry=mild") || !strings.Contains(logged, "code="+code) || !strings.Contains(logged, "result=ok") {
				t.Fatalf("missing retry log: %s", logged)
			}
		})
	}
}

func TestFaceRejectionMildRetryFailureMarksOnce(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	base := jpegFixture(t, 120, 160)
	seedFacePhoto(t, database, 2, base)

	uploadCount, checks, signs := 0, 0, 0
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			signs++
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			uploadCount++
			return "face-object", nil
		},
		checkFace: func(string, string, int64, string, string) (xxt.FaceCheckResult, error) {
			checks++
			return xxt.FaceCheckResult{}, &xxt.FaceRejectedError{Code: "CFR004", Message: "no match"}
		},
	}
	logs := captureFaceLog(t)
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetRand(fixedPhotoRand{})

	result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ErrorCode != SignErrorFaceRequired {
		t.Fatalf("result = %+v, want %q", result, SignErrorFaceRequired)
	}
	if uploadCount != 2 || checks != 2 || signs != 0 {
		t.Fatalf("uploads=%d checks=%d signs=%d, want exactly one retry and no submit", uploadCount, checks, signs)
	}
	var stored model.FacePhoto
	if err := database.Where("user_uid = ?", 2).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UseCount != 1 || !stored.IsFailureBefore {
		t.Fatalf("stored = %+v, want one counted failure", stored)
	}
	logged := logs.String()
	if strings.Count(logged, "face_check_retry=mild") != 1 || !strings.Contains(logged, "result=CFR004") {
		t.Fatalf("retry log = %s", logged)
	}
}

func TestFaceRejectionOtherCodeDoesNotRetry(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, jpegFixture(t, 120, 160))

	uploadCount, checks := 0, 0
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			t.Fatal("sign submitted after an unrecoverable face rejection")
			return xxt.SignResult{}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			uploadCount++
			return "face-object", nil
		},
		checkFace: func(string, string, int64, string, string) (xxt.FaceCheckResult, error) {
			checks++
			return xxt.FaceCheckResult{}, &xxt.FaceRejectedError{Code: "CFR001", Message: "no face"}
		},
	}
	logs := captureFaceLog(t)
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetRand(fixedPhotoRand{})

	result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ErrorCode != SignErrorFaceRequired {
		t.Fatalf("result = %+v", result)
	}
	if uploadCount != 1 || checks != 1 {
		t.Fatalf("uploads=%d checks=%d, want no retry for CFR001", uploadCount, checks)
	}
	if strings.Contains(logs.String(), "face_check_retry=mild") {
		t.Fatal("CFR001 triggered a mild retry")
	}
}

func TestFaceSuccessDoesNotRetry(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, jpegFixture(t, 120, 160))

	uploadCount, checks := 0, 0
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			uploadCount++
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			checks++
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
	logs := captureFaceLog(t)
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetRand(fixedPhotoRand{})

	result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if uploadCount != 1 || checks != 1 {
		t.Fatalf("uploads=%d checks=%d, want a single request on success", uploadCount, checks)
	}
	if strings.Contains(logs.String(), "face_check_retry=mild") {
		t.Fatal("successful path logged a retry")
	}
}
