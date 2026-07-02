// 火山方舟「多模态向量」适配器 —— 对接 doubao-embedding-vision 系列。
//
// 为什么不能直接用 eino 自带的 embeddingopenai：方舟的 vision 向量模型走的不是
// 标准 OpenAI embedding 协议，三处都不一样：
//   - 端点：/embeddings/multimodal（不是 /embeddings）
//   - 入参：input 是 [{type,text}] 这种「多模态片段」数组（不是 ["纯文本"]）
//   - 出参：data 是「单个对象」{embedding}（不是数组），一次只编码一条输入
//
// 我们让它实现 eino 的 embedding.Embedder 接口（只有 EmbedStrings 一个方法），
// 于是它和标准 embedder、假 embedder 在上层眼里是同一种东西——vectorstore.Store
// 一行不改就能换上它。这就是面向接口的回报，只不过这次换的是个「协议都不一样」的真 provider。

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/embedding"
)

// ArkContentPart 是多模态输入的「一片」内容。
//
// 现在只用到 text，但特意保留这个结构是为了「后续扩展」：
// 方舟 vision 模型本就支持图文混合，加上 image_url 字段后，就能把
// 「一张菜品照片 + 一句文字描述」一起编码成同一个向量——
// 将来做「按图找相似菜」或「图文一起检索」时直接复用这条路。
type ArkContentPart struct {
	Type     string       `json:"type"`                // "text"；后续可扩展 "image_url"
	Text     string       `json:"text,omitempty"`      // Type=="text" 时填
	ImageURL *ArkImageURL `json:"image_url,omitempty"` // 预留：Type=="image_url" 时填
}

// ArkImageURL 预留给后续传图（公网 url 或 base64 dataURL）。
type ArkImageURL struct {
	URL string `json:"url"`
}

// arkMultiModalEmbedder 实现 embedding.Embedder，对接方舟 /embeddings/multimodal。
type arkMultiModalEmbedder struct {
	apiKey  string
	baseURL string // 形如 https://ark.cn-beijing.volces.com/api/v3（末尾不带斜杠）
	model   string // 形如 doubao-embedding-vision-250615
	client  *http.Client
}

// 编译期确认它满足接口——所以上层拿到的还是同一种「Embedder」。
var _ embedding.Embedder = (*arkMultiModalEmbedder)(nil)

func newArkMultiModalEmbedder(apiKey, baseURL, model string) *arkMultiModalEmbedder {
	return &arkMultiModalEmbedder{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// EmbedStrings 满足 eino 接口：一批纯文本进、一批向量出，顺序一一对应。
//
// 因为 multimodal 接口「一次只收一条」，这里逐条调用——把每条文本包成
// 「一片 text 内容」喂进去。逐条的代价（放弃批量）是 provider 能力决定的，
// 不是我们想这样，就像被迫把「批量代付」改成「逐笔代付」。
func (e *arkMultiModalEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for i, t := range texts {
		vec, err := e.embedParts(ctx, []ArkContentPart{{Type: "text", Text: t}})
		if err != nil {
			return nil, fmt.Errorf("多模态 embed 第 %d 条失败: %w", i+1, err)
		}
		out = append(out, vec)
	}
	return out, nil
}

// embedParts 是多模态核心：把若干内容片段（文/图）编码成「一个」向量。
//
// 它是留给后续扩展的入口——现在 EmbedStrings 只塞一片 text 进来，
// 将来可以直接传 []ArkContentPart{{Type:"text",...}, {Type:"image_url",...}}。
func (e *arkMultiModalEmbedder) embedParts(ctx context.Context, parts []ArkContentPart) ([]float64, error) {
	payload, err := json.Marshal(map[string]any{
		"model": e.model,
		"input": parts,
	})
	if err != nil {
		return nil, fmt.Errorf("组装请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/embeddings/multimodal", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用方舟失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 把方舟原文带出来——404/400 时一眼能看出是模型名还是参数的问题。
		return nil, fmt.Errorf("方舟 multimodal 返回 %d: %s", resp.StatusCode, body)
	}

	// 注意 data 是「单个对象」不是数组——这正是和标准 OpenAI 协议的关键区别。
	var parsed struct {
		Data struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析方舟响应失败: %w（原文：%s）", err, body)
	}
	if len(parsed.Data.Embedding) == 0 {
		return nil, fmt.Errorf("方舟返回空向量（原文：%s）", body)
	}
	return parsed.Data.Embedding, nil
}
