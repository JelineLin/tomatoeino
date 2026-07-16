package llm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
)

// countingEmbedder 是测试替身：数被调了几次、收到多少条文本，返回可分辨的假向量。
type countingEmbedder struct {
	calls int
	texts []string
}

func (f *countingEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	f.calls++
	f.texts = append(f.texts, texts...)
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = []float64{float64(len(t)), 42} // 用长度当向量，能对上是谁的结果
	}
	return out, nil
}

func TestCachingEmbedder_命中不调API(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{}
	c := newCachingEmbedder(inner, dir, "model-a")
	ctx := context.Background()

	// 第一次：全未命中，走 inner 并落盘。
	v1, err := c.EmbedStrings(ctx, []string{"清蒸鳕鱼", "白粥"})
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 || len(inner.texts) != 2 {
		t.Fatalf("首次应调 inner 一次 2 条，got calls=%d texts=%d", inner.calls, len(inner.texts))
	}

	// 第二次同样文本：全命中，inner 不该再被调。
	v2, err := c.EmbedStrings(ctx, []string{"清蒸鳕鱼", "白粥"})
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Fatalf("全命中时不该再调 inner，calls=%d", inner.calls)
	}
	for i := range v1 {
		if v1[i][0] != v2[i][0] {
			t.Fatalf("缓存回读的向量和首算不一致：%v vs %v", v1[i], v2[i])
		}
	}

	// 混合批：一条命中 + 一条新文本 → inner 只收到新那条，顺序不乱。
	v3, err := c.EmbedStrings(ctx, []string{"新菜南瓜粥", "白粥"})
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("混合批应再调一次 inner，calls=%d", inner.calls)
	}
	if v3[0][0] != float64(len("新菜南瓜粥")) || v3[1][0] != float64(len("白粥")) {
		t.Fatalf("混合批结果顺序错位：%v", v3)
	}
}

func TestCachingEmbedder_换模型整体失效(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	a := &countingEmbedder{}
	if _, err := newCachingEmbedder(a, dir, "model-a").EmbedStrings(ctx, []string{"清蒸鳕鱼"}); err != nil {
		t.Fatal(err)
	}

	// 同一目录、同一文本、不同模型名 → 哈希不同 → 必须未命中重算，绝不复用别的模型的向量。
	b := &countingEmbedder{}
	if _, err := newCachingEmbedder(b, dir, "model-b").EmbedStrings(ctx, []string{"清蒸鳕鱼"}); err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatal("换模型后不该命中旧模型的缓存")
	}
}

func TestCachingEmbedder_损坏文件当未命中(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{}
	c := newCachingEmbedder(inner, dir, "model-a")
	ctx := context.Background()

	if _, err := c.EmbedStrings(ctx, []string{"清蒸鳕鱼"}); err != nil {
		t.Fatal(err)
	}
	// 把唯一的缓存文件写坏。
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("应恰好落盘 1 个缓存文件，got %d", len(entries))
	}
	if err := os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte("{坏的"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 损坏 = 未命中：重算一遍并覆盖回填，不报错。
	if _, err := c.EmbedStrings(ctx, []string{"清蒸鳕鱼"}); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("损坏文件应触发重算，calls=%d", inner.calls)
	}
}
