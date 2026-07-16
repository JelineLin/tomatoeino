package llm

// embed_cache.go —— 给 Embedder 包一层「按内容寻址」的磁盘缓存。
//
// 解决的问题：向量只活在内存里，进程每次重启都要把同样的历史文本重新调一遍
// embedding API——同样的输入换回同样的向量，这笔重复支出没换来任何东西。
// 有了缓存：文本没变的直接读盘（零 API 调用、毫秒装载），只有新增/改动的才现算。
// 附带的韧性红利：重启时哪怕 embedding 服务不可用，缓存命中的部分照常就绪。
//
// 设计取舍（和用户拍板的一致：零依赖写文件，不上 SQLite/向量库）：
//   - 一条向量一个 JSON 小文件，文件名 = sha256(模型名 + 文本)。内容寻址意味着：
//     * 文本变了（比如那餐补了反馈）→ 新哈希新文件，旧文件成孤儿——量级极小，不清理；
//     * 换 embedding 模型 → 哈希全变 → 天然整体失效重建，不会新旧向量混一个空间
//       （向量空间口径一致是硬约束，靠「模型名进哈希」结构性保证，不靠运维纪律）；
//   - 每个文件独立原子写（临时文件 + rename），增量写入不存在「全量重写」的放大问题；
//   - 缓存是【可随时丢弃重建】的派生数据：删掉整个目录 = 下次重启全量重算，权威数据
//     永远是 history.json 里的原文。
//   - 写缓存失败只记日志不报错——缓存哑了不该挡记账（和向量 Upsert 失败不阻断同一口径）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
)

// cachingEmbedder 是带磁盘缓存的 Embedder 装饰器。上层拿到的仍是 embedding.Embedder，
// vectorstore.Store 与业务层完全无感——又一次面向接口的回报。
type cachingEmbedder struct {
	inner embedding.Embedder
	dir   string // 缓存目录，一条向量一个文件
	model string // 模型名，参与哈希：换模型 = 整体失效
}

var _ embedding.Embedder = (*cachingEmbedder)(nil)

// cachedVector 是落盘格式。除向量本体外记模型名和维度，纯为人工排查时可读——
// 命中与否由文件名（内容哈希）决定，不读这些字段判断。
type cachedVector struct {
	Model string    `json:"model"`
	Dim   int       `json:"dim"`
	Vec   []float64 `json:"vec"`
}

func newCachingEmbedder(inner embedding.Embedder, dir, model string) *cachingEmbedder {
	return &cachingEmbedder{inner: inner, dir: dir, model: model}
}

// key 内容寻址：sha256(模型名 + NUL + 文本)。NUL 分隔防止「模型名+文本」拼接歧义。
func (c *cachingEmbedder) key(text string) string {
	h := sha256.Sum256([]byte(c.model + "\x00" + text))
	return hex.EncodeToString(h[:])
}

func (c *cachingEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	var missIdx []int
	var missTexts []string
	for i, t := range texts {
		if v, ok := c.load(c.key(t)); ok {
			out[i] = v
		} else {
			missIdx = append(missIdx, i)
			missTexts = append(missTexts, t)
		}
	}

	if len(missTexts) > 0 {
		vecs, err := c.inner.EmbedStrings(ctx, missTexts, opts...)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(missTexts) {
			return nil, fmt.Errorf("embedding 返回 %d 条向量，但请求了 %d 条文本", len(vecs), len(missTexts))
		}
		for j, v := range vecs {
			out[missIdx[j]] = v
			c.store(c.key(missTexts[j]), v)
		}
	}

	// 只在批量场景（启动重建/批量导入）报一眼命中率；运行时单条调用保持安静，不刷屏。
	if len(texts) > 1 {
		log.Printf("🧊 embedding 缓存：命中 %d/%d（未命中的 %d 条已现算并回填）",
			len(texts)-len(missTexts), len(texts), len(missTexts))
	}
	return out, nil
}

// load 读一条缓存。任何异常（不存在/损坏/维度为零）都按未命中处理——缓存永远可以重算。
func (c *cachingEmbedder) load(key string) ([]float64, bool) {
	raw, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return nil, false
	}
	var cv cachedVector
	if err := json.Unmarshal(raw, &cv); err != nil || len(cv.Vec) == 0 {
		return nil, false
	}
	return cv.Vec, true
}

// store 原子落盘一条缓存（临时文件 + rename）。失败只记日志——下次没命中再算一遍就是。
func (c *cachingEmbedder) store(key string, vec []float64) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		log.Printf("⚠️ 创建向量缓存目录失败（本条不缓存）: %v", err)
		return
	}
	raw, err := json.Marshal(cachedVector{Model: c.model, Dim: len(vec), Vec: vec})
	if err != nil {
		log.Printf("⚠️ 序列化向量缓存失败（本条不缓存）: %v", err)
		return
	}
	tmp, err := os.CreateTemp(c.dir, ".vec-*.tmp")
	if err != nil {
		log.Printf("⚠️ 创建向量缓存临时文件失败（本条不缓存）: %v", err)
		return
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		log.Printf("⚠️ 写向量缓存失败（本条不缓存）: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(c.dir, key+".json")); err != nil {
		os.Remove(tmp.Name())
		log.Printf("⚠️ 落盘向量缓存失败（本条不缓存）: %v", err)
	}
}

// embeddingCacheDir 决定缓存目录：OPENAI_EMBEDDING_CACHE 显式关掉返回空串（不缓存）；
// OPENAI_EMBEDDING_CACHE_DIR 可自定义位置，默认 data/vector-cache（和 DATA_DIR 默认值同根，
// 服务器上落在 /opt/menuagent/data/vector-cache）。缓存按「模型名+文本」寻址、不含用户概念：
// 同一段文本谁来算都是同一条向量，多户共享反而省钱。
func embeddingCacheDir() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_EMBEDDING_CACHE"))) {
	case "false", "0", "off", "no":
		return ""
	}
	if dir := os.Getenv("OPENAI_EMBEDDING_CACHE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join("data", "vector-cache")
}
