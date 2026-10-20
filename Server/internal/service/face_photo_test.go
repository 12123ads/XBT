package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	mathrand "math/rand/v2"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/facephoto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type fakeProfileFaceClient struct {
	data        []byte
	err         error
	calls       int
	mobile      string
	ctxErr      error
	hasDeadline bool
}

func (f *fakeProfileFaceClient) FetchProfileFaceContext(ctx context.Context, mobile, _ string) ([]byte, error) {
	f.calls++
	f.mobile = mobile
	_, f.hasDeadline = ctx.Deadline()
	if f.ctxErr != nil {
		return nil, f.ctxErr
	}
	return f.data, f.err
}

type fixedPhotoRand struct{ value int }

func (r fixedPhotoRand) Float64() float64 { return 0.5 }
func (r fixedPhotoRand) IntN(n int) int {
	if r.value >= n {
		return n - 1
	}
	return r.value
}

func jpegFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: uint8((x + y) % 255), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newFacePhotoFixture(t *testing.T) (*gorm.DB, *CredentialCrypto) {
	t.Helper()
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("face-photo-test")
	for _, uid := range []int64{1, 2, 3} {
		seedAuthorizationUser(t, database, cc, uid)
	}
	selectAuthorizationCourse(t, database, 1, 10, 20)
	selectAuthorizationCourse(t, database, 2, 10, 20)
	// uid 3 shares no selected course with uid 1.
	selectAuthorizationCourse(t, database, 3, 30, 40)
	if err := database.Create(&model.SignActivityScope{ActivityID: 100, CourseID: 10, ClassID: 20}).Error; err != nil {
		t.Fatal(err)
	}
	return database, cc
}

