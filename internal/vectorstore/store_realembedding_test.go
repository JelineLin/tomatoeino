package vectorstore_test

import (
	"context"
	"os"
	"testing"

	"github.com/cloudwego/eino/schema"

	"tomatoeino/internal/llm"
	"tomatoeino/internal/vectorstore"
)

// TestRealEmbedding 用「真」OpenAI embedding 跑一遍同一个 Store，
// 直观对比它和 L1 假 embedder 的差别。
//
// 跑法：
//
//	go test -v -run RealEmbedding ./internal/vectorstore/
//
// 没配 OPENAI_API_KEY 时自动跳过（CI / 没 key 的人不会被卡）。
// 会真实调用 embedding 接口、花一点点钱（几条文本，几乎可忽略）。
//
// 看点：查询词「给宝宝补充蛋白质的荤菜」和菜名「蒸排骨」「煎三文鱼」
// 在字面上一个字都不重合——假 embedder 会全判 0 分，
// 真 embedder 却能把这些「肉/鱼」类菜排到前面，把「西兰花」「番茄意面」压下去。
// 这就是语义检索相对关键词匹配的价值。
func TestRealEmbedding(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("未设置 OPENAI_API_KEY，跳过真实 embedding 测试")
	}

	ctx := context.Background()

	emb, err := llm.NewEmbedder(ctx)
	if err != nil {
		t.Fatalf("创建 Embedder 失败: %v", err)
	}

	// 注意：Store 完全没变，只是把假 embedder 换成了真的——这就是面向接口的回报。
	store := vectorstore.New(emb)

	docs := []*schema.Document{
		{ID: "排骨", Content: "蒸排骨配玉米土豆，姜蒜腌过"},
		{ID: "三文鱼", Content: "煎三文鱼"},
		{ID: "鳕鱼", Content: "清蒸鳕鱼，挤柠檬腌过"},
		{ID: "西兰花", Content: "白水西兰花，加点点薄盐"},
		{ID: "意面", Content: "番茄酱意面，煮软一点"},
	}
	if err := store.Add(ctx, docs); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}

	query := "给宝宝补充蛋白质的荤菜"
	got, err := store.Retrieve(ctx, query)
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	t.Logf("查询「%s」的检索结果（按相似度降序）：", query)
	for i, d := range got {
		t.Logf("  #%d  score=%.4f  [%s] %s", i+1, d.Score(), d.ID, d.Content)
	}

	// 软断言：荤菜（排骨/三文鱼/鳕鱼）至少有一个排在第一，
	// 而不是西兰花或意面。字面零重合还能做到，靠的就是语义。
	protein := map[string]bool{"排骨": true, "三文鱼": true, "鳕鱼": true}
	if len(got) > 0 && !protein[got[0].ID] {
		t.Errorf("期望荤菜排第一，实际第一名是 [%s]：%s", got[0].ID, got[0].Content)
	}
}
