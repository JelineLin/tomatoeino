package main

// token→userID 鉴权层的离线测试（多用户改造第 4 步）：
// 三级降级、注册表校验、解析、以及 fail-closed 的身份缺省行为。

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func writeUsersFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadUsers_ThreeLevels(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")

	// 级别 3：无文件无 fallback → nil（无鉴权本地模式）。
	reg, err := loadUsers(missing, "")
	if err != nil || reg != nil {
		t.Errorf("无文件无 fallback 应返回 nil 注册表，reg=%v err=%v", reg, err)
	}

	// 级别 2：无文件有 API_TOKEN → 合成单用户 home。
	reg, err = loadUsers(missing, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if uid, ok := reg.resolve("Bearer secret-token"); !ok || uid != defaultUserID {
		t.Errorf("API_TOKEN 降级模式应解析为 %s，得到 %q ok=%v", defaultUserID, uid, ok)
	}

	// 级别 1：users.json 存在 → 按表解析（每设备一条哈希）。
	// "tok-a" 与 "tok-b" 同属 wang 家；"tok-c" 属 li 家。
	p := writeUsersFile(t, `[
	  {"id":"wang","name":"老王家","token_sha256":[
	    "`+hashToken("tok-a")+`","`+hashToken("tok-b")+`"]},
	  {"id":"li","name":"小李家","token_sha256":["`+hashToken("tok-c")+`"]}
	]`)
	reg, err = loadUsers(p, "ignored-fallback")
	if err != nil {
		t.Fatal(err)
	}
	for tok, want := range map[string]string{"tok-a": "wang", "tok-b": "wang", "tok-c": "li"} {
		if uid, ok := reg.resolve("Bearer " + tok); !ok || uid != want {
			t.Errorf("token %q 应解析为 %s，得到 %q ok=%v", tok, want, uid, ok)
		}
	}
}

func TestLoadUsers_Validation(t *testing.T) {
	cases := map[string]string{
		"空表":     `[]`,
		"缺 id":   `[{"name":"x","token_sha256":["` + hashToken("t") + `"]}]`,
		"无钥匙":    `[{"id":"a","name":"x","token_sha256":[]}]`,
		"哈希不合法":  `[{"id":"a","token_sha256":["abc"]}]`,
		"钥匙一钥两主": `[{"id":"a","token_sha256":["` + hashToken("t") + `"]},{"id":"b","token_sha256":["` + hashToken("t") + `"]}]`,
	}
	for name, content := range cases {
		if _, err := loadUsers(writeUsersFile(t, content), ""); err == nil {
			t.Errorf("%s：应报错拒绝启动（配置错了宁可不跑）", name)
		}
	}
}

func TestResolve_Rejections(t *testing.T) {
	reg, _ := loadUsers(filepath.Join(t.TempDir(), "nope.json"), "good")
	for _, authz := range []string{
		"",                    // 没带头
		"Bearer ",             // 空 token
		"Bearer wrong",        // 错 token
		"good",                // 没有 Bearer 前缀
		"Basic Z29vZA==",      // 错误的 scheme
		"Bearer GOOD",         // 大小写不同（token 是精确匹配）
	} {
		if uid, ok := reg.resolve(authz); ok {
			t.Errorf("authz=%q 不该通过，解析出了 %q", authz, uid)
		}
	}
}

// fail-closed：没经过 withAuth 的请求，userIDFrom 必须是空串，
// 下游（第 5 步 registry）对空 uid 报错——绝不合成幽灵租户。
func TestUserIDFrom_FailClosed(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/api/x", nil)
	if uid := userIDFrom(r); uid != "" {
		t.Errorf("未鉴权请求的 uid 应为空串，得到 %q", uid)
	}
	// 经过注入后能取回。
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyUserID{}, "wang"))
	if uid := userIDFrom(r); uid != "wang" {
		t.Errorf("注入后应取回 wang，得到 %q", uid)
	}
}
