package platformauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"tomato-platform/internal/observability"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestResolveValidatesSessionAndProduct(t *testing.T) {
	const userID = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"
	client, err := NewClient("https://account.example.test/platform/", "menu", &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://account.example.test/platform/v1/me" {
				t.Fatalf("unexpected URL: %s", request.URL)
			}
			if request.Header.Get("Authorization") != "Bearer access-token" {
				t.Fatalf("token was not normalized")
			}
			if request.Header.Get(observability.RequestIDHeader) != "request-123" {
				t.Fatalf("request ID 未传给 account-server")
			}
			return response(http.StatusOK, `{"id":"`+userID+`","products":["english","menu"]}`), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.WithRequestID(context.Background(), "request-123")
	got, err := client.Resolve(ctx, "bearer access-token")
	if err != nil || got != userID {
		t.Fatalf("Resolve() = %q, %v", got, err)
	}
}

func TestResolveFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "invalid session", status: http.StatusUnauthorized, want: ErrUnauthorized},
		{name: "account forbidden", status: http.StatusForbidden, want: ErrForbidden},
		{name: "missing product", status: http.StatusOK, body: `{"id":"c733a5d7-7b65-49ac-b6d2-872fd57a4ce6","products":["english"]}`, want: ErrForbidden},
		{name: "bad user id", status: http.StatusOK, body: `{"id":"../../escape","products":["menu"]}`, want: ErrUnavailable},
		{name: "server error", status: http.StatusInternalServerError, want: ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient("https://account.example.test", "menu", &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(test.status, test.body), nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Resolve(context.Background(), "Bearer secret-value"); !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			} else if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("error leaked bearer token")
			}
		})
	}
}

func TestResolveRejectsMalformedAuthorizationWithoutRequest(t *testing.T) {
	called := false
	client, err := NewClient("https://account.example.test", "menu", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("unexpected")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Resolve(context.Background(), "Basic abc"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Resolve() error = %v", err)
	}
	if called {
		t.Fatal("malformed credentials should not reach account-server")
	}
}
