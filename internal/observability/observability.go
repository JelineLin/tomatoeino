// Package observability provides one structured logging and correlation contract
// for every tomato-platform process. It deliberately keeps tokens, request bodies,
// email addresses and raw network addresses out of the default log fields.
package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

type scopeKey struct{}

type requestScope struct {
	mu        sync.RWMutex
	requestID string
	userID    string
}

func (s *requestScope) values() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requestID, s.userID
}

// Configure installs the process-wide logger. JSON is the production-safe default;
// LOG_FORMAT=text remains available for local terminals and LOG_LEVEL controls volume.
func Configure(service string) *slog.Logger {
	logger := NewLogger(service, os.Stdout)
	slog.SetDefault(logger)
	return logger
}

func NewLogger(service string, output io.Writer) *slog.Logger {
	service = strings.TrimSpace(service)
	if service == "" {
		service = "tomato-platform"
	}
	options := &slog.HandlerOptions{Level: configuredLevel(os.Getenv("LOG_LEVEL"))}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LOG_FORMAT")), "text") {
		handler = slog.NewTextHandler(output, options)
	} else {
		handler = slog.NewJSONHandler(output, options)
	}
	handler = contextHandler{Handler: handler.WithAttrs([]slog.Attr{slog.String("service", service)})}
	return slog.New(handler)
}

func configuredLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithRequestID starts a correlation scope. Background workers use this to carry
// the original HTTP request ID after the request itself has completed.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, scopeKey{}, &requestScope{requestID: strings.TrimSpace(requestID)})
}

func RequestID(ctx context.Context) string {
	if scope := scopeFrom(ctx); scope != nil {
		requestID, _ := scope.values()
		return requestID
	}
	return ""
}

// WithUserID creates a derived scope for background work that did not pass through
// HTTP authentication, while preserving any existing request ID.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, scopeKey{}, &requestScope{
		requestID: RequestID(ctx),
		userID:    strings.TrimSpace(userID),
	})
}

// SetUserID enriches the current request scope after authentication. The scope is
// mutable so the outer access logger can observe the identity resolved downstream.
func SetUserID(ctx context.Context, userID string) {
	if scope := scopeFrom(ctx); scope != nil {
		scope.mu.Lock()
		scope.userID = strings.TrimSpace(userID)
		scope.mu.Unlock()
	}
}

func scopeFrom(ctx context.Context) *requestScope {
	if ctx == nil {
		return nil
	}
	scope, _ := ctx.Value(scopeKey{}).(*requestScope)
	return scope
}

// contextHandler adds correlation fields without requiring every call site to
// repeat them. Empty identities are omitted, including unauthenticated failures.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if scope := scopeFrom(ctx); scope != nil {
		requestID, userID := scope.values()
		if requestID != "" {
			record.AddAttrs(slog.String("request_id", requestID))
		}
		if userID != "" {
			record.AddAttrs(slog.String("user_id", userID))
		}
	}
	return h.Handler.Handle(ctx, record)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}