func TestFacePhotoLimitAndDeletion(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	svc := NewFacePhotoService(database, cc, &fakeProfileFaceClient{})
	photo := []byte("photo-bytes")
	for i := 0; i < facephoto.MaxPhotos; i++ {
		if _, err := svc.Add(1, 1, photo, "upload"); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := svc.Add(1, 1, photo, "upload"); !errors.Is(err, ErrFacePhotoLimit) {
		t.Fatalf("sixth photo err = %v, want ErrFacePhotoLimit", err)
	}
	photos, err := svc.List(1, 1)
	if err != nil || len(photos) != facephoto.MaxPhotos {
		t.Fatalf("photos=%d err=%v", len(photos), err)
	}
	if err := svc.Delete(1, photos[0].ID); err != nil {
		t.Fatal(err)
	}
	photos, err = svc.List(1, 1)
	if err != nil || len(photos) != facephoto.MaxPhotos-1 {
		t.Fatalf("after delete photos=%d err=%v", len(photos), err)
	}
	if _, err := svc.Add(1, 1, photo, "upload"); err != nil {
		t.Fatalf("add after delete: %v", err)
	}
}

func TestFacePhotoAuthorization(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	svc := NewFacePhotoService(database, cc, &fakeProfileFaceClient{})
	photo, err := svc.Add(2, 2, []byte("target-photo"), "upload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(1, 2); err != nil {
		t.Fatalf("delegated account access denied: %v", err)
	}
	if _, err := svc.List(1, 3); !errors.Is(err, ErrFacePhotoForbidden) {
		t.Fatalf("unrelated account err = %v, want ErrFacePhotoForbidden", err)
	}
	if err := svc.Delete(1, photo.ID); err != nil {
		t.Fatalf("delegated delete: %v", err)
	}
	if _, err := svc.Add(3, 1, []byte("x"), "upload"); !errors.Is(err, ErrFacePhotoForbidden) {
		t.Fatalf("reverse delegation err = %v, want ErrFacePhotoForbidden", err)
	}
}

func TestImportOfficialPhotoStoresBaseBytes(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	original := jpegFixture(t, 240, 320)
	upstream := &fakeProfileFaceClient{data: original}
	svc := NewFacePhotoService(database, cc, upstream)

	photo, err := svc.ImportOfficial(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if upstream.calls != 1 || upstream.mobile != "13800000002" {
		t.Fatalf("upstream calls=%d mobile=%q", upstream.calls, upstream.mobile)
	}
	if photo.Source != "official" || photo.UserUID != 2 {
		t.Fatalf("photo = %+v", photo)
	}
	if !bytes.Equal(photo.Data, original) {
		t.Fatal("official import did not store the downloaded bytes unchanged")
	}
	if len(photo.Data) > facephoto.MaxBaseBytes {
		t.Fatalf("stored base bytes = %d", len(photo.Data))
	}
}

func TestImportOfficialPhotoWithoutProfilePhoto(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	svc := NewFacePhotoService(database, cc, &fakeProfileFaceClient{err: xxt.ErrNoProfileFacePhoto})
	_, err := svc.ImportOfficial(context.Background(), 1, 2)
	if !errors.Is(err, xxt.ErrNoProfileFacePhoto) {
		t.Fatalf("err = %v, want ErrNoProfileFacePhoto", err)
	}
	var count int64
	if err := database.Model(&model.FacePhoto{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("photos created on failed import: %d err=%v", count, err)
	}
}

func TestImportedOfficialBaseIsTransformedAtSignTime(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	original := jpegFixture(t, 240, 320)
	photoSvc := NewFacePhotoService(database, cc, &fakeProfileFaceClient{data: original})
	imported, err := photoSvc.ImportOfficial(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	baseHash := sha256.Sum256(imported.Data)

	var uploaded []byte
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploaded = append([]byte(nil), data...)
			return "face-temp-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
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
		t.Fatalf("result=%+v", result)
	}
	if len(uploaded) == 0 || bytes.Equal(uploaded, imported.Data) {
		t.Fatal("sign uploaded the stored base instead of a transform")
	}
	if len(uploaded) > facephoto.MaxUploadBytes {
		t.Fatalf("uploaded bytes = %d", len(uploaded))
	}
	if bytes.Contains(uploaded, []byte("Exif")) {
		t.Fatal("uploaded transform still carries EXIF")
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", imported.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UseCount != 1 || stored.ObjectID != "face-temp-object" {
		t.Fatalf("stored = %+v", stored)
	}
	if sha256.Sum256(stored.Data) != baseHash {
		t.Fatal("base photo bytes changed after a sign")
	}
}

func TestSignImportsOfficialPhotoWhenPoolIsEmpty(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	original := jpegFixture(t, 240, 320)
	upstreamFace := &fakeProfileFaceClient{data: original}
	photoSvc := NewFacePhotoService(database, cc, upstreamFace)

	var uploaded []byte
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploaded = append([]byte(nil), data...)
			return "face-temp-object", nil
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
		Context: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("result=%+v", result)
	}
	if upstreamFace.calls != 1 || !upstreamFace.hasDeadline {
		t.Fatalf("official import calls=%d deadline=%v", upstreamFace.calls, upstreamFace.hasDeadline)
	}
	if len(uploaded) == 0 || bytes.Equal(uploaded, original) {
		t.Fatal("sign uploaded the downloaded original or nothing")
	}
	var photos []model.FacePhoto
	if err := database.Where("user_uid = ?", 2).Find(&photos).Error; err != nil {
		t.Fatal(err)
	}
	if len(photos) != 1 || photos[0].Source != "official" || !bytes.Equal(photos[0].Data, original) {
		t.Fatalf("stored base = %+v", photos)
	}
}

func TestSignWithoutPhotoReturnsUnavailable(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	upstreamFace := &fakeProfileFaceClient{err: xxt.ErrNoProfileFacePhoto}
	photoSvc := NewFacePhotoService(database, cc, upstreamFace)

	signs := 0
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			signs++
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			t.Fatal("upload attempted without a base photo")
			return "", nil
		},
		checkFace: func(string, string, int64, string, string) (xxt.FaceCheckResult, error) {
			t.Fatal("face check attempted without a base photo")
			return xxt.FaceCheckResult{}, nil
		},
	}
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetFacePhotoService(photoSvc)
	signSvc.SetRand(fixedPhotoRand{})

	failed, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
		Context: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Success || failed.ErrorCode != SignErrorFacePhotoUnavailable {
		t.Fatalf("result=%+v, want %q", failed, SignErrorFacePhotoUnavailable)
	}
	if upstreamFace.calls != 1 || signs != 0 {
		t.Fatalf("official import calls=%d signs=%d, want one import attempt and no submit", upstreamFace.calls, signs)
	}

	plain, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plain.Success || signs != 1 {
		t.Fatalf("non-face sign result=%+v signs=%d", plain, signs)
	}
	if upstreamFace.calls != 1 {
		t.Fatalf("non-face sign triggered an official import: calls=%d", upstreamFace.calls)
	}
}

