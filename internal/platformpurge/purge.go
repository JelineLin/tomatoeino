// Package platformpurge 是账号删除的跨进程执行链：账号是平台的，业务数据却分散在
// Menu / English 各自的进程里，硬删除账户前必须先让每个产品把自己那份数据清干净。
//
// 两侧共用这一个包：account-server 用 Client 发起，产品服务用 Handler 承接。
// 鉴权、UUID 校验、幂等语义只写一遍——这是删除链上最不该两边各写一套的地方。
//
// 与 platformauth 的方向正好相反：那个是产品问账户「这人是谁」，这个是账户命令
// 产品「把这人清掉」。因此凭证也不同：这里用进程间共享密钥，不是用户的会话 token。
package platformpurge

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"tomato-platform/internal/observability"
)

// Path 是产品服务挂载清除接口的路径前缀，后面直接跟平台 user UUID。
// 两侧引用同一个常量，改一处就不会漏改另一处。
const Path = "/internal/v1/users/"

// ErrPurgeFailed 表示这次清除没做成——调用方必须重试，绝不能当成删干净了。
var ErrPurgeFailed = errors.New("产品数据清除失败")

const maxResponseBody = 1 << 16

// Client 是 account-server 侧的发起端，一个产品一个实例。
type Client struct {
	product    string
	prefixURL  string
	secret     string
	httpClient *http.Client
}

// NewClient 组装某个产品的清除客户端。secret 是进程间共享密钥（PLATFORM_INTERNAL_TOKEN），
// 与用户会话无关：删除任务跑在后台，那时用户的会话早已撤销。
func NewClient(product, baseURL, secret string, httpClient *http.Client) (*Client, error) {
	product = strings.TrimSpace(product)
	baseURL = strings.TrimSpace(baseURL)
	if product == "" {
		return nil, fmt.Errorf("product code 不能为空")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("产品 base URL 不能为空")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("PLATFORM_INTERNAL_TOKEN 不能为空")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("产品 base URL 必须是有效的 http(s) 地址")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("产品 base URL 不能包含凭证、查询参数或 fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + Path
	if httpClient == nil {
		// 比 platformauth 宽松得多：清除是后台任务，宁可等也不要半途而废，
		// 失败了还要走一整轮退避重试。
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{product: product, prefixURL: parsed.String(), secret: secret, httpClient: httpClient}, nil
}

// Product 返回产品码，用于删除任务的日志与备注。
func (c *Client) Product() string { return c.product }

// Purge 让产品清掉这个用户的全部业务数据。
//
// 只有 204 才算清干净——这条严格是有来由的：产品服务同时托管着 SPA，
// 一旦对方没挂载清除路由（比如漏配 PLATFORM_INTERNAL_TOKEN），请求会掉进
// SPA 兜底，拿到一个 200 + index.html。把 200 或 404 当成功，就会「以为删了」
// 然后硬删账户，业务数据从此成为无人认领的孤儿。宁可一直重试、把配置问题吵出来。
func (c *Client) Purge(ctx context.Context, userID string) error {
	if !canonicalUUID(userID) {
		return fmt.Errorf("%w: %s 收到非规范 UUID", ErrPurgeFailed, c.product)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.prefixURL+userID, nil)
	if err != nil {
		return fmt.Errorf("%w: %s 创建清除请求失败", ErrPurgeFailed, c.product)
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	if requestID := observability.RequestID(ctx); requestID != "" {
		request.Header.Set(observability.RequestIDHeader, requestID)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %s 不可达", ErrPurgeFailed, c.product)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBody))

	if response.StatusCode != http.StatusNoContent {
		// 401/403 是密钥配错，404/200 多半是路由没挂载——都必须当失败。
		// 「这个产品从来没有该用户的数据」由产品侧的幂等实现返回 204，不靠状态码猜。
		return fmt.Errorf("%w: %s 返回 HTTP %d", ErrPurgeFailed, c.product, response.StatusCode)
	}
	return nil
}

// Handler 是产品服务侧的承接端：校验共享密钥和 UUID，然后把清除动作交给 purge。
// purge 必须自己保证幂等（用户不存在也返回 nil），account-server 会重试。
func Handler(secret string, purge func(context.Context, string) error) (http.Handler, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("PLATFORM_INTERNAL_TOKEN 不能为空")
	}
	if purge == nil {
		return nil, fmt.Errorf("purge 函数不能为空")
	}
	expected := sha256.Sum256([]byte(secret))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先验密钥再看别的：不给未授权者任何探测路径存在与否的余地。
		if !secretMatches(expected, r.Header.Get("Authorization")) {
			http.Error(w, "未授权", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", http.MethodDelete)
			http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
			return
		}
		userID := strings.TrimPrefix(r.URL.Path, Path)
		if !canonicalUUID(userID) {
			http.Error(w, "user ID 必须是规范 UUID", http.StatusBadRequest)
			return
		}
		observability.SetUserID(r.Context(), userID)
		if err := purge(r.Context(), userID); err != nil {
			// 报错就让 account-server 重试；这里绝不能返回 2xx。
			http.Error(w, "清除失败", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), nil
}

// secretMatches 比对共享密钥。先各自 sha256 再定长比较：比较的是等长摘要，
// 既不泄露密钥长度，也不会在第几个字节不同上泄露时序（与 users.json 的哈希查表同口径）。
func secretMatches(expected [sha256.Size]byte, authorization string) bool {
	parts := strings.Fields(authorization)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	presented := sha256.Sum256([]byte(parts[1]))
	return subtle.ConstantTimeCompare(presented[:], expected[:]) == 1
}

func canonicalUUID(s string) bool {
	parsed, err := uuid.Parse(s)
	return err == nil && parsed.String() == s
}
