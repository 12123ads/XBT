package im

import (
	"crypto/des"
	"encoding/hex"
	"testing"
)

func encryptIMPasswordForTest(t *testing.T, plain string) string {
	t.Helper()
	block, err := des.NewCipher(imPasswordDESKey)
	if err != nil {
		t.Fatal(err)
	}
	padding := des.BlockSize - len(plain)%des.BlockSize
	padded := append([]byte(plain), make([]byte, padding)...)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	out := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += des.BlockSize {
		block.Encrypt(out[offset:offset+des.BlockSize], padded[offset:offset+des.BlockSize])
	}
	return hex.EncodeToString(out)
}

func TestDecryptPasswordKnownFixture(t *testing.T) {
	// Generated with: openssl enc -des-ecb -provider legacy -K 534c32284d2f6544
	// for plaintext "secret-im-pass".
	got, err := DecryptPassword("0bd654e244170185155ddeefe47a8007")
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-im-pass" {
		t.Fatalf("got %q, want secret-im-pass", got)
	}
}

func TestDecryptPasswordRoundTrip(t *testing.T) {
	for _, plain := range []string{"a", "exactly8", "汉字密码-123", "twelve-bytes"} {
		encrypted := encryptIMPasswordForTest(t, plain)
		got, err := DecryptPassword(encrypted)
		if err != nil || got != plain {
			t.Fatalf("round trip %q: got %q err=%v", plain, got, err)
		}
	}
}

func TestDecryptPasswordRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "zz", "00112233", "0bd654e244170185155ddeefe47a800g"} {
		if _, err := DecryptPassword(input); err == nil {
			t.Fatalf("accepted invalid input %q", input)
		}
	}
}
