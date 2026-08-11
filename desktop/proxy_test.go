package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateAPIBaseURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https", raw: "https://english.example.com"},
		{name: "local http", raw: "http://127.0.0.1:8450/"},
		{name: "empty", raw: "", wantErr: true},
		{name: "relative", raw: "english.example.com", wantErr: true},
		{name: "unsupported scheme", raw: "file:///tmp/backend", wantErr: true},
		{name: "credentials", raw: "https://user:secret@english.example.com", wantErr: true},
		{name: "path prefix", raw: "https://english.example.com/root", wantErr: true},
		{name: "query", raw: "https://english.example.com?tenant=home", wantErr: true},
		{name: "fragment", raw: "https://english.example.com#api", wantErr: true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateAPIBaseURL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAPIBaseURL(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

func TestAPIProxyForwardsPathQueryHeadersAndBody(t *testing.T) {
	t.Parallel()
	type observedRequest struct {
		method        string
		path          string
		rawQuery      string
		authorization string
		body          string
		hopHeader     string
	}
	observed := make(chan observedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		observed <- observedRequest{
			method:        r.Method,
			path:          r.URL.Path,
			rawQuery:      r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"),
			body:          string(body),
			hopHeader:     r.Header.Get("X-Desktop-Hop"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	handler, err := newAPIProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/english/answers?source=desktop", strings.NewReader(`{"lesson_id":7}`))
	req.Header.Set("Authorization", "Bearer same-user-token")
	req.Header.Set("Connection", "X-Desktop-Hop")
	req.Header.Set("X-Desktop-Hop", "must-be-removed")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	got := <-observed
	if got.method != http.MethodPost || got.path != "/api/english/answers" || got.rawQuery != "source=desktop" {
		t.Fatalf("unexpected upstream request: %+v", got)
	}
	if got.authorization != "Bearer same-user-token" || got.body != `{"lesson_id":7}` {
		t.Fatalf("auth/body not preserved: %+v", got)
	}
	if got.hopHeader != "" {
		t.Fatalf("hop-by-hop header leaked upstream: %q", got.hopHeader)
	}
}

func TestAPIProxyRejectsNonEnglishRoutes(t *testing.T) {
	t.Parallel()
	handler, err := newAPIProxy("https://english.example.com")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/chat", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestAPIProxyReturnsJSONBadGateway(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := upstream.URL
	upstream.Close()
	handler, err := newAPIProxy(target)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/english/profile", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", got)
	}
}
