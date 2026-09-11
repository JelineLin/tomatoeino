package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"tomato-platform/internal/observability"
)

const loginChallengeTTL = 5 * time.Minute

var ErrInvalidInput = errors.New("请求参数无效")

type AppleIdentityVerifier interface {
	Verify(context.Context, string) (AppleIdentity, error)
}

type Service struct {
	store         AccountStore
	appleVerifier AppleIdentityVerifier
	appleTokens   AppleTokenService
	cipher        *TokenCipher
	tokens        *TokenManager
	now           func() time.Time
}

type Challenge struct {
	ID        string    `json:"challenge_id"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type LoginInput struct {
	IdentityToken     string `json:"identity_token"`
	AuthorizationCode string `json:"authorization_code"`
	RedirectURI       string `json:"redirect_uri"`
	ChallengeID       string `json:"challenge_id"`
	ProductCode       string `json:"product_code"`
	DisplayName       string `json:"display_name"`
	Device            Device `json:"device"`
}

type TokenSet struct {
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	TokenType        string    `json:"token_type"`
	User             User      `json:"user"`
}

func NewService(store AccountStore, appleVerifier AppleIdentityVerifier, appleTokens AppleTokenService, cipher *TokenCipher, tokens *TokenManager) *Service {
	return &Service{
		store: store, appleVerifier: appleVerifier, appleTokens: appleTokens,
		cipher: cipher, tokens: tokens, now: time.Now,
	}
}

func (s *Service) NewAppleChallenge(ctx context.Context, productCode string) (Challenge, error) {
	productCode = strings.TrimSpace(productCode)
	if !validProduct(productCode) {
		return Challenge{}, fmt.Errorf("%w: product_code 无效", ErrInvalidInput)
	}
	nonce, hash, err := NewOpaqueToken()
	if err != nil {
		return Challenge{}, err
	}
	expiresAt := s.now().UTC().Add(loginChallengeTTL)
	id, err := s.store.CreateChallenge(ctx, productCode, hash, expiresAt)
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{ID: id, Nonce: nonce, ExpiresAt: expiresAt}, nil
}

func (s *Service) LoginApple(ctx context.Context, in LoginInput) (TokenSet, error) {
	in.IdentityToken = strings.TrimSpace(in.IdentityToken)
	in.AuthorizationCode = strings.TrimSpace(in.AuthorizationCode)
	in.RedirectURI = strings.TrimSpace(in.RedirectURI)
	in.ChallengeID = strings.TrimSpace(in.ChallengeID)
	in.ProductCode = strings.TrimSpace(in.ProductCode)
	in.Device.ID = strings.TrimSpace(in.Device.ID)
	in.Device.Name = cleanDisplayText(in.Device.Name, 100)
	in.Device.Platform = cleanDisplayText(in.Device.Platform, 40)
	in.DisplayName = cleanDisplayText(in.DisplayName, 100)
	if in.IdentityToken == "" || in.AuthorizationCode == "" || in.ChallengeID == "" ||
		!validProduct(in.ProductCode) || in.Device.ID == "" {
		return TokenSet{}, fmt.Errorf("%w: 登录参数不完整", ErrInvalidInput)
	}
	if utf8.RuneCountInString(in.Device.ID) > 200 {
		return TokenSet{}, fmt.Errorf("%w: device.id 过长", ErrInvalidInput)
	}
	identity, err := s.appleVerifier.Verify(ctx, in.IdentityToken)
	if err != nil {
		return TokenSet{}, err
	}
	appleTokens, err := s.appleTokens.ExchangeCode(ctx, identity.ClientID, in.AuthorizationCode, in.RedirectURI)
	if err != nil {
		return TokenSet{}, err
	}
	exchangedIdentity, err := s.appleVerifier.Verify(ctx, appleTokens.IdentityToken)
	if err != nil {
		return TokenSet{}, err
	}
	if exchangedIdentity.Subject != identity.Subject || exchangedIdentity.ClientID != identity.ClientID ||
		exchangedIdentity.Nonce != identity.Nonce {
		return TokenSet{}, ErrInvalidAppleAuthorization
	}
	credentialAAD := appleCredentialAAD(identity.Subject, identity.ClientID)
	appleCiphertext, appleNonce, err := s.cipher.SealFor(credentialAAD, appleTokens.RefreshToken)
	if err != nil {
		return TokenSet{}, err
	}
	refreshToken, refreshHash, err := NewOpaqueToken()
	if err != nil {
		return TokenSet{}, err
	}
	refreshExpiresAt := RefreshTokenExpiresAt(s.now())
	session, err := s.store.LoginApple(ctx, AppleLoginParams{
		ChallengeID:            in.ChallengeID,
		ProductCode:            in.ProductCode,
		Identity:               identity,
		AppleClientID:          identity.ClientID,
		AppleRefreshHash:       HashOpaqueToken(appleTokens.RefreshToken),
		AppleRefreshCiphertext: appleCiphertext,
		AppleRefreshNonce:      appleNonce,
		DisplayName:            in.DisplayName,
		Device:                 in.Device,
		RefreshHash:            refreshHash,
		RefreshExpiresAt:       refreshExpiresAt,
	})
	if err != nil {
		return TokenSet{}, err
	}
	observability.SetUserID(ctx, session.User.ID)
	return s.issue(session, refreshToken, refreshExpiresAt)
}

func (s *Service) Refresh(ctx context.Context, rawRefreshToken string) (TokenSet, error) {
	rawRefreshToken = strings.TrimSpace(rawRefreshToken)
	if rawRefreshToken == "" {
		return TokenSet{}, ErrInvalidSession
	}
	newToken, newHash, err := NewOpaqueToken()
	if err != nil {
		return TokenSet{}, err
	}
	expiresAt := RefreshTokenExpiresAt(s.now())
	session, err := s.store.RotateSession(ctx, HashOpaqueToken(rawRefreshToken), newHash, expiresAt)
	if err != nil {
		return TokenSet{}, err
	}
	observability.SetUserID(ctx, session.User.ID)
	return s.issue(session, newToken, expiresAt)
}

func (s *Service) Authenticate(ctx context.Context, rawAccessToken string) (UserSession, error) {
	claims, err := s.tokens.VerifyAccessToken(rawAccessToken)
	if err != nil {
		return UserSession{}, ErrInvalidSession
	}
	user, err := s.store.UserForSession(ctx, claims.Subject, claims.SessionID)
	if err != nil {
		return UserSession{}, err
	}
	observability.SetUserID(ctx, user.ID)
	return UserSession{User: user, SessionID: claims.SessionID}, nil
}

func (s *Service) Logout(ctx context.Context, rawAccessToken string) error {
	session, err := s.Authenticate(ctx, rawAccessToken)
	if err != nil {
		return err
	}
	return s.store.RevokeSession(ctx, session.User.ID, session.SessionID)
}

func (s *Service) RequestDeletion(ctx context.Context, rawAccessToken string) (DeletionJob, error) {
	claims, err := s.tokens.VerifyAccessToken(rawAccessToken)
	if err != nil {
		return DeletionJob{}, ErrInvalidSession
	}
	observability.SetUserID(ctx, claims.Subject)
	return s.store.RequestDeletion(ctx, claims.Subject, claims.SessionID)
}

func (s *Service) issue(session UserSession, refreshToken string, refreshExpiresAt time.Time) (TokenSet, error) {
	accessToken, accessExpiresAt, err := s.tokens.IssueAccessToken(session.User.ID, session.SessionID)
	if err != nil {
		return TokenSet{}, err
	}
	return TokenSet{
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshExpiresAt,
		TokenType:        "Bearer",
		User:             session.User,
	}, nil
}

func IsAuthenticationError(err error) bool {
	return errors.Is(err, ErrInvalidChallenge) || errors.Is(err, ErrInvalidSession) ||
		errors.Is(err, ErrAccountUnavailable) || errors.Is(err, ErrProductUnavailable) ||
		errors.Is(err, ErrInvalidAppleIdentity) || errors.Is(err, ErrInvalidAppleAuthorization)
}

func validProduct(code string) bool {
	return code == "menu" || code == "english"
}

func cleanDisplayText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	out := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if utf8.RuneCountInString(out) <= maxRunes {
		return out
	}
	runes := []rune(out)
	return string(runes[:maxRunes])
}
