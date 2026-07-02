// Package llm 负责创建 ChatModel 实例。
//
// 这里把"如何连模型"这件事收敛到一个地方，后续所有例子复用同一个构造函数——
// 就像金融系统里把"如何连 LP（流动性提供商）"封装成统一的 client factory，
// 换 LP（换模型 provider）时只改这一处。
package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	embeddingopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/joho/godotenv"
)

// init 在包加载时自动读取项目根目录的 .env，把里面的键值对注入进程环境变量。
//
// 等价于你以前手动 export 一遍，只不过现在把配置收敛到一个文件里管理——
// 就像把 LP 的网关地址、密钥从「每次手动敲」改成「读统一配置中心」。
//
// godotenv.Load() 的语义：
//   - 不覆盖已存在的环境变量（真实 export / CI 注入的值优先级更高）
//   - 文件不存在时静默返回，不报错（生产环境可以完全不用 .env，直接走真实环境变量）
func init() {
	_ = godotenv.Load()
}

// NewChatModel 用环境变量构造一个 OpenAI 兼容的 ChatModel。
//
// 需要的环境变量：
//   - OPENAI_API_KEY  : 你的 API key
//   - OPENAI_BASE_URL : 兼容网关地址，如 https://api.openai.com/v1（公司内部网关换成对应地址）
//   - OPENAI_MODEL    : 模型名，如 gpt-4o-mini
//
// 返回的是 eino 的 model.BaseChatModel 接口，而不是具体实现类型——
// 这样上层代码只依赖接口，不关心底层是 OpenAI 还是 Ark，符合依赖倒置。
//
// 只会「聊天」（Generate/Stream），不带工具调用能力。例子 01/03 用的就是它。
func NewChatModel(ctx context.Context) (model.BaseChatModel, error) {
	return newOpenAIChatModel(ctx)
}

// NewToolCallingChatModel 和 NewChatModel 连的是同一个模型、同一套环境变量，
// 区别只在「对外暴露的接口更宽」：返回 model.ToolCallingChatModel（多了 WithTools），
// 这是 eino ReAct agent 装配工具时要求的类型。
//
// 为什么要单开一个函数：BaseChatModel 没有 WithTools，agent 接不上；而具体的
// *openai.ChatModel 其实两个接口都实现了，所以这里只是「把同一个东西按更宽的接口交出去」。
// 就像同一个 LP client，对账模块只要「查询」接口，下单模块要「查询+下单」接口——
// 同一个实例，按调用方需要的能力面暴露。
func NewToolCallingChatModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	return newOpenAIChatModel(ctx)
}

// newOpenAIChatModel 是私有构造：读环境变量、建出具体的 *openai.ChatModel。
// 上面两个公开工厂都复用它，只是把返回值「向上转型」成各自需要的接口——
// 把「怎么连模型」这件事收敛到唯一一处，换 provider / 改默认值只动这里。
func newOpenAIChatModel(ctx context.Context) (*openai.ChatModel, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("环境变量 OPENAI_API_KEY 未设置")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1" // 默认官方地址
	}

	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini" // 学习阶段用便宜的小模型即可
	}

	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModel 失败: %w", err)
	}
	return cm, nil
}

