package handler

import (
	"encoding/json"
	"errors"
	"log"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

type faceDeviceClient interface {
	FetchClientID(mobile, password string, info xxt.DeviceInfo) (string, error)
}

// ensureFaceDevice loads or creates the account's synthetic device profile and
// refreshes clientId from upstream. It never fails the caller: a missing or
// expired clientId only means faceResult is sent without its device signature.
func ensureFaceDevice(database *gorm.DB, cc *service.CredentialCrypto, client faceDeviceClient, uid int64, mobile, password string) (deviceCipher, clientIDCipher string) {
	var existing model.User
	err := database.Select("device_info_cipher, client_id_cipher").Where("uid = ?", uid).Take(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("face device profile load failed: uid=%d err=%v", uid, err)
	}
	deviceCipher = existing.DeviceInfoCipher
	clientIDCipher = existing.ClientIDCipher

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

	clientID, err := client.FetchClientID(mobile, password, info)
	if err != nil {
		log.Printf("clientId fetch failed: uid=%d err=%v", uid, err)
		return deviceCipher, clientIDCipher
	}
	if clientID == "" {
		return deviceCipher, clientIDCipher
	}
	cipher, err := cc.Encrypt(clientID)
	if err != nil {
		log.Printf("clientId encrypt failed: uid=%d err=%v", uid, err)
		return deviceCipher, clientIDCipher
	}
	return deviceCipher, cipher
}

func loadDeviceInfo(cc *service.CredentialCrypto, cipher string) (xxt.DeviceInfo, bool) {
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
