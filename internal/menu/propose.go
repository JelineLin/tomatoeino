package menu

// propose.go —— F3「可编辑推荐菜单」的后端核心（方案 A）。
//
// 问题：agent 的推荐一直是自由 markdown，前端没法逐项编辑、也没法一键采纳入库。
// 方案 A 用一个工具 propose_menu 让 agent 把推荐【同时】以结构化参数登记一份——
// 结构化输出走工具参数，比让 deepseek 在正文里自由吐 JSON 稳得多（见 DeepSeek 工具调用坑）。
//
// 怎么把工具产出的结构化菜单捞到 HTTP 层：复用 trace.go 的 ctx 贯穿机制。
// 处理器在 Generate 前往 ctx 挂一个请求级 MenuSink，propose_menu 调用时把菜单写进去，
// Generate 返回后处理器读走。ctx 是编译图世界里唯一的请求级通道，天然按请求隔离。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// RecommendedMenu 是一次推荐的结构化结果：某天的若干餐。随简报下发给 iOS，
// 供「逐项编辑 + 一键应用入库」。
type RecommendedMenu struct {
	Date  string         `json:"date"`
	Meals []ProposedMeal `json:"meals"`
}

// ProposedMeal 是推荐里的一餐。字段一律 always-present（不 omitempty），
// 让 iOS 端非可选解码省心（time/reason 空就给空串，两个切片恒为 [] 不为 null）。
type ProposedMeal struct {
	Meal   string `json:"meal"`   // lunch/fruit/dinner
	Time   string `json:"time"`   // 建议用餐时间，可空串
	Dishes []Dish `json:"dishes"` // 复用历史的 Dish{Name,Detail}
	// Alternatives 是这一餐的备选菜（约定 2 道）：家长觉得主推的不合适，前端直接
	// 换上去即可——【零请求、零延迟、零 token】。
	//
	// 这是「调整菜单」的主路径。以前改一道菜要把家长的一句话丢回 agent 整份重生成，
	// 慢且三餐全变；但 agent 在推理时本来就权衡过好几个方案，让它顺手把落选的一起
	// 吐出来，就把「再问一次模型」变成了「翻一下已经发到手里的牌」。
	// 模型漏填不阻断登记：前端拿到空数组就不显示切换入口。
	Alternatives []Dish `json:"alternatives"`
	Reason       string `json:"reason"` // 这餐这样搭配的简短理由，可空串
	// Applied：这餐已被家长（可能编辑后）采纳入库。propose_menu 登记时恒为 false，
	// 采纳成功后由 HTTP 层回写简报缓存（连同编辑后的菜品）——重进 App 拉到的简报
	// 才能既显示家长实际采纳的版本、又保住「已采纳」徽章，编辑不丢。
	Applied bool `json:"applied,omitempty"`
}

// MenuSink 是请求级的结构化菜单收集器。带锁纯属防御——一次生成里 agent 理论上
// 只调一次 propose_menu，但多调/并发也不炸（后写覆盖）。
type MenuSink struct {
	mu   sync.Mutex
	menu *RecommendedMenu
}

type menuSinkKey struct{}

// WithMenuSink 往 ctx 挂一个收集器。用法：
//
//	ctx, sink := menu.WithMenuSink(ctx)
//	msg, _ := agent.Generate(ctx, ...)
//	structured := sink.Get() // agent 没调 propose_menu 则 nil
func WithMenuSink(ctx context.Context) (context.Context, *MenuSink) {
	s := &MenuSink{}
	return context.WithValue(ctx, menuSinkKey{}, s), s
}

func (s *MenuSink) set(m *RecommendedMenu) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.menu = m
}

// Get 返回收集到的结构化菜单；agent 本轮没调 propose_menu 则返回 nil。
func (s *MenuSink) Get() *RecommendedMenu {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.menu
}

func menuSinkFrom(ctx context.Context) *MenuSink {
	s, _ := ctx.Value(menuSinkKey{}).(*MenuSink)
	return s
}

// ---- 单道菜收集器（菜品级替换用）----

// DishSink 和 MenuSink 是同一套路数，只是收一道菜。
//
// 为什么值得单独有一条路：家长嫌某道菜不合适时，原来只能把一句话丢回 agent
// 【整份重生成】——三餐全变、跑一圈 ReAct、等几十秒，就为了换一道菜。
// 备选菜（Alternatives）吃掉了大部分这种需求，但备选都不满意时仍需要兜底，
// 而那时候要重算的也只是一道菜，不该再赔上整天的安排。
type DishSink struct {
	mu   sync.Mutex
	dish *Dish
}

type dishSinkKey struct{}

