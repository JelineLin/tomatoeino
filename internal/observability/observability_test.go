package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMiddlewareCorrelatesStructuredAccessLog(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(NewLogger("test-service", &output))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) == "" {
			t.Error("request ID 未写入 context")
		}
		SetUserID(r.Context(), "user-123")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/items?token=must-not-log", nil)
	request.Header.Set("Authorization", "Bearer must-not-log")
	request.Header.Set(RequestIDHeader, "bad request id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	requestID := response.Header().Get(RequestIDHeader)
	if len(requestID) != 32 || requestID == "bad request id" {
		t.Fatalf("生成的 request ID = %q", requestID)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatalf("日志不是 JSON: %v\n%s", err, output.String())
	}
	for key, want := range map[string]any{
		"service": "test-service", "request_id": requestID, "user_id": "user-123",
		"method": http.MethodPost, "path": "/v1/items", "status": float64(http.StatusCreated),
	} {
		if event[key] != want {
			t.Errorf("%s = %#v, want %#v", key, event[key], want)
		}
	}
	if strings.Contains(output.String(), "must-not-log") {
		t.Fatalf("日志泄露了 query 或 Authorization: %s", output.String())
	}
}

func TestHTTPMiddlewarePreservesValidRequestIDAndFlusher(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Fatal("SSE handler 看不到 http.Flusher")
		}
		w.(http.Flusher).Flush()
	}))
	request := httptest.NewRequest(http.MethodGet, "/stream", nil)
	request.Header.Set(RequestIDHeader, "upstream-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get(RequestIDHeader); got != "upstream-123" {
		t.Fatalf("request ID = %q", got)
	}
}

func TestContextHandlerAddsCorrelationToDomainLog(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	var output bytes.Buffer
	logger := NewLogger("worker", &output)
	ctx := WithRequestID(context.Background(), "request-1")
	SetUserID(ctx, "user-1")
	logger.InfoContext(ctx, "done")
	if !strings.Contains(output.String(), `"request_id":"request-1"`) ||
		!strings.Contains(output.String(), `"user_id":"user-1"`) {
		t.Fatalf("correlation fields missing: %s", output.String())
	}
}

func TestDefaultLoggerBridgesLegacyLogCallsIntoJSON(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(NewLogger("legacy-bridge", &output))
	t.Cleanup(func() { slog.SetDefault(previous) })
	log.Print("startup ready")

	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatalf("legacy log 不是 JSON: %v\n%s", err, output.String())
	}
	if event["service"] != "legacy-bridge" || event["msg"] != "startup ready" {
		t.Fatalf("legacy log fields = %#v", event)
	}
}
