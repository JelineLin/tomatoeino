// 例子 01：裸调用 ChatModel
//
// 学习目标：
//  1. 理解 schema.Message —— eino 里所有对话的数据契约（System / User / Assistant 三种角色）
//  2. 理解 ChatModel 的两种调用方式：
//     - Generate：阻塞，一次性拿到完整回复（像同步 RPC）
//     - Stream  ：流式，一个 token 一个 token 地吐（像 SSE / gRPC stream）
//
// 运行：
//
//	export OPENAI_API_KEY=sk-xxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1   # 公司网关换成对应地址
//	export OPENAI_MODEL=gpt-4o-mini
//	go run ./examples/01_chatmodel
package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/cloudwego/eino/schema"

	"tomatoeino/internal/llm"
)

func main() {
	ctx := context.Background()

	cm, err := llm.NewChatModel(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// messages 是一个有序的对话历史。
	// SystemMessage 设定角色/规则；UserMessage 是用户输入。
	// 这跟你平时构造请求参数一样，只不过这里的"参数"是自然语言。
	messages := []*schema.Message{
		schema.SystemMessage("你是一个专业的育儿专家，回答控制在两三句话以内。"),
		schema.UserMessage("用一句话建议2岁宝宝的睡前故事。"),
	}

	// ---------- 方式一：Generate（一次性返回）----------
	fmt.Println("===== Generate（阻塞式）=====")
	resp, err := cm.Generate(ctx, messages)
	if err != nil {
		log.Fatal(err)
	}
	// resp 本身也是一个 *schema.Message，角色是 assistant。
	fmt.Println(resp.Content)
	// Usage 里有 token 消耗，生产环境用来做成本核算（像对账）。
	if resp.ResponseMeta != nil && resp.ResponseMeta.Usage != nil {
		fmt.Printf("[tokens] prompt=%d completion=%d total=%d\n",
			resp.ResponseMeta.Usage.PromptTokens,
			resp.ResponseMeta.Usage.CompletionTokens,
			resp.ResponseMeta.Usage.TotalTokens)
	}

	// ---------- 方式二：Stream（流式返回）----------
	fmt.Println("\n===== Stream（流式）=====")
	stream, err := cm.Stream(ctx, messages)
	if err != nil {
		log.Fatal(err)
	}
	// StreamReader 必须 Close，否则可能泄漏底层连接/goroutine（跟 http.Response.Body 一样）。
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break // 流正常结束
		}
		if err != nil {
			log.Fatal(err)
		}
		// 每个 chunk 是一个增量片段，把 Content 拼起来就是完整回复。
		fmt.Print(chunk.Content)
	}
	fmt.Println()
}