// WithDishSink 往 ctx 挂一个单菜收集器。用法同 WithMenuSink。
func WithDishSink(ctx context.Context) (context.Context, *DishSink) {
	s := &DishSink{}
	return context.WithValue(ctx, dishSinkKey{}, s), s
}

func (s *DishSink) set(d *Dish) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dish = d
}

// Get 返回收集到的菜；agent 本轮没调 propose_dish 则 nil。
func (s *DishSink) Get() *Dish {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dish
}

func dishSinkFrom(ctx context.Context) *DishSink {
	s, _ := ctx.Value(dishSinkKey{}).(*DishSink)
	return s
}

type proposeDishInput struct {
	Name   string            `json:"name" jsonschema:"description=新菜的菜名,required"`
	Detail string            `json:"detail" jsonschema:"description=做法/分量要点,required"`
	Uses   []ingredientUseIn `json:"uses" jsonschema:"description=这道菜会用掉的【家庭库存】食材（只列 list_inventory 里真有的；用不到库存就不传）。家长采纳这一餐时会照它自动出库"`
}

// makeProposeDish 返回 propose_dish 工具闭包。和 propose_menu 一样【不写任何账本】，
// 只把一道菜登记进 ctx 里的 DishSink，由 HTTP 层替换掉简报里的那一道。
func makeProposeDish() func(context.Context, proposeDishInput) (string, error) {
	return func(ctx context.Context, in proposeDishInput) (string, error) {
		toolLog(ctx, "propose_dish(name=%s uses=%d)", in.Name, len(in.Uses))

		name := strings.TrimSpace(in.Name)
		if name == "" {
			return "propose_dish 失败：菜名不能为空，请给出具体的一道菜。", nil
		}
		// 复用 toDishes 的清洗（空名整条丢、份数非正的 uses 丢），口径和 propose_menu 一致。
		dishes := toDishes([]proposedDishIn{{Name: name, Detail: in.Detail, Uses: in.Uses}})
		if len(dishes) == 0 {
			return "propose_dish 失败：菜名不能为空，请给出具体的一道菜。", nil
		}

		sink := dishSinkFrom(ctx)
		if sink == nil {
			// 没挂收集器说明这不是「换一道菜」的流程——别让模型以为自己换成了。
			return "（本轮不支持登记单道菜，请照常用文字回答家长。）", nil
		}
		sink.set(&dishes[0])
		return fmt.Sprintf("已登记新菜「%s」，家长端会替换掉原来那道。现在用一两句话告诉家长换成了什么、为什么。", dishes[0].Name), nil
	}
}

// ---- propose_menu 工具 ----

type proposeMenuInput struct {
	Date  string           `json:"date" jsonschema:"description=推荐针对的日期 YYYY-MM-DD（今天/明天按上下文里的今天换算）,required"`
	Meals []proposedMealIn `json:"meals" jsonschema:"description=推荐的各餐（午餐/水果/晚餐），每餐带餐别、时间、菜品,required"`
}

type proposedMealIn struct {
	Meal         string           `json:"meal" jsonschema:"description=餐别，只能是 lunch/fruit/dinner 之一,required"`
	Time         string           `json:"time" jsonschema:"description=建议用餐时刻如 12:00，可不传"`
	Dishes       []proposedDishIn `json:"dishes" jsonschema:"description=这一餐【主推】的菜品清单,required"`
	Alternatives []proposedDishIn `json:"alternatives" jsonschema:"description=这一餐的备选菜【正好 2 道】。家长不满意主推时会直接换上去，所以每道都必须是能独立顶替主推的完整菜、同样满足时令和库存、同样不和最近几天重样——不要给「主推的换个做法」这种变体,required"`
	Reason       string           `json:"reason" jsonschema:"description=这餐这样搭配的简短理由，给家长看，可不传"`
}

// proposedDishIn 是推荐里的一道菜。比记餐的 dishInput 多一个 uses——
// 记餐是「已经吃过了」，推荐是「打算这么吃」，只有后者需要预告吃掉哪些库存。
type proposedDishIn struct {
	Name   string            `json:"name" jsonschema:"description=菜名,required"`
	Detail string            `json:"detail" jsonschema:"description=做法/分量要点，没有可不传"`
	Uses   []ingredientUseIn `json:"uses" jsonschema:"description=这道菜会用掉的【家庭库存】食材（只列 list_inventory 里真有的；用不到库存的菜不传）。家长采纳这一餐时会照它自动出库，所以按实际会吃掉的量填"`
}

type ingredientUseIn struct {
	Name string  `json:"name" jsonschema:"description=库存里的食材名，要和 list_inventory 显示的名字一字不差,required"`
	Qty  float64 `json:"qty" jsonschema:"description=这道菜用掉多少份（0.5 表示半份）。拿不准就填 1,required"`
}

