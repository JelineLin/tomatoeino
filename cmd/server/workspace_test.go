package main

// workspace 注册表的离线测试（多用户改造第 5 步）：
// fail-closed 契约（未知 uid 必须报错，绝不合成幽灵租户）、懒加载缓存、隔离。
// 用假 embedder + 假 chat model 构图，全程不联网：新用户空历史 → 零 embedding 调用。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type stubEmbedder struct{}

func (stubEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = []float64{1}
	}
	return out, nil
}

// stubChatModel 只为离线构图存在——测试里永远不会真的跑对话。
type stubChatModel struct{}

func (stubChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("stub", nil), nil
}

func (stubChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("stub", nil)}), nil
}

func (m stubChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func testRegistry(t *testing.T, users *userRegistry) *registry {
	t.Helper()
	return newRegistry(t.TempDir(), users, false, stubEmbedder{}, stubChatModel{})
}

func TestRegistry_FailClosed(t *testing.T) {
	ctx := context.Background()
	p := writeUsersFile(t, `[{"id":"wang","name":"老王家","token_sha256":["`+hashToken("t")+`"]}]`)
	users, err := loadUsers(p, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := testRegistry(t, users)

	// 空 uid（绕过了鉴权中间件）→ 报错。
	if _, err := reg.get(ctx, ""); err == nil {
		t.Error("空 uid 必须报错——绝不合成幽灵租户")
	}
	// 未注册 uid → 报错。
	if _, err := reg.get(ctx, "ghost"); err == nil {
		t.Error("未注册 uid 必须报错")
	}
	// 合法 uid → 正常构建。
	if _, err := reg.get(ctx, "wang"); err != nil {
		t.Errorf("合法 uid 应能构建: %v", err)
	}
}

func TestRegistry_AcceptsOnlyCanonicalPlatformUUIDs(t *testing.T) {
	reg := newRegistry(t.TempDir(), nil, true, stubEmbedder{}, stubChatModel{})
	if _, err := reg.get(context.Background(), "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"); err != nil {
		t.Fatalf("已认证的平台 UUID 应可建 workspace: %v", err)
	}
	for _, id := range []string{"home", "../../escape", "C733A5D7-7B65-49AC-B6D2-872FD57A4CE6"} {
		if _, err := reg.get(context.Background(), id); err == nil {
			t.Errorf("平台模式不应接受非规范 ID %q", id)
		}
	}
}

// 平台用户没有 users.json 那样的本地名册，重启后名册只剩磁盘目录一份。
// 这个测试盯住的是「凌晨重启 → 07:00 简报漏人」这条链路。
func TestRegistry_PlatformRosterSurvivesRestart(t *testing.T) {
	const alive = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"
	dataDir := t.TempDir()
	for _, name := range []string{alive, "C733A5D7-7B65-49AC-B6D2-872FD57A4CE7", "home", "not-a-uuid"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "users", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dataDir, "users", "stray.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := newRegistry(dataDir, nil, true, stubEmbedder{}, stubChatModel{}) // 全新进程：注册表是空的
	got := map[string]bool{}
	for _, uid := range reg.allUIDs() {
		got[uid] = true
	}
	if !got[alive] {
		t.Errorf("重启后应从磁盘认出平台用户 %s，得到 %v", alive, reg.allUIDs())
	}
	if len(got) != 1 {
		t.Errorf("只有规范 UUID 目录算平台用户，得到 %v", reg.allUIDs())
	}

	// 旧用户那条线仍走 users.json，不因平台模式而丢。
	p := writeUsersFile(t, `[{"id":"wang","name":"老王家","token_sha256":["`+hashToken("t")+`"]}]`)
	users, err := loadUsers(p, "")
	if err != nil {
		t.Fatal(err)
	}
	mixed := newRegistry(dataDir, users, true, stubEmbedder{}, stubChatModel{})
	got = map[string]bool{}
	for _, uid := range mixed.allUIDs() {
		got[uid] = true
	}
	if !got[alive] || !got["wang"] || len(got) != 2 {
		t.Errorf("平台用户与旧用户应取并集，得到 %v", mixed.allUIDs())
	}
}

func TestRegistry_NilUsersOnlyDefault(t *testing.T) {
	ctx := context.Background()
	reg := testRegistry(t, nil) // 无鉴权本地模式

	if _, err := reg.get(ctx, defaultUserID); err != nil {
		t.Errorf("本地模式 %s 应可用: %v", defaultUserID, err)
	}
	if _, err := reg.get(ctx, "wang"); err == nil {
		t.Error("本地模式除 home 外一律拒绝")
	}
}

func TestRegistry_CacheAndIsolation(t *testing.T) {
	ctx := context.Background()
	p := writeUsersFile(t, `[
	  {"id":"wang","name":"w","token_sha256":["`+hashToken("a")+`"]},
	  {"id":"li","name":"l","token_sha256":["`+hashToken("b")+`"]}
	]`)
	users, _ := loadUsers(p, "")
	reg := testRegistry(t, users)

	w1, err := reg.get(ctx, "wang")
	if err != nil {
		t.Fatal(err)
	}
	w2, _ := reg.get(ctx, "wang")
	if w1 != w2 {
		t.Error("同一用户两次 get 应命中缓存、返回同一 workspace")
	}

	l1, _ := reg.get(ctx, "li")
	if l1 == w1 {
		t.Error("不同用户必须是不同 workspace")
	}
	// 隔离实测：wang 记一笔库存，li 看不见。
	if _, err := w1.inv.Add("鳕鱼", 2, "块"); err != nil {
		t.Fatal(err)
	}
	if got := l1.inv.List(""); len(got) != 0 {
		t.Errorf("li 的库存应为空，却看到了 %v", got)
	}
	if got := w1.inv.List(""); len(got) != 1 {
		t.Errorf("wang 的库存应有 1 条，实际 %d", len(got))
	}
	// 数据落在各自的用户目录（文件级隔离即租户隔离）。
	if _, err := os.Stat(filepath.Join(reg.dataDir, "users", "wang", "inventory.json")); err != nil {
		t.Errorf("wang 的库存文件应存在于自己的目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(reg.dataDir, "users", "li", "inventory.json")); err == nil {
		t.Error("li 没记过账，不该有库存文件")
	}
}
