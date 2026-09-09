package account

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestTokenCipherRoundTripAndTamper(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewTokenCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Seal("apple-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Open(ciphertext, nonce)
	if err != nil || plaintext != "apple-refresh-token" {
		t.Fatalf("Open() = %q, %v", plaintext, err)
	}
	ciphertext[0] ^= 1
	if _, err := cipher.Open(ciphertext, nonce); err == nil {
		t.Fatal("tampered ciphertext should be rejected")
	}
}

func TestTokenCipherRejectsInvalidKey(t *testing.T) {
	if _, err := NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil ||
		!strings.Contains(err.Error(), "32 字节") {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenCipherBindsAdditionalData(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, _ := NewTokenCipher(key)
	ciphertext, nonce, err := cipher.SealFor("apple-user\x00menu", "refresh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.OpenFor("other-user\x00menu", ciphertext, nonce); err == nil {
		t.Fatal("credential must not decrypt under different identity context")
	}
	if _, err := cipher.OpenFor("apple-user\x00menu", ciphertext, nonce[:3]); err == nil {
		t.Fatal("invalid nonce length should return an error")
	}
}
