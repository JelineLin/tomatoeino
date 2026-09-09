package account

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAppleVerifier(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	jwks := testJWKSet(t, "key-1", &privateKey.PublicKey)
	requests := 0
	verifier, err := NewAppleVerifier([]string{"com.example.menu", "com.example.english"})
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	verifier.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(jwks)),
		}, nil
	})}

	raw := signAppleToken(t, privateKey, "key-1", appleClaims{
		Email:         "parent@example.com",
		EmailVerified: true,
		Nonce:         "login-nonce",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    appleIssuer,
			Subject:   "apple-user-1",
			Audience:  jwt.ClaimStrings{"com.example.menu"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		},
	})
	for range 2 {
		identity, err := verifier.Verify(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Subject != "apple-user-1" || identity.ClientID != "com.example.menu" || identity.Nonce != "login-nonce" ||
			identity.Email != "parent@example.com" || !identity.EmailVerified {
			t.Fatalf("identity = %+v", identity)
		}
	}
	if requests != 1 {
		t.Fatalf("JWKS requests = %d, want 1 cached request", requests)
	}
}

func TestAppleVerifierRejectsWrongAudienceAndMissingNonce(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	verifier, _ := NewAppleVerifier([]string{"com.example.menu"})
	verifier.now = func() time.Time { return now }
	verifier.keys = map[string]*rsa.PublicKey{"key-1": &privateKey.PublicKey}
	verifier.keysUntil = now.Add(time.Hour)

	tests := []appleClaims{
		{
			Nonce: "nonce",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer: appleIssuer, Subject: "user", Audience: jwt.ClaimStrings{"wrong"},
				IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			},
		},
		{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer: appleIssuer, Subject: "user", Audience: jwt.ClaimStrings{"com.example.menu"},
				IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			},
		},
	}
	for _, claims := range tests {
		raw := signAppleToken(t, privateKey, "key-1", claims)
		if _, err := verifier.Verify(context.Background(), raw); err == nil {
			t.Fatal("invalid Apple token should be rejected")
		}
	}
}

func TestAppleVerifierDistinguishesProviderOutage(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	verifier, _ := NewAppleVerifier([]string{"com.example.menu"})
	verifier.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("unavailable")),
		}, nil
	})}
	raw := signAppleToken(t, privateKey, "key-1", appleClaims{
		Nonce: "nonce",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: appleIssuer, Subject: "user", Audience: jwt.ClaimStrings{"com.example.menu"},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	})
	_, err = verifier.Verify(context.Background(), raw)
	if !errors.Is(err, ErrAppleUnavailable) {
		t.Fatalf("error = %v, want ErrAppleUnavailable", err)
	}
}

func signAppleToken(t *testing.T, key *rsa.PrivateKey, kid string, claims appleClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testJWKSet(t *testing.T, kid string, key *rsa.PublicKey) string {
	t.Helper()
	e := make([]byte, 4)
	binary.BigEndian.PutUint32(e, uint32(key.E))
	e = []byte(strings.TrimLeft(string(e), "\x00"))
	payload := map[string]any{"keys": []map[string]string{{
		"kid": kid,
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(e),
	}}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
