package service

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/facephoto"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type fakeProfileFaceClient struct {
	data   []byte
	err    error
	calls  int
	mobile string
}

func (f *fakeProfileFaceClient) FetchProfileFace(mobile, _ string) ([]byte, error) {
	f.calls++
	f.mobile = mobile
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

func TestImportOfficialPhotoStoresOnlyTransformedBytes(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	original := jpegFixture(t, 240, 320)
	upstream := &fakeProfileFaceClient{data: original}
	svc := NewFacePhotoService(database, cc, upstream)
	svc.SetRand(fixedPhotoRand{})

	photo, transformed, err := svc.ImportOfficial(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if upstream.calls != 1 || upstream.mobile != "13800000002" {
		t.Fatalf("upstream calls=%d mobile=%q", upstream.calls, upstream.mobile)
	}
	if photo.Source != "official" || photo.UserUID != 2 {
		t.Fatalf("photo = %+v", photo)
	}
	if bytes.Equal(photo.Data, original) {
		t.Fatal("downloaded original bytes were persisted")
	}
	if transformed.CropWidthRatio < 0.90 || transformed.CropWidthRatio > 0.99 {
		t.Fatalf("crop width ratio = %.4f", transformed.CropWidthRatio)
	}
	if transformed.AngleDegrees < -5 || transformed.AngleDegrees > 5 {
		t.Fatalf("angle = %.4f", transformed.AngleDegrees)
	}
	if len(photo.Data) > facephoto.MaxUploadBytes {
		t.Fatalf("stored bytes = %d", len(photo.Data))
	}
	if exif := bytes.Index(photo.Data, []byte("Exif")); exif >= 0 {
		t.Fatal("stored photo still carries EXIF")
	}
}

func TestImportOfficialPhotoWithoutProfilePhoto(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	svc := NewFacePhotoService(database, cc, &fakeProfileFaceClient{err: xxt.ErrNoProfileFacePhoto})
	_, _, err := svc.ImportOfficial(1, 2)
	if !errors.Is(err, xxt.ErrNoProfileFacePhoto) {
		t.Fatalf("err = %v, want ErrNoProfileFacePhoto", err)
	}
	var count int64
	if err := database.Model(&model.FacePhoto{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("photos created on failed import: %d err=%v", count, err)
	}
}

func TestImportedOfficialPhotoCompletesFaceSign(t *testing.T) {
	database, cc := newFacePhotoFixture(t)
	original := jpegFixture(t, 240, 320)
	photoSvc := NewFacePhotoService(database, cc, &fakeProfileFaceClient{data: original})
	photoSvc.SetRand(fixedPhotoRand{})
	imported, _, err := photoSvc.ImportOfficial(1, 2)
	if err != nil {
		t.Fatal(err)
	}

	uploaded := 0
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploaded++
			if !bytes.Equal(data, imported.Data) {
				t.Fatal("sign uploaded bytes that differ from the transformed pool entry")
			}
			return "face-temp-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc"}, nil
		},
	}
	signSvc := NewSignService(database, upstream, cc, nil)
	result, err := signSvc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || uploaded != 1 {
		t.Fatalf("result=%+v uploads=%d", result, uploaded)
	}
	var stored model.FacePhoto
	if err := database.Where("id = ?", imported.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UseCount != 1 || stored.ObjectID != "face-temp-object" {
		t.Fatalf("stored = %+v", stored)
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
