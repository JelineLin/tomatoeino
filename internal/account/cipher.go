package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// TokenCipher 只用于保护必须可恢复的上游凭证（Apple refresh token）。
// 平台自己的 refresh token 不需要恢复，仍然只保存 SHA-256 摘要。
type TokenCipher struct {
	aead cipher.AEAD
}

func NewTokenCipher(encodedKey string) (*TokenCipher, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encodedKey)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("ACCOUNT_DATA_KEY 必须是 Base64 编码的 32 字节密钥")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化凭证加密失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化凭证加密失败: %w", err)
	}
	return &TokenCipher{aead: aead}, nil
}

func (c *TokenCipher) Seal(plaintext string) (ciphertext, nonce []byte, err error) {
	return c.SealFor("", plaintext)
}

func (c *TokenCipher) SealFor(aad, plaintext string) (ciphertext, nonce []byte, err error) {
	if plaintext == "" {
		return nil, nil, fmt.Errorf("待加密凭证为空")
	}
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("生成凭证加密 nonce 失败: %w", err)
	}
	return c.aead.Seal(nil, nonce, []byte(plaintext), []byte(aad)), nonce, nil
}

func (c *TokenCipher) Open(ciphertext, nonce []byte) (string, error) {
	return c.OpenFor("", ciphertext, nonce)
}

func (c *TokenCipher) OpenFor(aad string, ciphertext, nonce []byte) (string, error) {
	if len(nonce) != c.aead.NonceSize() || len(ciphertext) < c.aead.Overhead() {
		return "", fmt.Errorf("加密凭证结构无效")
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return "", fmt.Errorf("解密上游凭证失败: %w", err)
	}
	return string(plaintext), nil
}
