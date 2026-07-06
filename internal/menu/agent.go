package menu

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"tomatoeino/internal/llm"
	"tomatoeino/internal/vectorstore"
)

// systemPersona 是 agent 的人设 + 决策口径。
//
// 这里把「将来才接的能力」也先写进口径（优先用现有食材、考虑时令、避免重样），
// 这样等真加了 库存/时令/超市 工具，模型的行为基调不用重调——口径先行，工具后补。
const systemPersona = `你是一个「幼儿备餐助手」，帮家长依据宝宝的吃饭历史规划三餐。

工作方式（必须严格遵守）：
- 你自己看不到宝宝的任何吃饭历史，唯一的获取方式是**调用工具**。因此在给出任何具体推荐前，
  你必须先调用至少一个工具拿到真实数据，绝不凭空编造宝宝吃过什么。
- 在调用工具、拿到结果之前，**不要输出任何面向家长的话**（尤其别说「好的」「我先看看」「稍等」
  这类过渡语）——直接发起工具调用。只有拿到工具结果后，才开口给答案。
- 工具：search_meal_history（按语义找相近的餐）、recent_meals（看最近几天吃了啥）、
  find_by_ingredient（按食材精确找）、seasonal_produce（查当月应季食材）、
  ask_user（向家长提问，仅当缺少家长才知道的信息时用）。查询类工具可多次、组合调用。
- 例外：如果上下文里出现【工具备忘】，那是你上一轮亲自调用工具查到的真实数据——
  本轮可以直接引用它作答，不必为同样的数据重复调用工具；只有备忘不够回答本轮问题、
  或数据可能过期时才再调工具。
- 拿到工具结果后，在**同一轮**里直接给出最终、可照做的推荐/答案，不要停在「正在查」。

推荐原则：
- 尽量和最近几天不重样，注意荤素搭配、食材多样。
- 考虑时令：推荐前可用 seasonal_produce 查当月应季食材，应季的优先、反季的少推。
- 参考历史里的做法和分量（宝宝餐通常少油少盐、煮软切小）。
- 只有当确实缺少「只有家长才知道」的信息（如冰箱里现有什么、想吃荤还是素）时，
  才调用 ask_user 向家长提问——一次只问一个具体问题，别和其他工具同时调用；
  不要在普通回答里提问后就结束。家长的回答会作为下一条消息回来，届时结合
  之前已查到的数据继续完成推荐，不要重查。其它情况都要给出可直接照做的答案。

回答风格：中文、简洁、可直接照着做；给出菜名时尽量带上关键做法/分量要点。`

// BuildAgent 把零件装配成一个可用的备餐 ReAct agent。
//
// 装配链路（每一步都依赖「接口」而非具体实现，所以好替换、好测试）：
//
//	历史 JSON ──LoadHistory──▶ []Day ──BuildDocuments──▶ []*Document
//	                                                         │ Store.Add（批量向量化）
//	Embedder ─────────────────────────────────────────────▶ 内存向量库 Store
//	                                                         │
//	ToolCallingChatModel + Tools(含 Store) ──react.NewAgent──▶ *react.Agent
//
// 返回的 []Day 同时给 HTTP 层的 /api/history 直接用，省一次读盘。
func BuildAgent(ctx context.Context, historyPath string) (*react.Agent, []Day, error) {
	// 1. 读历史
	days, err := LoadHistory(historyPath)
	if err != nil {
		return nil, nil, err
	}

	// 2. 建 embedder + 向量库，把历史灌进去（一次批量 embedding）
	embedder, err := llm.NewEmbedder(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 embedder 失败: %w", err)
	}
	store := vectorstore.New(embedder)
	if err := store.Add(ctx, BuildDocuments(days)); err != nil {
		return nil, nil, fmt.Errorf("把历史灌进向量库失败: %w", err)
	}

	// 3. 建带工具调用能力的模型 + 工具集
	cm, err := llm.NewToolCallingChatModel(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 ToolCallingChatModel 失败: %w", err)
	}
	tools, err := NewTools(store, days)
	if err != nil {
		return nil, nil, err
	}

	// 4. 装配 ReAct agent：模型 + 工具 + 人设
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		// L3 对话级中断：ask_user 一被调用，本轮立即结束、问题原文即本轮输出。
		// 家长的回答随下一轮进来，配合 L2 会话的全保真历史（此前查过的工具结果
		// 都在），模型原地续推、不用重查——「挂起-恢复」落在对话边界上。
		ToolReturnDirectly: map[string]struct{}{ToolNameAskUser: {}},
		// 人设 + 当天日期，每个请求动态注入（而非 NewPersonaModifier 那种建图时写死）。
		// 为什么日期必须给：模型自己不知道今天几号，不给就瞎猜——实测把
		// seasonal_produce 的 month 猜成了 4（实际 7 月），还通过工具备忘把错误
		// 月份的数据传染给下一轮。动态取值也让长驻进程跨月/跨天不会过期。
		MessageModifier: func(_ context.Context, input []*schema.Message) []*schema.Message {
			sys := schema.SystemMessage(systemPersona +
				fmt.Sprintf("\n\n今天是 %s。", time.Now().Format("2006-01-02")))
			return append([]*schema.Message{sys}, input...)
		},
		// MaxStep 限制「思考-调工具」的最大轮数，防止模型陷在工具循环里出不来。
		MaxStep: 12,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("创建 ReAct agent 失败: %w", err)
	}

	return agent, days, nil
}
