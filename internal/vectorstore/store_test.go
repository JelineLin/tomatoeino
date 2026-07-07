package vectorstore

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
)

// fakeEmbedder 是一个「假」向量化器，专门用来在不联网、不花钱的情况下学原理。
//
// 它的做法极其朴素：预先定一张「词表」，把每段文字数成一个「这些词各出现几次」的向量。
//   词表 = [蒸, 炒, 煎, 排骨, 鸡腿, 牛肉, 三文鱼, 玉米, 土豆, 番茄, 意面]
//   "蒸排骨配玉米" -> 蒸=1, 排骨=1, 玉米=1, 其余=0 -> [1,0,0,1,0,0,0,1,0,0,0]
//
// 真实的 OpenAI embedding 当然比这聪明得多（它懂语义、近义词），
// 但「文字 -> 向量 -> 比方向」这个骨架是一模一样的。
// 等 L2 我们把这个 fakeEmbedder 换成真的，Store 的代码一行都不用改——
// 这就是面向接口编程的回报。
type fakeEmbedder struct {
	vocab []string
}

// 确保 fakeEmbedder 满足 embedding.Embedder 接口（编译期检查）。
var _ embedding.Embedder = (*fakeEmbedder)(nil)

func (f *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		vec := make([]float64, len(f.vocab))
		for j, w := range f.vocab {
			vec[j] = float64(strings.Count(t, w))
		}
		out[i] = vec
	}
	return out, nil
}

func TestRetrieve_排序符合直觉(t *testing.T) {
	ctx := context.Background()

	emb := &fakeEmbedder{vocab: []string{
		"蒸", "炒", "煎", "排骨", "鸡腿", "牛肉", "三文鱼", "玉米", "土豆", "番茄", "意面",
	}}
	store := New(emb)

	docs := []*schema.Document{
		{ID: "a", Content: "蒸排骨配玉米"},
		{ID: "b", Content: "蒸鸡腿配土豆"},
		{ID: "c", Content: "番茄意面"},
		{ID: "d", Content: "煎三文鱼"},
	}
	if err := store.Add(ctx, docs); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}

	// 查「蒸排骨」：期望 a（蒸+排骨全中）排第一，b（只中"蒸"）排第二，
	// c/d 跟查询毫无共同词，相似度为 0，应排在最后。
	got, err := store.Retrieve(ctx, "蒸排骨")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("没检索到任何结果")
	}

	// 打印出来，方便你跑 `go test -v` 时直观看到每条的相似度分数
	t.Log("查询「蒸排骨」的检索结果（按相似度降序）：")
	for i, d := range got {
		t.Logf("  #%d  score=%.4f  %s", i+1, d.Score(), d.Content)
	}

	if got[0].ID != "a" {
		t.Errorf("期望第一名是 a(蒸排骨配玉米)，实际是 %s(%s)", got[0].ID, got[0].Content)
	}
	if got[1].ID != "b" {
		t.Errorf("期望第二名是 b(蒸鸡腿配土豆)，实际是 %s(%s)", got[1].ID, got[1].Content)
	}
}
