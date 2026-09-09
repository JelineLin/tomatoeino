// Package platformauth lets product services validate a platform access token
// against account-server without learning the account signing key.
package platformauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnauthorized = errors.New("platform session is invalid")
	ErrForbidden    = errors.New("platform product membership is inactive")
	ErrUnavailable  = errors.New("account service is unavailable")
)

const maxResponseBody = 1 << 20

// Client performs live session and product-membership checks through GET /v1/me.
// Deliberately not caching successful responses makes logout and account deletion
// visible to product services immediately.
type Client struct {
	meURL       string
	productCode string
	httpClient  *http.Client
}

// NewClient constructs a product-scoped account client. An http.Client may be
// injected for tests; nil selects a short production timeout.
func NewClient(baseURL, productCode string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	productCode = strings.TrimSpace(productCode)
	if baseURL == "" {
		return nil, fmt.Errorf("ACCOUNT_BASE_URL 不能为空")
	}
	if productCode == "" {
		return nil, fmt.Errorf("product code 不能为空")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("ACCOUNT_BASE_URL 必须是有效的 http(s) 地址")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("ACCOUNT_BASE_URL 不能包含凭证、查询参数或 fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/me"
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Second}
	}
	return &Client{meURL: parsed.String(), productCode: productCode, httpClient: httpClient}, nil
}

// Resolve returns the canonical platform user UUID after live session and
// membership validation. Authorization is forwarded only to account-server and
// is never included in returned errors.
func (c *Client) Resolve(ctx context.Context, authorization string) (string, error) {
	token, ok := bearerToken(authorization)
	if !ok {
		return "", ErrUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.meURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: 创建校验请求失败", ErrUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: 请求失败", ErrUnavailable)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return "", ErrUnauthorized
	case http.StatusForbidden:
		return "", ErrForbidden
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBody))
		return "", fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}

	var me struct {
		ID       string   `json:"id"`
		Products []string `json:"products"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody))
	if err := decoder.Decode(&me); err != nil {
		return "", fmt.Errorf("%w: 响应格式错误", ErrUnavailable)
	}
	userID, err := uuid.Parse(me.ID)
	if err != nil {
		return "", fmt.Errorf("%w: account-server 返回无效 user ID", ErrUnavailable)
	}
	for _, product := range me.Products {
		if product == c.productCode {
			return userID.String(), nil
		}
	}
	return "", ErrForbidden
}

func bearerToken(authorization string) (string, bool) {
	parts := strings.Fields(authorization)
	returnToken := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		returnToken = parts[1]
	}
	return returnToken, returnToken != ""
}