// toDishes 把工具参数里的菜品转成领域 Dish，顺手清洗。
// 清洗策略沿用本文件的一贯口径——单格坏数据只丢那一格，绝不因此让整次登记失败：
// 空菜名整条丢弃；份数非正的 uses 丢弃（宁可少扣一样，也不能把账扣错）。
func toDishes(in []proposedDishIn) []Dish {
	out := make([]Dish, 0, len(in))
	for _, d := range in {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			continue
		}
		dish := Dish{Name: name, Detail: strings.TrimSpace(d.Detail)}
		for _, u := range d.Uses {
			un := strings.TrimSpace(u.Name)
			if un == "" || u.Qty <= 0 {
				continue
			}
			dish.Uses = append(dish.Uses, IngredientUse{Name: un, Qty: u.Qty})
		}
		out = append(out, dish)
	}
	return out
}

// MergeUses 把一餐里所有菜的 Uses 合并成一张出库清单（同名累加）。
// 采纳一餐时按它一次性扣账——两道菜都用到西兰花就该扣两份，分开扣会因为
// 「扣完第一次就出清」而少扣。
func MergeUses(dishes []Dish) []IngredientUse {
	idx := make(map[string]int, 4)
	out := make([]IngredientUse, 0, 4)
	for _, d := range dishes {
		for _, u := range d.Uses {
			if i, ok := idx[u.Name]; ok {
				out[i].Qty += u.Qty
				continue
			}
			idx[u.Name] = len(out)
			out = append(out, u)
		}
	}
	return out
}

// makeProposeMenu 返回 propose_menu 工具闭包。它【不写任何账本】——只把结构化菜单
// 登记进 ctx 里的 MenuSink，供 HTTP 层随简报下发。真正入库是家长在前端编辑后点「应用」，
// 走 /api/history/apply（等价于 record_meal，但由家长确认后触发，不是 agent 自作主张）。
func makeProposeMenu() func(context.Context, proposeMenuInput) (string, error) {
	return func(ctx context.Context, in proposeMenuInput) (string, error) {
		toolLog(ctx, "propose_menu(date=%s meals=%d)", in.Date, len(in.Meals))

		// 日期必须是具体的 YYYY-MM-DD——和 record_meal 一样把关。否则「今天」「2026/07/09」这类
		// 会污染历史：HistoryStore 按字符串升序存、recent_meals 取尾部当「最近」，非 ISO 串会
		// 排到最后被永久当成最新的一天。返回人话错误让模型自己换算重试。
		date := strings.TrimSpace(in.Date)
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Sprintf("propose_menu 失败：日期 %q 不是 YYYY-MM-DD 格式，请按今天的日期换算成具体日期再登记。", in.Date), nil
		}

		rm := &RecommendedMenu{Date: date}
		seen := map[string]bool{} // 同一餐别只登记一次——前端按餐别唯一渲染/采纳，重复会串卡片
		for _, pm := range in.Meals {
			if !validMealField(pm.Meal) {
				return fmt.Sprintf("propose_menu 失败：餐别 %q 无效，只能是 lunch/fruit/dinner。", pm.Meal), nil
			}
			if seen[pm.Meal] {
				continue
			}
			dishes := toDishes(pm.Dishes)
			if len(dishes) == 0 {
				continue
			}
			seen[pm.Meal] = true
			rm.Meals = append(rm.Meals, ProposedMeal{
				Meal: pm.Meal,
				Time: strings.TrimSpace(pm.Time),
				Dishes: dishes,
				// 备选缺失不阻断登记：模型漏填是常事，但主推是好的就不该整餐作废。
				Alternatives: toDishes(pm.Alternatives),
				Reason:       strings.TrimSpace(pm.Reason),
			})
		}
		if len(rm.Meals) == 0 {
			return "propose_menu 失败：没有有效的餐或菜品，请带上具体餐别和至少一道菜。", nil
		}

		// 没有收集器（如聊天流程没挂 sink）——结构化卡片这条路暂不支持，别让模型
		// 对家长承诺「有可编辑卡片」；让它照常用文字把这份推荐完整写出来。
		sink := menuSinkFrom(ctx)
		if sink == nil {
			return "（本轮无法登记为可编辑卡片，请照常用文字把这份推荐完整写给家长。）", nil
		}
		sink.set(rm)
		// 回执里点名备选数：模型漏填时它自己能从回执看出来，下次登记会补上。
		alts := 0
		for _, m := range rm.Meals {
			alts += len(m.Alternatives)
		}
		return fmt.Sprintf("已登记结构化菜单（%d 餐、%d 道备选，家长端会显示为可编辑卡片）。"+
			"现在请继续照常输出面向家长的文字简报——正文只写主推的菜，备选不用写进正文，家长在卡片上自己换。",
			len(rm.Meals), alts), nil
	}
}
