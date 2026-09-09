package account

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAppleOAuthExchangeAndRevoke(t *testing.T) {
	client, publicKey := testAppleOAuthClient(t)
	var endpoints []string
	client.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		endpoints = append(endpoints, req.URL.Path)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		assertAppleClientSecret(t, form.Get("client_secret"), publicKey, "com.example.menu")
		switch req.URL.Path {
		case "/auth/token":
			if form.Get("grant_type") != "authorization_code" || form.Get("code") != "one-time-code" {
				t.Fatalf("exchange form = %v", form)
			}
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"apple-access","refresh_token":"apple-refresh","id_token":"apple-id"}`), nil
		case "/auth/revoke":
			if form.Get("token_type_hint") != "refresh_token" || form.Get("token") != "apple-refresh" {
				t.Fatalf("revoke form = %v", form)
			}
			return jsonHTTPResponse(http.StatusOK, ``), nil
		default:
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
			return nil, nil
		}
	})}
	client.tokenURL = "https://apple.test/auth/token"
	client.revokeURL = "https://apple.test/auth/revoke"

	tokens, err := client.ExchangeCode(context.Background(), "com.example.menu", "one-time-code", "")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken != "apple-refresh" || tokens.IdentityToken != "apple-id" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if err := client.Revoke(context.Background(), "com.example.menu", tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if strings.Join(endpoints, ",") != "/auth/token,/auth/revoke" {
		t.Fatalf("endpoints = %v", endpoints)
	}
}

func TestAppleOAuthClassifiesErrors(t *testing.T) {
	client, _ := testAppleOAuthClient(t)
	client.tokenURL = "https://apple.test/auth/token"
	status := http.StatusBadRequest
	client.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(status, `{"error":"invalid_grant"}`), nil
	})}
	_, err := client.ExchangeCode(context.Background(), "com.example.menu", "bad", "")
	if !errors.Is(err, ErrInvalidAppleAuthorization) {
		t.Fatalf("400 error = %v", err)
	}
	status = http.StatusServiceUnavailable
	_, err = client.ExchangeCode(context.Background(), "com.example.menu", "bad", "")
	if !errors.Is(err, ErrAppleUnavailable) {
		t.Fatalf("503 error = %v", err)
	}
	status = http.StatusBadRequest
	client.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(status, `{"error":"invalid_client"}`), nil
	})}
	_, err = client.ExchangeCode(context.Background(), "com.example.menu", "bad", "")
	if !errors.Is(err, ErrAppleUnavailable) {
		t.Fatalf("invalid_client error = %v", err)
	}
}

func testAppleOAuthClient(t *testing.T) (*AppleOAuthClient, *ecdsa.PublicKey) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	client, err := NewAppleOAuthClient("TEAM123", "KEY123", pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	return client, &privateKey.PublicKey
}

func assertAppleClientSecret(t *testing.T, raw string, key *ecdsa.PublicKey, clientID string) {
	t.Helper()
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Header["kid"] != "KEY123" {
			t.Fatalf("kid = %v", token.Header["kid"])
		}
		return key, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer("TEAM123"), jwt.WithAudience(appleIssuer), jwt.WithSubject(clientID),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC) }),
	)
	if err != nil || !token.Valid {
		t.Fatalf("client secret invalid: %v", err)
	}
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
