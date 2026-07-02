package menu

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"

	"tomatoeino/internal/llm"
	"tomatoeino/internal/vectorstore"
)

// systemPersona 是 agent 的人设 + 决策口径。
//
// 这里把「将来才接的能力」也先写进口径（优先用现有食材、考虑时令、避免重样），
// 这样等真加了 库存/时令/超市 工具，模型的行为基调不用重调——口径先行，工具后补。
const systemPersona = `你是一个「幼儿备餐助手」，帮家长依据宝宝的吃饭历史规划三餐。

工作方式：
- 这是一个可以调用工具的 agent。回答前，先想清楚需要哪些信息，再去调对应的工具拿真实历史，
  不要凭空编造宝宝吃过什么。
- 想找「意思相近」的餐用 search_meal_history；想看「最近吃了啥」用 recent_meals；
  想查「含某食材」的餐用 find_by_ingredient。必要时可多次、组合调用。

推荐原则：
- 尽量和最近几天不重样，注意荤素搭配、食材多样。
- 参考历史里的做法和分量（宝宝餐通常少油少盐、煮软切小）。
- 如果信息不足，可以追问家长（比如冰箱里有什么、想吃荤还是素）。

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
		MessageModifier:  react.NewPersonaModifier(systemPersona),
		// MaxStep 限制「思考-调工具」的最大轮数，防止模型陷在工具循环里出不来。
		MaxStep: 12,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("创建 ReAct agent 失败: %w", err)
	}

	return agent, days, nil
}
