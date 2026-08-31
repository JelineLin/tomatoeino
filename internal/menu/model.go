// Package menu 是「幼儿备餐 agent」的业务核心：把 history.json（宝宝的吃饭历史）
// 装配成一个能自主检索、自主决策的 ReAct agent。
//
// 分层（刻意和 internal/llm、internal/vectorstore 解耦）：
//   - model.go ：history.json 的领域类型 + 读盘（本文件）
//   - docs.go  ：把领域对象摊平成 schema.Document，灌进向量库
//   - tools.go ：把「查历史」的几种能力包成 eino 工具，交给模型自主调用
//   - agent.go ：把 embedder / 向量库 / 模型 / 工具 装配成 ReAct agent
//
// 为什么单独成包、不写死在 cmd/server 里：HTTP 服务和命令行 demo 都复用它，
// 就像把「清算核心逻辑」沉到一个领域包，对外可以套 RPC 服务，也可以跑批处理脚本。
//
// 路线图：数据源按「形态」逐个接入，每个都只是再加一个工具丢进 tools.go，
// agent 编排不用改。已接：静态知识库（history.json→向量库）、计算型（season.go 时令表）；
// 待接：实时刷新型（超市当天有什么）、有状态读写型（家庭库存）。
package menu

import (
	"encoding/json"
	"fmt"
	"os"
)

// Day 是一天的备餐记录。三餐都是指针：因为历史里确实有「只记了水果+晚餐、没有午餐」
// 的日子（如 2025-12-19），用指针区分「这一餐没有」(nil) 和「这一餐是空的」。
type Day struct {
	Date   string `json:"date"`
	Lunch  *Meal  `json:"lunch,omitempty"`
	Fruit  *Meal  `json:"fruit,omitempty"`
	Dinner *Meal  `json:"dinner,omitempty"`
}

// Meal 是某一餐：几点吃、吃了哪些菜，以及可选的「儿童食用反馈」。
type Meal struct {
	Time     string    `json:"time"`
	Dishes   []Dish    `json:"dishes"`
	Feedback *Feedback `json:"feedback,omitempty"` // 家长给这一餐的反馈；nil=还没反馈（omitempty 兼容旧数据）
}

// Feedback 是家长记的「儿童食用反馈」——爱吃/不爱吃/一般 + 可选备注。
// 现挂在 Dish 上（菜级粒度：一餐里粥爱吃、青菜不爱吃是常态，餐级一刀切分不清功过）；
// Meal 上的同名字段保留是为了兼容旧数据（只读展示，新反馈一律记到菜上）。
// agent 建议时读到并按频率权衡：不爱吃的降低出现频率但不完全排除，爱吃的适当多安排。
type Feedback struct {
	Rating string `json:"rating"`         // like（爱吃）/ dislike（不爱吃）/ ok（一般）
	Note   string `json:"note,omitempty"` // 备注，如「只吃了几口」「换个做法就行」
}

// Dish 是一道菜：菜名 + 做法/分量明细 + 可选的菜级食用反馈。
type Dish struct {
	Name     string    `json:"name"`
	Detail   string    `json:"detail"`
	Feedback *Feedback `json:"feedback,omitempty"` // nil=还没反馈（omitempty 兼容旧数据）
	// Uses 是这道菜会吃掉哪几样家庭库存，由 propose_menu 在推荐时一并登记。
	// 家长采纳这一餐时按它自动出库（见 InventoryStore.ConsumeAll）。
	// omitempty：历史里的旧菜没有这个字段，聊天里 record_meal 记的餐也不填。
	Uses []IngredientUse `json:"uses,omitempty"`
}

// IngredientUse 是「这道菜用掉哪样库存、用掉多少」。
//
// 关键设计：扣减依据在【推荐生成的那一刻】就确定，而不是采纳时再让模型猜一遍。
// agent 本来就是照着 list_inventory 的结果配的菜，那一刻它最清楚这道菜吃掉的是
// 账上的哪几样；等到采纳时再推断，等于把已经有的信息丢掉再花一次 token 找回来。
//
// 不带单位：单位以账本里已有的为准（Consume 只认名字 + 份数）。让模型少填一个字段，
// 也就不会出现「账上记的是块、推荐说的是克」这种对不上的情况。
type IngredientUse struct {
	Name string  `json:"name"`
	Qty  float64 `json:"qty"`
}

// LoadHistory 读 history.json 反序列化成 []Day。
//
// 故意只做「读盘 + 解析」这一件事，不掺向量化/检索——读数据和用数据分开，
// 测试时可以塞一份小 JSON 进来，不必碰真实文件和真实 API。
func LoadHistory(path string) ([]Day, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取历史文件 %s 失败: %w", path, err)
	}

	var days []Day
	if err := json.Unmarshal(raw, &days); err != nil {
		return nil, fmt.Errorf("解析历史文件 %s 失败: %w", path, err)
	}
	return days, nil
}
