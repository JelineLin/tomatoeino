package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const defaultUserID = "home"

var safeUserID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type userRecord struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TokenSHA256 []string `json:"token_sha256"`
}
type userRegistry struct {
	byHash map[string]string
	names  map[string]string
}

func loadUsers(path, fallbackToken string) (*userRegistry, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if fallbackToken == "" {
			return nil, nil
		}
		return &userRegistry{byHash: map[string]string{hashToken(fallbackToken): defaultUserID}, names: map[string]string{defaultUserID: "默认用户"}}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []userRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	r := &userRegistry{byHash: map[string]string{}, names: map[string]string{}}
	for _, u := range records {
		if !safeUserID.MatchString(u.ID) {
			return nil, fmt.Errorf("用户 ID %q 不安全", u.ID)
		}
		r.names[u.ID] = u.Name
		for _, h := range u.TokenSHA256 {
			h = strings.ToLower(strings.TrimSpace(h))
			if len(h) != 64 {
				return nil, fmt.Errorf("用户 %s 的 token 哈希无效", u.ID)
			}
			if _, ok := r.byHash[h]; ok {
				return nil, fmt.Errorf("token 哈希重复")
			}
			r.byHash[h] = u.ID
		}
	}
	return r, nil
}

func hashToken(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func (r *userRegistry) resolve(auth string) (string, bool) {
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == auth || token == "" {
		return "", false
	}
	id, ok := r.byHash[hashToken(token)]
	return id, ok
}

type userKey struct{}

func userIDFrom(r *http.Request) string { id, _ := r.Context().Value(userKey{}).(string); return id }

func withAuth(users *userRegistry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/english/") {
			next.ServeHTTP(w, r)
			return
		}
		id := defaultUserID
		if users != nil {
			var ok bool
			id, ok = users.resolve(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="englishcoach"`)
				http.Error(w, "访问码无效", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, id)))
	})
}
