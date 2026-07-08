package vectorstore

// Upsert/Remove/并发 的白盒测试（复用 store_test.go 的 fakeEmbedder，离线免费）。
// 这是多用户改造八步里的第 1 步：先把「运行时写」的地基打牢再谈上层功能。

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

func newTestStore() *Store {
	return New(&fakeEmbedder{vocab: []string{"鳕鱼", "牛肉", "番茄", "清蒸", "红烧"}})
}

func doc(id, content string) *schema.Document {
	return &schema.Document{ID: id, Content: content}
}

// retrieveAll 取回库里全部文档（TopK 给大数），按 ID 建 map 方便断言。
func retrieveAll(t *testing.T, s *Store, query string) map[string]string {
	t.Helper()
	docs, err := s.Retrieve(context.Background(), query, retriever.WithTopK(100))
	if err != nil {
		t.Fatalf("Retrieve 出错: %v", err)
	}
	out := map[string]string{}
	for _, d := range docs {
		out[d.ID] = d.Content
	}
	return out
}

func TestUpsert_SameIDReplaces(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	// 先 Add 一条「清蒸鳕鱼」,再用同 ID Upsert 成「红烧鳕鱼」——覆盖修正场景。
	if err := s.Add(ctx, []*schema.Document{doc("2026-07-08-dinner", "清蒸鳕鱼")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(ctx, []*schema.Document{doc("2026-07-08-dinner", "红烧鳕鱼")}); err != nil {
		t.Fatal(err)
	}

	got := retrieveAll(t, s, "鳕鱼")
	if len(got) != 1 {
		t.Fatalf("同 ID Upsert 后应只剩 1 条（旧向量不留尸体），实际 %d 条: %v", len(got), got)
	}
	if got["2026-07-08-dinner"] != "红烧鳕鱼" {
		t.Errorf("应是新内容「红烧鳕鱼」，实际 %q", got["2026-07-08-dinner"])
	}
}

func TestUpsert_NewIDActsAsAdd(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	if err := s.Upsert(ctx, []*schema.Document{doc("d1", "番茄牛肉")}); err != nil {
		t.Fatal(err)
	}
	if got := retrieveAll(t, s, "牛肉"); len(got) != 1 || got["d1"] != "番茄牛肉" {
		t.Errorf("空库 Upsert 应等价于 Add，实际 %v", got)
	}
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	if err := s.Add(ctx, []*schema.Document{doc("d1", "清蒸鳕鱼"), doc("d2", "红烧牛肉")}); err != nil {
		t.Fatal(err)
	}
	s.Remove("d1")
	got := retrieveAll(t, s, "鳕鱼 牛肉")
	if len(got) != 1 {
		t.Fatalf("删 1 条后应剩 1 条，实际 %d: %v", len(got), got)
	}
	if _, ok := got["d1"]; ok {
		t.Error("d1 已删除，不该还能检索到")
	}
	// 幂等：删不存在的 ID 不炸。
	s.Remove("d1", "不存在的id", "")
}

// TestConcurrentReadWrite 用 -race 检查读写竞态：
// 一半 goroutine 反复 Retrieve，一半反复 Upsert/Remove 同一批 ID。
// 断言逻辑正确性没意义（时序不定），要的是 race detector 不报警。
func TestConcurrentReadWrite(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()
	if err := s.Add(ctx, []*schema.Document{doc("seed", "清蒸鳕鱼")}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("w%d-%d", n, i%5)
				_ = s.Upsert(ctx, []*schema.Document{doc(id, "番茄牛肉")})
				if i%7 == 0 {
					s.Remove(id)
				}
			}
		}(w)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = s.Retrieve(ctx, "鳕鱼 牛肉", retriever.WithTopK(3))
			}
		}()
	}
	wg.Wait()

	// 库应仍可用，seed 一直没动过、必须还在。
	if got := retrieveAll(t, s, "鳕鱼"); got["seed"] != "清蒸鳕鱼" {
		t.Errorf("并发读写后 seed 文档丢失或损坏: %v", got)
	}
}
