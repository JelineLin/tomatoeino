package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

const RequestIDHeader = "X-Request-ID"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)

// HTTPMiddleware assigns or validates a request ID and writes one privacy-safe
// access event per request. Query strings and request bodies are intentionally absent.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(requestID) {
			requestID = newRequestID()
		}
		ctx := WithRequestID(r.Context(), requestID)
		r = r.WithContext(ctx)
		w.Header().Set(RequestIDHeader, requestID)
		recorder := &responseRecorder{ResponseWriter: w}
		started := time.Now()

		defer func() {
			if recovered := recover(); recovered != nil {
				if recorder.status == 0 {
					recorder.status = http.StatusInternalServerError
				}
				slog.ErrorContext(ctx, "http panic", "panic_type", fmt.Sprintf("%T", recovered))
				writeAccessLog(ctx, r, recorder, started)
				panic(recovered)
			}
			writeAccessLog(ctx, r, recorder, started)
		}()
		next.ServeHTTP(recorder, r)
	})
}

func writeAccessLog(ctx context.Context, r *http.Request, recorder *responseRecorder, started time.Time) {
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < http.StatusBadRequest && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") {
		return
	}
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"duration_ms", time.Since(started).Milliseconds(),
		"response_bytes", recorder.bytes,
	}
	switch {
	case status >= http.StatusInternalServerError:
		slog.ErrorContext(ctx, "http request", attrs...)
	case status >= http.StatusBadRequest:
		slog.WarnContext(ctx, "http request", attrs...)
	default:
		slog.InfoContext(ctx, "http request", attrs...)
	}
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	// Entropy failures should be practically unreachable, but preserve the
	// validation contract so downstream services can safely forward the value.
	return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}

func (w *responseRecorder) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
