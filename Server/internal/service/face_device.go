package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

// FaceDeviceClient fetches the upstream clientId used to sign faceResult. It is
// satisfied by *xxt.Client.
type FaceDeviceClient interface {
	FetchClientIDContext(ctx context.Context, mobile, password string, info xxt.DeviceInfo) (string, error)
}

// The production client must keep satisfying the backfill contract.
var _ FaceDeviceClient = (*xxt.Client)(nil)

// EnsureFaceDevice loads or creates the account's synthetic device profile and
// refreshes clientId from upstream. The returned error describes only the
// clientId fetch or encryption; callers that cannot degrade (login, account
// creation) ignore it because a missing signature never blocks those flows.
func EnsureFaceDevice(ctx context.Context, database *gorm.DB, cc *CredentialCrypto, client FaceDeviceClient, uid int64, mobile, password string) (string, string, error) {
	var existing model.User
	err := database.Select("device_info_cipher, client_id_cipher").Where("uid = ?", uid).Take(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("face device profile load failed: uid=%d err=%v", uid, err)
	}
	deviceCipher := existing.DeviceInfoCipher
	clientIDCipher := existing.ClientIDCipher

	info, ok := loadDeviceInfo(cc, deviceCipher)
	if !ok {
		info = xxt.GenerateDeviceInfo(uid, mobile)
		encoded, err := json.Marshal(info)
		if err != nil {
			log.Printf("face device profile encode failed: uid=%d err=%v", uid, err)
		} else if cipher, err := cc.Encrypt(string(encoded)); err != nil {
			log.Printf("face device profile encrypt failed: uid=%d err=%v", uid, err)
		} else {
			deviceCipher = cipher
		}
	}

	clientID, err := client.FetchClientIDContext(ctx, mobile, password, info)
	if err != nil {
		log.Printf("clientId fetch failed: uid=%d err=%v", uid, err)
		return deviceCipher, clientIDCipher, err
	}
	if clientID == "" {
		return deviceCipher, clientIDCipher, nil
	}
	cipher, err := cc.Encrypt(clientID)
	if err != nil {
		log.Printf("clientId encrypt failed: uid=%d err=%v", uid, err)
		return deviceCipher, clientIDCipher, err
	}
	return deviceCipher, cipher, nil
}

func loadDeviceInfo(cc *CredentialCrypto, cipher string) (xxt.DeviceInfo, bool) {
	if cipher == "" {
		return xxt.DeviceInfo{}, false
	}
	plain, err := cc.Decrypt(cipher)
	if err != nil {
		return xxt.DeviceInfo{}, false
	}
	var info xxt.DeviceInfo
	if err := json.Unmarshal([]byte(plain), &info); err != nil {
		return xxt.DeviceInfo{}, false
	}
	return info, info.Valid()
}
