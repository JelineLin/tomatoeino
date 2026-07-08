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
	return newRegistry(t.TempDir(), users, stubEmbedder{}, stubChatModel{})
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
