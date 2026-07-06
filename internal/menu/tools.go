package menu

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"tomatoeino/internal/vectorstore"
)

// searchTopK 是 search_meal_history 默认召回多少条。检索是「给模型候选」，
// 不是「精确查一行」，给几条让模型自己挑。
const searchTopK = 6

// ToolNameAskUser 是「向家长提问」工具名。agent.go 把它配进 ToolReturnDirectly：
// 模型一调它，整轮立即结束、问题原文就是本轮输出——这是 L3「对话级中断」的一半；
// 另一半靠 L2 会话：家长回答随下一轮进来，模型在全保真历史上原地续推。
const ToolNameAskUser = "ask_user"

// NewTools 把 agent 的能力包成 eino 工具，交给 ReAct agent 自主调用。
//
// 关键设计：每个工具都是独立、自带描述的——模型只看描述就知道「该用哪个」。
// 加一个数据源（将来的 超市/库存）= 再 append 一个 InferTool，agent 编排不用改。
// 这正是选 ReAct 而非写死 RAG 链的回报——seasonal_produce 的加入就是第一次兑现：
// 只动了 tools.go（append）和人设一句口径，检索/编排一行没改。
//
// 五个工具覆盖不同「形态」：
//   - search_meal_history ：语义检索（意思像就行，靠向量库）
//   - recent_meals        ：按时间取最近 N 天（精确、不耗 embedding）
//   - find_by_ingredient  ：按食材精确子串匹配（找「含某样东西」的餐）
//   - seasonal_produce    ：计算型（时令表 + 当前日期的纯函数，见 season.go）
//   - ask_user            ：人机交互（向家长提问并中断本轮，见 ToolNameAskUser）
func NewTools(store *vectorstore.Store, days []Day) ([]tool.BaseTool, error) {
	searchTool, err := utils.InferTool(
		"search_meal_history",
		"按语义检索宝宝的历史菜单：传入一句自然语言（如『清淡的鱼类晚餐』『不重样的午餐』），"+
			"返回意思最接近的若干条历史餐次。适合『想吃点像 X 的』这类模糊需求。",
		makeSearch(store),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 search_meal_history 工具失败: %w", err)
	}

	recentTool, err := utils.InferTool(
		"recent_meals",
		"取最近 N 天的完整菜单（午餐/水果/晚餐），按日期返回。适合『这几天吃了啥』"+
			"『安排明天的别和最近重样』这类需要看近期记录的需求。",
		makeRecent(days),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 recent_meals 工具失败: %w", err)
	}

	ingredientTool, err := utils.InferTool(
		"find_by_ingredient",
		"按食材/关键词精确查找历史上含它的餐次（在菜名和做法明细里做子串匹配），"+
			"如查『鳕鱼』『羊肚菌』『西兰花』。适合『最近吃过哪些鱼』『上次羊肚菌怎么做的』。",
		makeFindByIngredient(days),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 find_by_ingredient 工具失败: %w", err)
	}

	seasonTool, err := utils.InferTool(
		"seasonal_produce",
		"查指定月份的时令食材（不传 month 默认当前月份）：应季蔬菜、水果、水产，附备餐提示。"+
			"适合『来点应季的』『这个季节吃什么合适』，以及推荐配菜前确认食材是否应季。",
		makeSeasonal(time.Now),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 seasonal_produce 工具失败: %w", err)
	}

	askTool, err := utils.InferTool(
		ToolNameAskUser,
		"当缺少只有家长才知道的关键信息（如冰箱里现有什么、宝宝今天胃口如何、想吃荤还是素）时，"+
			"用它向家长提一个具体问题。调用后本轮立即结束、问题直接呈给家长，家长的回答会出现在"+
			"下一条消息里。一次只问一个问题；不要和其他工具同时调用；能从历史/时令查到的信息不要问。",
		makeAskUser(),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 ask_user 工具失败: %w", err)
	}

	return []tool.BaseTool{searchTool, recentTool, ingredientTool, seasonTool, askTool}, nil
}

// ---- search_meal_history ----

type searchInput struct {
	Query string `json:"query" jsonschema:"description=要检索的自然语言查询，描述想找什么样的餐,required"`
}

