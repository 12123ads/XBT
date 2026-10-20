package im

import (
	"crypto/des"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// imPasswordDESKey is the fixed key used by 学习通 when it returns
// accountInfo.imAccount.password. It is protocol material, not a user secret.
var imPasswordDESKey = []byte("SL2(M/eD")

// DecryptPassword turns the upstream hex-encoded DES/ECB/PKCS5Padding value
// into the plaintext password sent to the Easemob token endpoint.
func DecryptPassword(encryptedHex string) (string, error) {
	encryptedHex = strings.TrimSpace(encryptedHex)
	if encryptedHex == "" {
		return "", errors.New("im password is empty")
	}
	raw, err := hex.DecodeString(encryptedHex)
	if err != nil {
		return "", fmt.Errorf("im password is not valid hex: %w", err)
	}
	if len(raw) == 0 || len(raw)%des.BlockSize != 0 {
		return "", errors.New("im password is not a whole number of DES blocks")
	}
	block, err := des.NewCipher(imPasswordDESKey)
	if err != nil {
		return "", fmt.Errorf("initialize im password cipher: %w", err)
	}
	plain := make([]byte, len(raw))
	for offset := 0; offset < len(raw); offset += des.BlockSize {
		block.Decrypt(plain[offset:offset+des.BlockSize], raw[offset:offset+des.BlockSize])
	}
	unpadded, err := pkcs5Unpad(plain, des.BlockSize)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(unpadded) {
		return "", errors.New("im password is not valid UTF-8")
	}
	return string(unpadded), nil
}

func pkcs5Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid im password padding length")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("invalid im password padding")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, errors.New("invalid im password padding")
		}
	}
	return data[:len(data)-padding], nil
}