func TestBasePhotoTransformsPerExecution(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	base := jpegFixture(t, 120, 160)
	photo := seedFacePhoto(t, database, 2, base)
	baseHash := sha256.Sum256(photo.Data)

	var uploads [][]byte
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploads = append(uploads, append([]byte(nil), data...))
			return fmt.Sprintf("face-object-%d", len(uploads)), nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc-" + objectID}, nil
		},
	}
	signSvc := NewSignService(database, upstream, cc, nil)
	signSvc.SetRand(mathrand.New(mathrand.NewPCG(7, 11)))
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
			t.Fatalf("sign failed: %+v", result)
		}
		if err := database.Where("user_uid = ? AND activity_id = ?", 2, 100).Delete(&model.SignRecord{}).Error; err != nil {
			t.Fatal(err)
		}
	}

	sign()
	sign()
	if len(uploads) != 2 {
		t.Fatalf("uploads = %d, want a fresh transform upload per execution", len(uploads))
	}
	if bytes.Equal(uploads[0], uploads[1]) {
		t.Fatal("two executions uploaded identical transforms")
	}
	for _, data := range uploads {
		if len(data) > facephoto.MaxUploadBytes {
			t.Fatalf("uploaded bytes = %d", len(data))
		}
		if bytes.Contains(data, []byte("Exif")) {
			t.Fatal("uploaded transform still carries EXIF")
		}
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", photo.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(stored.Data) != baseHash {
		t.Fatal("base photo bytes changed across executions")
	}
}

func TestPickForSignPrefersUnfailedUsedPhoto(t *testing.T) {
	database, _ := newFacePhotoFixture(t)
	first := seedFacePhoto(t, database, 2, []byte("first"))
	second := seedFacePhoto(t, database, 2, []byte("second"))
	third := seedFacePhoto(t, database, 2, []byte("third"))
	if err := database.Model(&model.FacePhoto{}).Where("id = ?", first.ID).Updates(map[string]any{
		"use_count": 3, "is_failure_before": true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.FacePhoto{}).Where("id = ?", second.ID).Update("use_count", 1).Error; err != nil {
		t.Fatal(err)
	}
	chosen, err := PickForSign(database, 2, fixedPhotoRand{})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != second.ID {
		t.Fatalf("chosen = %d, want unfailed used photo %d (third=%d)", chosen.ID, second.ID, third.ID)
	}
	if _, err := PickForSign(database, 99, fixedPhotoRand{}); !errors.Is(err, facephoto.ErrNoPhotos) {
		t.Fatalf("empty pool err = %v", err)
	}
}

func TestRecordFacePhotoUseCountsAndMarks(t *testing.T) {
	database, _ := newFacePhotoFixture(t)
	photo := seedFacePhoto(t, database, 2, []byte("bytes"))
	if err := RecordFacePhotoUse(database, photo.ID, "object-1", true); err != nil {
		t.Fatal(err)
	}
	if err := RecordFacePhotoUse(database, photo.ID, "object-2", false); err != nil {
		t.Fatal(err)
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", photo.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UseCount != 2 || !stored.IsFailureBefore || stored.ObjectID != "object-2" {
		t.Fatalf("stored = %+v", stored)
	}
}
