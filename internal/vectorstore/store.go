// Package vectorstore 实现一个最小可用的「内存向量库」。
//
// 它的全部职责只有两件事：
//   - Add     ：把文档向量化后存进内存
//   - Retrieve：把查询向量化，跟库里每条算相似度，返回最像的前 K 条
//
// 之所以自己写而不是直接上 Qdrant，是为了把「向量检索到底怎么算」这件事
// 摊开在眼前——就像你想搞懂清算逻辑时，会先在 Excel 里手算一遍对账，
// 而不是一上来就调清算中心的黑盒接口。
//
// 关键点：它实现了 eino 的 retriever.Retriever 接口（只有一个 Retrieve 方法），
// 所以在上层 Agent 眼里，它和 Qdrant / Milvus 是同一种东西、可以无缝替换。
package vectorstore

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// 默认返回多少条。检索是「召回候选」，给下游（模型）参考，
// 不是「精确查一行」，所以默认给几条让模型自己挑。
const defaultTopK = 5

// Store 是内存向量库本体。
//
// embedder 是「把文字变成向量」的工具——注意这里存的是接口 embedding.Embedder，
// 不是某个具体实现。L1 我们会塞一个「假 embedder」进来做实验，
// L2 再换成真的 OpenAI embedder，Store 这个文件一行都不用改。
type Store struct {
	embedder embedding.Embedder
	docs     []*schema.Document // 每条 doc 的 DenseVector 在 Add 时就算好存好
}

// New 用一个 embedder 构造空库。
func New(embedder embedding.Embedder) *Store {
	return &Store{embedder: embedder}
}

// Add 把一批文档灌进库。
//
// 流程：把所有 Content 收集起来「批量」embed（一次 API 调用处理多条，省钱省时，
// 就像批量代付比逐笔代付手续费低），再把算出来的向量挂回各自的 doc 上存起来。
func (s *Store) Add(ctx context.Context, docs []*schema.Document) error {
	if len(docs) == 0 {
		return nil
	}

	// 1. 收集待向量化的文本
	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Content
	}

	// 2. 批量向量化
	vectors, err := s.embedder.EmbedStrings(ctx, texts)
	if err != nil {
		return fmt.Errorf("向量化文档失败: %w", err)
	}
	// 防御：embedder 必须「输入几条、输出几条」一一对应，否则后面会错位
	if len(vectors) != len(docs) {
		return fmt.Errorf("向量数(%d)与文档数(%d)不一致", len(vectors), len(docs))
	}

	// 3. 把向量挂回 doc，存入库
	//    schema.Document.WithDenseVector 把向量塞进 doc 的 MetaData，
	//    后面 Retrieve 时用 doc.DenseVector() 取回来。
	for i, d := range docs {
		s.docs = append(s.docs, d.WithDenseVector(vectors[i]))
	}
	return nil
}

// Retrieve 实现 retriever.Retriever 接口：给一句查询，返回最相关的若干文档（按相似度降序）。
func (s *Store) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	// 解析调用方传进来的通用选项（TopK / ScoreThreshold 等）。
	// eino 把这些选项统一成 Options，GetCommonOptions 负责合并默认值与传入值。
	// Options.TopK 是 *int：用指针是为了区分「没传」和「传了 0」，所以默认值也得给个指针。
	topK := defaultTopK
	o := retriever.GetCommonOptions(&retriever.Options{TopK: &topK}, opts...)
	if o.TopK != nil {
		topK = *o.TopK
	}

	if len(s.docs) == 0 {
		return nil, nil
	}

	// 1. 查询用「同一个」embedder 向量化——索引和检索必须同模型，
	//    否则两边向量不在一个空间里，cosine 算出来毫无意义。
	qvs, err := s.embedder.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("向量化查询失败: %w", err)
	}
	queryVec := qvs[0]

	// 2. 跟库里每条算 cosine，记下分数
	type scored struct {
		doc   *schema.Document
		score float64
	}
	ranked := make([]scored, 0, len(s.docs))
	for _, d := range s.docs {
		score := cosine(queryVec, d.DenseVector())
		// ScoreThreshold 是「过滤」不是「排序」：低于阈值的直接丢弃
		if o.ScoreThreshold != nil && score < *o.ScoreThreshold {
			continue
		}
		ranked = append(ranked, scored{doc: d, score: score})
	}

	// 3. 按分数从高到低排序
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	// 4. 取前 topK，把分数写回 doc（下游可以用 doc.Score() 看到相关度）
	if topK > len(ranked) {
		topK = len(ranked)
	}
	result := make([]*schema.Document, 0, topK)
	for i := 0; i < topK; i++ {
		result = append(result, ranked[i].doc.WithScore(ranked[i].score))
	}
	return result, nil
}

// cosine 计算两个向量的余弦相似度，范围 [-1, 1]，越大越相似。
//
// 公式：dot(a,b) / (|a| * |b|)
// 只看方向、不看长度——这正是「语义像不像」想要的。
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
