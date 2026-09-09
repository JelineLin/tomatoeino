package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"tomato-platform/internal/account"
)

const maxJSONBody = 1 << 20

func (s *server) handleAppleChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var request struct {
		ProductCode string `json:"product_code"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	challenge, err := s.account.NewAppleChallenge(r.Context(), request.ProductCode)
	if err != nil {
		s.handleAccountError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, challenge)
}

func (s *server) handleAppleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var request account.LoginInput
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	tokens, err := s.account.LoginApple(r.Context(), request)
	if err != nil {
		s.handleAccountError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, tokens)
}

func (s *server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var request struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	tokens, err := s.account.Refresh(r.Context(), request.RefreshToken)
	if err != nil {
		s.handleAccountError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, tokens)
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		session, err := s.account.Authenticate(r.Context(), bearerToken(r))
		if err != nil {
			s.handleAccountError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, session.User)
	case http.MethodDelete:
		job, err := s.account.RequestDeletion(r.Context(), bearerToken(r))
		if err != nil {
			s.handleAccountError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
	}
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := s.account.Logout(r.Context(), bearerToken(r)); err != nil {
		s.handleAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, account.ErrInvalidInput):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求参数不正确")
	case errors.Is(err, account.ErrProductUnavailable), errors.Is(err, account.ErrAccountUnavailable):
		writeAPIError(w, http.StatusForbidden, "access_denied", "账号或产品当前不可用")
	case errors.Is(err, account.ErrAppleUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "identity_provider_unavailable", "Apple 登录服务暂时不可用")
	case account.IsAuthenticationError(err):
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "登录凭证或会话无效")
	default:
		log.Printf("account API error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务暂时不可用")
	}
}

func bearerToken(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"code": code, "message": message,
	}})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
}
