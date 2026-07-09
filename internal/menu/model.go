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

// Feedback 是家长给某一餐的「儿童食用反馈」——爱吃/不爱吃/一般 + 可选备注。
// 挂在 Meal 上（餐级粒度，对齐「一餐一条向量」）；agent 建议时会读到并权衡：
// 不爱吃的餐/做法尽量避开或改良，爱吃的可复现借鉴。
type Feedback struct {
	Rating string `json:"rating"`         // like（爱吃）/ dislike（不爱吃）/ ok（一般）
	Note   string `json:"note,omitempty"` // 备注，如「只吃了几口」「换个做法就行」
}

// Dish 是一道菜：菜名 + 做法/分量明细。
type Dish struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
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
