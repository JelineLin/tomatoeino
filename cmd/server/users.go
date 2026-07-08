// users.go —— 多用户改造第 4 步：token → userID 的身份解析。
//
// 设计（评审定稿 + 嫁接 B 方案的哈希做法）：
//   - data/users.json（gitignored）是用户注册表，每个用户（家庭）一条，
//     token 存 sha256 哈希数组——每台设备一条哈希，吊销某台手机只删一条、不动全家；
//     注册表泄露也拿不到可用 token（哈希不可逆）。
//   - resolve 时把来访 token 先 sha256 再查 map：哈希后查表本身就是业界标准的
//     抗时序做法（比较的是哈希、不是密钥原文，且 map 查找不泄露前缀匹配长度）。
//
// 三级兼容降级（现有部署和 iOS 零改动）：
//   1. users.json 存在 → 多用户模式，按表解析；
//   2. 没有 users.json 但设了 API_TOKEN → 合成单用户 "home"（哈希它）；
//   3. 两者都没有 → 无鉴权本地模式，所有请求都算 "home"，启动时醒目告警。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// defaultUserID 是单用户降级模式下的固定身份，也是现有数据迁移的归属。
const defaultUserID = "home"

// userRecord 是 users.json 里的一条。
type userRecord struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TokenSHA256 []string `json:"token_sha256"`
}

// userRegistry 是启动时建好的 token 哈希反查表。nil 表示无鉴权本地模式。
type userRegistry struct {
	byHash map[string]string // sha256(token) 的 hex → userID
	names  map[string]string // userID → 展示名（日志用）
}

// loadUsers 按三级降级构造注册表。
func loadUsers(path, fallbackToken string) (*userRegistry, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if fallbackToken == "" {
			return nil, nil // 级别 3：无鉴权本地模式
		}
		// 级别 2：API_TOKEN 单用户模式——把它哈希进表，行为与多用户完全同构。
		return &userRegistry{
			byHash: map[string]string{hashToken(fallbackToken): defaultUserID},
			names:  map[string]string{defaultUserID: "默认家庭"},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取用户注册表 %s 失败: %w", path, err)
	}

	var records []userRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("解析用户注册表 %s 失败: %w", path, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("用户注册表 %s 是空的——要么删掉它走降级模式，要么至少配一个用户", path)
	}

	reg := &userRegistry{byHash: map[string]string{}, names: map[string]string{}}
	for _, r := range records {
		if r.ID == "" {
			return nil, fmt.Errorf("用户注册表里有条目缺 id")
		}
		if len(r.TokenSHA256) == 0 {
			return nil, fmt.Errorf("用户 %s 没有任何 token_sha256——没有钥匙的门等于焊死", r.ID)
		}
		reg.names[r.ID] = r.Name
		for _, h := range r.TokenSHA256 {
			h = strings.ToLower(strings.TrimSpace(h))
			if len(h) != 64 {
				return nil, fmt.Errorf("用户 %s 的 token_sha256 %q 不是 64 位 hex（用 echo -n <token> | shasum -a 256 生成）", r.ID, h)
			}
			if owner, dup := reg.byHash[h]; dup {
				return nil, fmt.Errorf("同一个 token 哈希同时属于 %s 和 %s——一把钥匙只能开一扇门", owner, r.ID)
			}
			reg.byHash[h] = r.ID
		}
	}
	return reg, nil
}

// resolve 从 Authorization 头解析出 userID。
func (u *userRegistry) resolve(authz string) (string, bool) {
	tok := strings.TrimPrefix(authz, "Bearer ")
	if tok == authz || tok == "" { // 没有 Bearer 前缀或空 token
		return "", false
	}
	uid, ok := u.byHash[hashToken(tok)]
	return uid, ok
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// ---- 请求上下文里的用户身份 ----

type ctxKeyUserID struct{}

// userIDFrom 取出鉴权中间件放进请求 ctx 的 userID。
// 拿不到就是编程错误（某个 handler 绕过了 withAuth 挂载）——fail-closed，返回空串，
// 下游（第 5 步的 registry）对空 uid 必须报错，绝不合成幽灵租户。
func userIDFrom(r *http.Request) string {
	uid, _ := r.Context().Value(ctxKeyUserID{}).(string)
	return uid
}
