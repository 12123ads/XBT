package service

import (
	"context"
	"log"
	"time"

	"xbt2/server/internal/model"
)

const (
	// faceClientIDFetchTimeout bounds the on-demand upstream call. It is much
	// shorter than a sign window so a slow account-info endpoint cannot delay
	// the face request.
	faceClientIDFetchTimeout = 5 * time.Second
	// faceClientIDFailCooldown stops an account with a broken upstream fetch
	// from producing one extra userInfo call per sign attempt.
	faceClientIDFailCooldown = 10 * time.Minute
	// faceClientIDFailureLimit caps the cooldown map by pruning expired entries
	// before inserting. Only failing accounts occupy it.
	faceClientIDFailureLimit = 4096
)

// faceClientID returns the plaintext clientId used to sign faceResult. A stored
// value is used directly; an empty or unreadable value triggers one background
// fetch per cooldown window. Failure is not an error: the caller sends the face
// request without a signature and the sign continues.
func (s *SignService) faceClientID(ctx context.Context, target *model.User, password string) string {
	if target.ClientIDCipher != "" {
		plain, err := s.cc.Decrypt(target.ClientIDCipher)
		if err == nil && plain != "" {
			return plain
		}
		if err != nil {
			log.Printf("face clientId decrypt failed: uid=%d err=%v", target.UID, err)
		}
	}
	return s.backfillFaceClientID(ctx, target, password)
}

func (s *SignService) backfillFaceClientID(ctx context.Context, target *model.User, password string) string {
	lookup := s.faceDevice
	if lookup == nil {
		return ""
	}
	if s.faceClientIDCoolingDown(target.UID) {
		log.Printf("face clientId backfill skipped: uid=%d reason=cooldown", target.UID)
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, faceClientIDFetchTimeout)
	defer cancel()

	deviceCipher, clientIDCipher, err := EnsureFaceDevice(ctx, s.db, s.cc, lookup, target.UID, target.Mobile, password)
	if err != nil {
		s.markFaceClientIDFailure(target.UID)
		log.Printf("face clientId backfill failed: uid=%d err=%v", target.UID, err)
		return ""
	}
	if clientIDCipher == "" {
		s.markFaceClientIDFailure(target.UID)
		log.Printf("face clientId backfill failed: uid=%d err=empty clientId", target.UID)
		return ""
	}
	plain, err := s.cc.Decrypt(clientIDCipher)
	if err != nil || plain == "" {
		s.markFaceClientIDFailure(target.UID)
		log.Printf("face clientId backfill failed: uid=%d err=unreadable clientId (%v)", target.UID, err)
		return ""
	}

	updates := map[string]interface{}{"client_id_cipher": clientIDCipher}
	if deviceCipher != "" && deviceCipher != target.DeviceInfoCipher {
		updates["device_info_cipher"] = deviceCipher
	}
	if err := s.db.Model(&model.User{}).Where("uid = ?", target.UID).Updates(updates).Error; err != nil {
		// The fetched value still signs this request; only the write-back failed.
		s.markFaceClientIDFailure(target.UID)
		log.Printf("face clientId backfill failed: uid=%d err=persist: %v", target.UID, err)
		return plain
	}
	s.clearFaceClientIDFailure(target.UID)
	target.ClientIDCipher = clientIDCipher
	if cipher, ok := updates["device_info_cipher"].(string); ok {
		target.DeviceInfoCipher = cipher
	}
	log.Printf("face clientId backfill succeeded: uid=%d", target.UID)
	return plain
}

func (s *SignService) faceClientIDCoolingDown(uid int64) bool {
	s.faceClientIDMu.Lock()
	defer s.faceClientIDMu.Unlock()
	failedAt, ok := s.faceClientIDFailures[uid]
	return ok && time.Since(failedAt) < faceClientIDFailCooldown
}

func (s *SignService) markFaceClientIDFailure(uid int64) {
	s.faceClientIDMu.Lock()
	defer s.faceClientIDMu.Unlock()
	if len(s.faceClientIDFailures) >= faceClientIDFailureLimit {
		for key, failedAt := range s.faceClientIDFailures {
			if time.Since(failedAt) >= faceClientIDFailCooldown {
				delete(s.faceClientIDFailures, key)
			}
		}
	}
	s.faceClientIDFailures[uid] = time.Now()
}

func (s *SignService) clearFaceClientIDFailure(uid int64) {
	s.faceClientIDMu.Lock()
	defer s.faceClientIDMu.Unlock()
	delete(s.faceClientIDFailures, uid)
}
