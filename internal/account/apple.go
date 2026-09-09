package account

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	appleIssuer  = "https://appleid.apple.com"
	appleKeysURL = "https://appleid.apple.com/auth/keys"
)

var (
	ErrInvalidAppleIdentity = errors.New("Apple identity token 无效")
	ErrAppleUnavailable     = errors.New("Apple 身份服务不可用")
)

type AppleIdentity struct {
	Subject       string
	ClientID      string
	Email         string
	EmailVerified bool
	Nonce         string
}

type AppleVerifier struct {
	client    *http.Client
	audiences []string
	keysURL   string
	now       func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	keysUntil time.Time
}

func NewAppleVerifier(clientIDs []string) (*AppleVerifier, error) {
	audiences := compactStrings(clientIDs)
	if len(audiences) == 0 {
		return nil, fmt.Errorf("APPLE_CLIENT_IDS 未设置")
	}
	return &AppleVerifier{
		client:    &http.Client{Timeout: 5 * time.Second},
		audiences: audiences,
		keysURL:   appleKeysURL,
		now:       time.Now,
	}, nil
}

type appleClaims struct {
	Email         string       `json:"email"`
	EmailVerified flexibleBool `json:"email_verified"`
	Nonce         string       `json:"nonce"`
	jwt.RegisteredClaims
}

type flexibleBool bool

func (b *flexibleBool) UnmarshalJSON(data []byte) error {
	var boolean bool
	if err := json.Unmarshal(data, &boolean); err == nil {
		*b = flexibleBool(boolean)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("email_verified 格式无效")
	}
	switch strings.ToLower(text) {
	case "true":
		*b = true
	case "false":
		*b = false
	default:
		return fmt.Errorf("email_verified 格式无效")
	}
	return nil
}

func (v *AppleVerifier) Verify(ctx context.Context, rawToken string) (AppleIdentity, error) {
	var claims appleClaims
	token, err := jwt.ParseWithClaims(rawToken, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("Apple token 缺少 kid")
		}
		return v.key(ctx, kid)
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(appleIssuer),
		jwt.WithAudience(v.audiences...),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(v.now),
	)
	if errors.Is(err, ErrAppleUnavailable) {
		return AppleIdentity{}, err
	}
	if err != nil || !token.Valid || strings.TrimSpace(claims.Subject) == "" ||
		strings.TrimSpace(claims.Nonce) == "" {
		return AppleIdentity{}, ErrInvalidAppleIdentity
	}
	clientID := matchingAudience(claims.Audience, v.audiences)
	if clientID == "" {
		return AppleIdentity{}, ErrInvalidAppleIdentity
	}
	return AppleIdentity{
		Subject:       claims.Subject,
		ClientID:      clientID,
		Email:         strings.TrimSpace(claims.Email),
		EmailVerified: bool(claims.EmailVerified),
		Nonce:         claims.Nonce,
	}, nil
}

func matchingAudience(actual jwt.ClaimStrings, expected []string) string {
	for _, candidate := range actual {
		for _, allowed := range expected {
			if candidate == allowed {
				return candidate
			}
		}
	}
	return ""
}

func (v *AppleVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, fresh := v.keys[kid], v.now().Before(v.keysUntil)
	v.mu.RUnlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := v.refreshKeys(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAppleUnavailable, err)
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if key = v.keys[kid]; key == nil {
		return nil, fmt.Errorf("Apple 公钥 kid 不存在")
	}
	return key, nil
}

type appleJWKSet struct {
	Keys []struct {
		KID string `json:"kid"`
		KTY string `json:"kty"`
		ALG string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (v *AppleVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.keysURL, nil)
	if err != nil {
		return fmt.Errorf("创建 Apple 公钥请求失败: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("获取 Apple 公钥失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("获取 Apple 公钥失败: HTTP %d", resp.StatusCode)
	}
	var set appleJWKSet
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&set); err != nil {
		return fmt.Errorf("解析 Apple 公钥失败: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, item := range set.Keys {
		if item.KID == "" || item.KTY != "RSA" || (item.ALG != "" && item.ALG != "RS256") {
			continue
		}
		key, err := rsaKey(item.N, item.E)
		if err == nil {
			keys[item.KID] = key
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("Apple 公钥响应中没有可用的 RSA key")
	}
	v.mu.Lock()
	v.keys = keys
	v.keysUntil = v.now().Add(6 * time.Hour)
	v.mu.Unlock()
	return nil
}

func rsaKey(modulus, exponent string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil || len(nBytes) == 0 {
		return nil, fmt.Errorf("RSA modulus 无效")
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
		return nil, fmt.Errorf("RSA exponent 无效")
	}
	padded := make([]byte, 4)
	copy(padded[4-len(eBytes):], eBytes)
	e := binary.BigEndian.Uint32(padded)
	if e < 2 || e > 1<<31-1 {
		return nil, fmt.Errorf("RSA exponent 超出范围")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
