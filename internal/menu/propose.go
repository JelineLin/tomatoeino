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
// 让 iOS 端非可选解码省心（time/reason 空就给空串）。
type ProposedMeal struct {
	Meal   string `json:"meal"`   // lunch/fruit/dinner
	Time   string `json:"time"`   // 建议用餐时间，可空串
	Dishes []Dish `json:"dishes"` // 复用历史的 Dish{Name,Detail}
	Reason string `json:"reason"` // 这餐这样搭配的简短理由，可空串
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

// ---- propose_menu 工具 ----

type proposeMenuInput struct {
	Date  string           `json:"date" jsonschema:"description=推荐针对的日期 YYYY-MM-DD（今天/明天按上下文里的今天换算）,required"`
	Meals []proposedMealIn `json:"meals" jsonschema:"description=推荐的各餐（午餐/水果/晚餐），每餐带餐别、时间、菜品,required"`
}

type proposedMealIn struct {
	Meal   string      `json:"meal" jsonschema:"description=餐别，只能是 lunch/fruit/dinner 之一,required"`
	Time   string      `json:"time" jsonschema:"description=建议用餐时刻如 12:00，可不传"`
	Dishes []dishInput `json:"dishes" jsonschema:"description=这一餐的菜品清单（复用记餐的菜品结构）,required"`
	Reason string      `json:"reason" jsonschema:"description=这餐这样搭配的简短理由，给家长看，可不传"`
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
			dishes := make([]Dish, 0, len(pm.Dishes))
			for _, d := range pm.Dishes {
				name := strings.TrimSpace(d.Name)
				if name == "" {
					continue
				}
				dishes = append(dishes, Dish{Name: name, Detail: strings.TrimSpace(d.Detail)})
			}
			if len(dishes) == 0 {
				continue
			}
			seen[pm.Meal] = true
			rm.Meals = append(rm.Meals, ProposedMeal{
				Meal:   pm.Meal,
				Time:   strings.TrimSpace(pm.Time),
				Dishes: dishes,
				Reason: strings.TrimSpace(pm.Reason),
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
		return fmt.Sprintf("已登记结构化菜单（%d 餐，家长端会显示为可编辑卡片）。现在请继续照常输出面向家长的文字简报。", len(rm.Meals)), nil
	}
}
