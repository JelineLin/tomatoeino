package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"

	"tomato-platform/internal/observability"
	"tomato-platform/internal/platformauth"
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

type accountIdentityResolver interface {
	Resolve(context.Context, string) (string, error)
}

// newAccountResolver 按 ACCOUNT_BASE_URL 决定是否启用统一账户；没配就是接口零值。
// 返回接口而不是 *platformauth.Client：nil 的具体指针装进接口后接口不等于 nil，
// withAuth 会误以为账户服务在线，第一个错 token 就空指针崩掉。
func newAccountResolver(baseURL, productCode string) (accountIdentityResolver, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, nil
	}
	client, err := platformauth.NewClient(baseURL, productCode, nil)
	if err != nil {
		return nil, err
	}
	return client, nil
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

func withAuth(users *userRegistry, accounts accountIdentityResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/english/") {
			next.ServeHTTP(w, r)
			return
		}
		id := defaultUserID
		if users != nil || accounts != nil {
			authorization := r.Header.Get("Authorization")
			legacyID, legacyOK := "", false
			if users != nil {
				legacyID, legacyOK = users.resolve(authorization)
			}
			if legacyOK {
				id = legacyID
			} else if accounts != nil {
				var err error
				id, err = accounts.Resolve(r.Context(), authorization)
				switch {
				case err == nil:
					// 这个 id 会拼进 audioDir/<id>/… 的落盘路径。上游只会返回规范 UUID，
					// 但路径拼接的闸门必须守在自己这边——上游哪天变了，这里先炸。
					if !safeUserID.MatchString(id) {
						slog.ErrorContext(r.Context(), "account-server returned unsafe user ID")
						http.Error(w, "统一账户返回了非法用户标识", http.StatusBadGateway)
						return
					}
				case errors.Is(err, platformauth.ErrForbidden):
					http.Error(w, "当前账户未开通或已停用 English Coach", http.StatusForbidden)
					return
				case errors.Is(err, platformauth.ErrUnavailable):
					http.Error(w, "统一账户服务暂时不可用", http.StatusServiceUnavailable)
					return
				default:
					w.Header().Set("WWW-Authenticate", `Bearer realm="englishcoach"`)
					http.Error(w, "登录会话无效", http.StatusUnauthorized)
					return
				}
			} else {
				w.Header().Set("WWW-Authenticate", `Bearer realm="englishcoach"`)
				http.Error(w, "访问码无效", http.StatusUnauthorized)
				return
			}
		}
		ctx := context.WithValue(r.Context(), userKey{}, id)
		observability.SetUserID(ctx, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
