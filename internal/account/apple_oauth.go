package account

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	appleTokenURL  = "https://appleid.apple.com/auth/token"
	appleRevokeURL = "https://appleid.apple.com/auth/revoke"
)

var ErrInvalidAppleAuthorization = errors.New("Apple authorization code 或 refresh token 无效")

type AppleTokens struct {
	AccessToken   string
	RefreshToken  string
	IdentityToken string
}

type AppleTokenService interface {
	ExchangeCode(context.Context, string, string, string) (AppleTokens, error)
	Revoke(context.Context, string, string) error
}

type AppleOAuthClient struct {
	teamID     string
	keyID      string
	privateKey *ecdsa.PrivateKey
	client     *http.Client
	tokenURL   string
	revokeURL  string
	now        func() time.Time
}

func NewAppleOAuthClient(teamID, keyID string, privateKeyPEM []byte) (*AppleOAuthClient, error) {
	teamID, keyID = strings.TrimSpace(teamID), strings.TrimSpace(keyID)
	if teamID == "" || keyID == "" {
		return nil, fmt.Errorf("APPLE_TEAM_ID 和 APPLE_KEY_ID 必须设置")
	}
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("APPLE_PRIVATE_KEY_PATH 中不是有效的 PEM 私钥")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 Apple PKCS#8 私钥失败: %w", err)
	}
	privateKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("Apple 私钥不是 EC 私钥")
	}
	if privateKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("Apple 私钥必须使用 P-256 曲线")
	}
	return &AppleOAuthClient{
		teamID: teamID, keyID: keyID, privateKey: privateKey,
		client:   &http.Client{Timeout: 8 * time.Second},
		tokenURL: appleTokenURL, revokeURL: appleRevokeURL, now: time.Now,
	}, nil
}

func (c *AppleOAuthClient) ExchangeCode(ctx context.Context, clientID, code, redirectURI string) (AppleTokens, error) {
	clientID, code = strings.TrimSpace(clientID), strings.TrimSpace(code)
	if clientID == "" || code == "" {
		return AppleTokens{}, ErrInvalidAppleAuthorization
	}
	secret, err := c.clientSecret(clientID)
	if err != nil {
		return AppleTokens{}, err
	}
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {secret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
	}
	if redirectURI = strings.TrimSpace(redirectURI); redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	resp, err := c.postForm(ctx, c.tokenURL, form)
	if err != nil {
		return AppleTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AppleTokens{}, appleOAuthError(resp)
	}
	var body struct {
		AccessToken   string `json:"access_token"`
		RefreshToken  string `json:"refresh_token"`
		IdentityToken string `json:"id_token"`
	}
	if err := decodeLimitedJSON(resp.Body, &body); err != nil {
		return AppleTokens{}, fmt.Errorf("%w: 解析 token 响应失败: %v", ErrAppleUnavailable, err)
	}
	if body.RefreshToken == "" || body.IdentityToken == "" {
		return AppleTokens{}, fmt.Errorf("%w: token 响应缺少必要字段", ErrAppleUnavailable)
	}
	return AppleTokens{
		AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, IdentityToken: body.IdentityToken,
	}, nil
}

func (c *AppleOAuthClient) Revoke(ctx context.Context, clientID, refreshToken string) error {
	clientID, refreshToken = strings.TrimSpace(clientID), strings.TrimSpace(refreshToken)
	if clientID == "" || refreshToken == "" {
		return ErrInvalidAppleAuthorization
	}
	secret, err := c.clientSecret(clientID)
	if err != nil {
		return err
	}
	resp, err := c.postForm(ctx, c.revokeURL, url.Values{
		"client_id":       {clientID},
		"client_secret":   {secret},
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return appleOAuthError(resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

func (c *AppleOAuthClient) clientSecret(clientID string) (string, error) {
	now := c.now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer: c.teamID, Subject: clientID,
		Audience: jwt.ClaimStrings{appleIssuer},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = c.keyID
	secret, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("签发 Apple client secret 失败: %w", err)
	}
	return secret, nil
}

func (c *AppleOAuthClient) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: 创建 Apple 请求失败: %v", ErrAppleUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAppleUnavailable, err)
	}
	return resp, nil
}

func appleOAuthError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = decodeLimitedJSON(resp.Body, &body)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: Apple HTTP %d", ErrAppleUnavailable, resp.StatusCode)
	}
	if body.Error == "invalid_grant" {
		return fmt.Errorf("%w: %s", ErrInvalidAppleAuthorization, body.Error)
	}
	return fmt.Errorf("%w: Apple HTTP %d (%s)", ErrAppleUnavailable, resp.StatusCode, body.Error)
}

func decodeLimitedJSON(reader io.Reader, target any) error {
	return json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(target)
}
