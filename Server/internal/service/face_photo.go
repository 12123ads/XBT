package service

import (
	"errors"
	"fmt"
	"log"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/facephoto"
	"xbt2/server/internal/model"
)

var (
	ErrFacePhotoLimit     = errors.New("face photo pool is full")
	ErrFacePhotoNotFound  = errors.New("face photo not found")
	ErrFacePhotoForbidden = errors.New("face photo management forbidden")
	ErrFacePhotoTooLarge  = errors.New("face photo exceeds the size limit")
)

type facePhotoClient interface {
	FetchProfileFace(mobile, password string) ([]byte, error)
}

// FacePhotoService owns the managed photo pool. All reads and writes are
// scoped by the operator's delegation rights; official imports are transformed
// in memory and only the transform output ever reaches the database.
type FacePhotoService struct {
	db   *gorm.DB
	cc   *CredentialCrypto
	xxt  facePhotoClient
	rand facephoto.RandSource
}

func NewFacePhotoService(db *gorm.DB, cc *CredentialCrypto, xxtClient facePhotoClient) *FacePhotoService {
	return &FacePhotoService{db: db, cc: cc, xxt: xxtClient}
}

// SetRand installs the transform randomness used for official imports. Tests
// inject a fixed seed; production leaves it nil (time-seeded).
func (s *FacePhotoService) SetRand(rnd facephoto.RandSource) { s.rand = rnd }

func (s *FacePhotoService) List(operatorUID, targetUID int64) ([]model.FacePhoto, error) {
	if err := s.Authorize(operatorUID, targetUID); err != nil {
		return nil, err
	}
	var photos []model.FacePhoto
	if err := s.db.Where("user_uid = ?", targetUID).Order("id asc").Find(&photos).Error; err != nil {
		return nil, err
	}
	return photos, nil
}

func (s *FacePhotoService) Get(operatorUID int64, photoID uint) (model.FacePhoto, error) {
	photo, err := s.loadPhoto(photoID)
	if err != nil {
		return model.FacePhoto{}, err
	}
	if err := s.Authorize(operatorUID, photo.UserUID); err != nil {
		return model.FacePhoto{}, err
	}
	return photo, nil
}

func (s *FacePhotoService) Add(operatorUID, targetUID int64, data []byte, source string) (model.FacePhoto, error) {
	if len(data) == 0 {
		return model.FacePhoto{}, errors.New("face photo is empty")
	}
	if len(data) > facephoto.MaxUploadBytes {
		return model.FacePhoto{}, ErrFacePhotoTooLarge
	}
	if err := s.Authorize(operatorUID, targetUID); err != nil {
		return model.FacePhoto{}, err
	}
	if source == "" {
		source = "upload"
	}
	photo := model.FacePhoto{UserUID: targetUID, Data: data, Source: source}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var locked struct{ ID uint }
		if err := tx.Model(&model.User{}).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("uid = ?", targetUID).Take(&locked).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.FacePhoto{}).Where("user_uid = ?", targetUID).Count(&count).Error; err != nil {
			return err
		}
		if count >= facephoto.MaxPhotos {
			return ErrFacePhotoLimit
		}
		return tx.Create(&photo).Error
	})
	if err != nil {
		return model.FacePhoto{}, err
	}
	return photo, nil
}

func (s *FacePhotoService) Delete(operatorUID int64, photoID uint) error {
	photo, err := s.loadPhoto(photoID)
	if err != nil {
		return err
	}
	if err := s.Authorize(operatorUID, photo.UserUID); err != nil {
		return err
	}
	return s.db.Delete(&model.FacePhoto{}, photo.ID).Error
}

// ImportOfficial fetches the account's official photo with the account's own
// session and persists only the mandatory transform output. The downloaded
// original stays in this function's scope and is never written anywhere.
func (s *FacePhotoService) ImportOfficial(operatorUID, targetUID int64) (model.FacePhoto, facephoto.Result, error) {
	if err := s.Authorize(operatorUID, targetUID); err != nil {
		return model.FacePhoto{}, facephoto.Result{}, err
	}
	var count int64
	if err := s.db.Model(&model.FacePhoto{}).Where("user_uid = ?", targetUID).Count(&count).Error; err != nil {
		return model.FacePhoto{}, facephoto.Result{}, err
	}
	if count >= facephoto.MaxPhotos {
		return model.FacePhoto{}, facephoto.Result{}, ErrFacePhotoLimit
	}

	target, err := LoadActiveUser(s.db, targetUID)
	if err != nil {
		return model.FacePhoto{}, facephoto.Result{}, ErrFacePhotoForbidden
	}
	password, err := s.cc.Decrypt(target.CredentialCipher)
	if err != nil {
		return model.FacePhoto{}, facephoto.Result{}, fmt.Errorf("account credential unavailable")
	}

	original, err := s.xxt.FetchProfileFace(target.Mobile, password)
	if err != nil {
		return model.FacePhoto{}, facephoto.Result{}, err
	}
	transformed, err := facephoto.Transform(original, facephoto.Options{Rand: s.rand})
	if err != nil {
		return model.FacePhoto{}, facephoto.Result{}, fmt.Errorf("transform official face photo: %w", err)
	}

	photo, err := s.Add(operatorUID, targetUID, transformed.JPEG, "official")
	if err != nil {
		return model.FacePhoto{}, facephoto.Result{}, err
	}
	return photo, transformed, nil
}

