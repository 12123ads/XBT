// Package faker builds import links that ChaoxingSignFaker (CSF) recognizes.
//
// The link format is not versioned by CSF, so it is pinned to the format
// verified against CSF 9ae5398 (2026-10-20). The `pwd` parameter is not a
// password hash: it is AES/CBC with a fixed key that CSF ships, so anyone with
// the link can recover the account password. Callers must treat the result as a
// password and must not persist or log it.
package faker

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	// LinkBase is the fixed CSF import endpoint.
	LinkBase = "http://cdn.aquamarine5.fun/?"
	// TransferKey is the AES key and IV CSF uses for the `pwd` parameter.
	TransferKey = "u2oh6Vu^HWe4_AES"
	// QRSize is the generated PNG edge length in pixels.
	QRSize = 512
)

// BuildURL renders the CSF import link for one account.
//
// Parameter order is fixed (phone, pwd, name, face, dc) and every value is
// query-escaped. `face` is always empty because xbt has no persisted cloud
// object id.
func BuildURL(mobile, password, name, deviceCode string) (string, error) {
	if strings.TrimSpace(mobile) == "" {
		return "", errors.New("mobile is required")
	}
	if password == "" {
		return "", errors.New("password is required")
	}
	if strings.TrimSpace(deviceCode) == "" {
		return "", errors.New("device code is required")
	}
	encrypted, err := EncryptPassword(password)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.Grow(len(LinkBase) + len(mobile) + len(name) + len(encrypted) + len(deviceCode) + 48)
	b.WriteString(LinkBase)
	b.WriteString("phone=")
	b.WriteString(url.QueryEscape(mobile))
	b.WriteString("&pwd=")
	b.WriteString(url.QueryEscape(encrypted))
	b.WriteString("&name=")
	b.WriteString(url.QueryEscape(name))
	b.WriteString("&face=&dc=")
	b.WriteString(url.QueryEscape(deviceCode))
	return b.String(), nil
}

// EncryptPassword renders Base64(AES-128-CBC/PKCS5(plain, key, key)).
func EncryptPassword(plain string) (string, error) {
	block, err := aes.NewCipher([]byte(TransferKey))
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad([]byte(plain), block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(TransferKey)).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

// QRPNG encodes content as an error-correction-medium PNG at QRSize.
func QRPNG(content string) ([]byte, error) {
	return qrcode.Encode(content, qrcode.Medium, QRSize)
}

// DataURL wraps PNG bytes as a browser-usable data URL.
func DataURL(png []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

func pkcs7Pad(src []byte, blockSize int) []byte {
	padding := blockSize - len(src)%blockSize
	pad := byte(padding)
	out := make([]byte, len(src)+padding)
	copy(out, src)
	for i := len(src); i < len(out); i++ {
		out[i] = pad
	}
	return out
}