// NewEmbedder 用环境变量构造一个 OpenAI 兼容的 Embedder（把文字变成向量）。
//
// 凭证 & 端点：embedding 可以和 chat 完全不在一家——比如 chat 走 DeepSeek、embedding 走
// 火山方舟（DeepSeek 根本没有 embedding 接口，硬用它的网关会 404）。chat 的网关地址和 key
// 对 embedding 不一定适用，所以 embedding 的「地址」和「密钥」都先找自己专用的，
// 没配再回退到和 chat 共用的（就像同一家 LP 给清算和行情各开一把密钥、各给一个网关）：
//   - OPENAI_EMBEDDING_API_KEY  : embedding 专用 key；留空则回退用 OPENAI_API_KEY
//   - OPENAI_EMBEDDING_BASE_URL : embedding 专用网关地址；留空则回退用 OPENAI_BASE_URL
//   - OPENAI_EMBEDDING_MODEL    : embedding 模型名 / 接入点 ID，默认 text-embedding-3-small
//   - OPENAI_EMBEDDING_BATCH    : 是否允许「一次一批」。默认 true（OpenAI 支持批量）；
//                                 豆包等「单次只收一条」的接入点设为 false，会自动改走逐条调用。
//   - OPENAI_EMBEDDING_MULTIMODAL : 设为 true 改用方舟多模态向量接口（doubao-embedding-vision，
//                                   非标准协议，见 ark_embedding.go）；默认 false 走标准 OpenAI 协议。
//
// 注意 embedding 和 chat 是「两个不同的模型/接口」：各算各的钱、各有各的模型名，
// 甚至各有各的 key，不能混用。
//
// 返回 embedding.Embedder 接口——这正是 vectorstore.Store 需要的类型，
// 所以 L1 里那个假 embedder 可以直接被它替换，Store 代码无感知。
func NewEmbedder(ctx context.Context) (embedding.Embedder, error) {
	// 先找 embedding 专用 key；没配再回退到和 chat 共用的 OPENAI_API_KEY。
	apiKey := os.Getenv("OPENAI_EMBEDDING_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("环境变量 OPENAI_EMBEDDING_API_KEY / OPENAI_API_KEY 均未设置")
	}

	// embedding 的网关可以和 chat 不在一家：先找 embedding 专用地址，没配再回退到
	// 和 chat 共用的 OPENAI_BASE_URL（仍为空则组件走官方默认地址）。
	baseURL := os.Getenv("OPENAI_EMBEDDING_BASE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}

	modelName := os.Getenv("OPENAI_EMBEDDING_MODEL")
	if modelName == "" {
		modelName = "text-embedding-3-small" // 便宜、够用的小向量模型
	}

	// 方舟 vision 向量走的不是标准 OpenAI 协议（端点 /embeddings/multimodal、入参是多模态片段、
	// 出参是单条），eino 自带 embedder 接不了。OPENAI_EMBEDDING_MULTIMODAL=true 时改用我们
	// 自己的适配器（见 ark_embedding.go），它一样实现 embedding.Embedder，上层无感知。
	if multimodalEnabled() {
		return newArkMultiModalEmbedder(apiKey, baseURL, modelName), nil
	}

	emb, err := embeddingopenai.NewEmbedder(ctx, &embeddingopenai.EmbeddingConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Embedder 失败: %w", err)
	}

	// 有些 provider（如豆包/火山方舟的 embedding 接入点）单次只接受一条文本、不支持
	// 「一次一批」。设 OPENAI_EMBEDDING_BATCH=false 时，包一层适配器把批量拆成逐条调用——
	// 上层 vectorstore.Store 仍按「一次传一批」写，完全不用改。这就是面向接口的回报。
	if !batchEnabled() {
		return &perTextEmbedder{inner: emb}, nil
	}
	return emb, nil
}

// batchEnabled 读 OPENAI_EMBEDDING_BATCH，默认 true（按支持批量处理，如 OpenAI）。
// 只有显式设成 false / 0 / off / no 才关掉批量、改走逐条。
func batchEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_EMBEDDING_BATCH"))) {
	case "false", "0", "off", "no":
		return false
	default:
		return true
	}
}

// multimodalEnabled 读 OPENAI_EMBEDDING_MULTIMODAL，默认 false（走标准 OpenAI 协议）。
// 设成 true / 1 / on / yes 时改用方舟多模态向量适配器（doubao-embedding-vision）。
func multimodalEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_EMBEDDING_MULTIMODAL"))) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// perTextEmbedder 把「一次一批」的 EmbedStrings 拆成「一次一条」逐个调底层 embedder，
// 兼容那些单次只收一条文本的 provider（如豆包部分 embedding 接入点）。
//
// 代价：放弃了批量请求省下的往返/费用（就像被迫把「批量代付」改成「逐笔代付」），
// 换来的是兼容性。这是 provider 能力差异下的折中，而非更优实现。
type perTextEmbedder struct {
	inner embedding.Embedder
}

// 编译期确认它满足 embedding.Embedder 接口，所以上层拿到的还是同一种东西。
var _ embedding.Embedder = (*perTextEmbedder)(nil)

func (e *perTextEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for _, t := range texts {
		vs, err := e.inner.EmbedStrings(ctx, []string{t}, opts...)
		if err != nil {
			return nil, fmt.Errorf("逐条 embed 第 %d 条失败: %w", len(out)+1, err)
		}
		if len(vs) != 1 {
			return nil, fmt.Errorf("逐条 embed 期望返回 1 个向量，实际 %d 个", len(vs))
		}
		out = append(out, vs[0])
	}
	return out, nil
}