// makeSearch 返回一个闭包，捕获向量库。工具内部就是调 Store.Retrieve——
// 整个语义检索的脏活（向量化查询、算 cosine、排序、取 TopK）都在 vectorstore 里，
// 这里只负责把命中结果拼成模型读得懂的人话。
func makeSearch(store *vectorstore.Store) func(context.Context, searchInput) (string, error) {
	return func(ctx context.Context, in searchInput) (string, error) {
		q := strings.TrimSpace(in.Query)
		log.Printf("🔧 search_meal_history(query=%q)", q)
		if q == "" {
			return "查询为空，请给一句描述想找什么样的餐。", nil
		}
		docs, err := store.Retrieve(ctx, q, retriever.WithTopK(searchTopK))
		if err != nil {
			return "", fmt.Errorf("检索失败: %w", err)
		}
		if len(docs) == 0 {
			return "历史里没有检索到相关的餐次。", nil
		}
		lines := make([]string, 0, len(docs))
		for _, d := range docs {
			lines = append(lines, "- "+d.Content)
		}
		return "检索到的历史餐次（按相关度降序）：\n" + strings.Join(lines, "\n"), nil
	}
}

// ---- recent_meals ----

type recentInput struct {
	Days int `json:"days" jsonschema:"description=要回看的天数，建议 3~7；不传或<=0 按 3 天,required"`
}

// makeRecent 捕获历史切片。history.json 是按日期升序排的，所以「最近 N 天」就是取尾部。
func makeRecent(days []Day) func(context.Context, recentInput) (string, error) {
	return func(ctx context.Context, in recentInput) (string, error) {
		n := in.Days
		if n <= 0 {
			n = 3
		}
		if n > len(days) {
			n = len(days)
		}
		log.Printf("🔧 recent_meals(days=%d)", n)
		if n == 0 {
			return "历史为空。", nil
		}
		recent := days[len(days)-n:]
		blocks := make([]string, 0, len(recent))
		for _, d := range recent {
			blocks = append(blocks, renderDay(d))
		}
		return fmt.Sprintf("最近 %d 天的菜单：\n%s", n, strings.Join(blocks, "\n")), nil
	}
}

// ---- find_by_ingredient ----

type ingredientInput struct {
	Ingredient string `json:"ingredient" jsonschema:"description=要查找的食材或关键词，如『鳕鱼』『羊肚菌』,required"`
}

// makeFindByIngredient 在所有菜的「菜名 + 明细」里做子串匹配，命中就把那一餐整条带出来。
func makeFindByIngredient(days []Day) func(context.Context, ingredientInput) (string, error) {
	return func(ctx context.Context, in ingredientInput) (string, error) {
		kw := strings.TrimSpace(in.Ingredient)
		log.Printf("🔧 find_by_ingredient(ingredient=%q)", kw)
		if kw == "" {
			return "请给一个要查找的食材或关键词。", nil
		}
		var hits []string
		for _, d := range days {
			for _, mk := range mealOrder {
				m := d.mealOf(mk.field)
				if m == nil {
					continue
				}
				if mealContains(m, kw) {
					hits = append(hits, "- "+renderMeal(d.Date, mk.label, m))
				}
			}
		}
		if len(hits) == 0 {
			return fmt.Sprintf("历史里没有出现过「%s」。", kw), nil
		}
		return fmt.Sprintf("历史上含「%s」的餐次：\n%s", kw, strings.Join(hits, "\n")), nil
	}
}

// ---- ask_user ----

type askInput struct {
	Question string `json:"question" jsonschema:"description=要问家长的一个具体问题，中文，一次只问一件事,required"`
}

// makeAskUser 返回「向家长提问」工具。它不查任何数据——职责只是把问题原样交出去；
// 「调用即结束本轮」的行为不在这里实现，而是靠 agent 装配时的 ToolReturnDirectly
//（见 agent.go）。工具管内容、编排管流程，各归各位。
func makeAskUser() func(context.Context, askInput) (string, error) {
	return func(ctx context.Context, in askInput) (string, error) {
		q := strings.TrimSpace(in.Question)
		log.Printf("🔧 ask_user(question=%q)", q)
		if q == "" {
			// 别让空问题把整轮变成空白回复。
			return "想再和家长确认一点信息：请补充一下想吃什么、或家里现有什么食材？", nil
		}
		return q, nil
	}
}

// mealContains 判断一餐里有没有哪道菜的菜名/明细含关键词。
func mealContains(m *Meal, kw string) bool {
	for _, dish := range m.Dishes {
		if strings.Contains(dish.Name, kw) || strings.Contains(dish.Detail, kw) {
			return true
		}
	}
	return false
}

// renderDay 把一天的非空餐次逐行渲染，供 recent_meals 用。
func renderDay(d Day) string {
	var lines []string
	for _, mk := range mealOrder {
		m := d.mealOf(mk.field)
		if m == nil || len(m.Dishes) == 0 {
			continue
		}
		lines = append(lines, renderMeal(d.Date, mk.label, m))
	}
	return strings.Join(lines, "\n")
}