// Authorize allows the operator to act on their own pool, or on another
// account whose selected course overlaps one of the operator's selected
// courses. This is the same delegation set used before a sign request, without
// requiring a specific activity to exist.
func (s *FacePhotoService) Authorize(operatorUID, targetUID int64) error {
	if operatorUID <= 0 || targetUID <= 0 {
		return ErrFacePhotoForbidden
	}
	if _, err := LoadActiveUser(s.db, operatorUID); err != nil {
		return ErrFacePhotoForbidden
	}
	if operatorUID == targetUID {
		return nil
	}
	if _, err := LoadActiveUser(s.db, targetUID); err != nil {
		return ErrFacePhotoForbidden
	}
	var count int64
	if err := s.db.Table("user_courses own").
		Joins("JOIN user_courses target ON target.course_id = own.course_id AND target.class_id = own.class_id").
		Where("own.user_uid = ? AND own.is_selected = true", operatorUID).
		Where("target.user_uid = ? AND target.is_selected = true", targetUID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrFacePhotoForbidden
	}
	return nil
}

func (s *FacePhotoService) loadPhoto(photoID uint) (model.FacePhoto, error) {
	if photoID == 0 {
		return model.FacePhoto{}, ErrFacePhotoNotFound
	}
	var photo model.FacePhoto
	if err := s.db.Where("id = ?", photoID).Take(&photo).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.FacePhoto{}, ErrFacePhotoNotFound
		}
		return model.FacePhoto{}, err
	}
	return photo, nil
}

// PickForSign loads one photo for an account. After a failure, photos that
// never failed and were already used win over untouched and known-bad photos.
func PickForSign(database *gorm.DB, targetUID int64, rnd facephoto.RandSource) (model.FacePhoto, error) {
	var candidates []model.FacePhoto
	if err := database.Select("id, use_count, is_failure_before").Where("user_uid = ?", targetUID).Order("id asc").Find(&candidates).Error; err != nil {
		return model.FacePhoto{}, err
	}
	pool := make([]facephoto.Candidate, 0, len(candidates))
	hadFailure := false
	for _, candidate := range candidates {
		pool = append(pool, facephoto.Candidate{
			ID:              candidate.ID,
			UseCount:        candidate.UseCount,
			IsFailureBefore: candidate.IsFailureBefore,
		})
		hadFailure = hadFailure || candidate.IsFailureBefore
	}
	chosen, err := facephoto.Select(pool, hadFailure, rnd)
	if err != nil {
		return model.FacePhoto{}, err
	}
	var photo model.FacePhoto
	if err := database.Where("id = ?", chosen.ID).Take(&photo).Error; err != nil {
		return model.FacePhoto{}, err
	}
	return photo, nil
}

// RecordFacePhotoUse counts one upload/check attempt and optionally marks the
// photo as previously failing. Errors are logged by callers instead of
// failing the sign flow: bookkeeping must not hide the upstream outcome.
func RecordFacePhotoUse(database *gorm.DB, photoID uint, objectID string, failed bool) error {
	if photoID == 0 {
		return nil
	}
	updates := map[string]any{
		"use_count":         gorm.Expr("use_count + 1"),
		"is_failure_before": gorm.Expr("is_failure_before OR ?", failed),
	}
	if objectID != "" {
		updates["object_id"] = objectID
	}
	return database.Model(&model.FacePhoto{}).Where("id = ?", photoID).Updates(updates).Error
}

func MarkFacePhotoFailed(database *gorm.DB, photoID uint) {
	if photoID == 0 {
		return
	}
	if err := database.Model(&model.FacePhoto{}).Where("id = ?", photoID).Update("is_failure_before", true).Error; err != nil {
		log.Printf("mark face photo failed: id=%d err=%v", photoID, err)
	}
}
