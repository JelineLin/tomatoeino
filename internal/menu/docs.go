package menu

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// mealKind 标记一餐属于哪一类，既用于人话渲染，也写进 doc 的 MetaData 备查。
type mealKind struct {
	field string // 程序内部用的键："lunch" / "fruit" / "dinner"
	label string // 给人/模型看的中文："午餐" / "水果" / "晚餐"
}

// mealOrder 固定三餐的呈现顺序——午餐、水果、晚餐，和 history.json 的时间线一致。
var mealOrder = []mealKind{
	{field: "lunch", label: "午餐"},
	{field: "fruit", label: "水果"},
	{field: "dinner", label: "晚餐"},
}

// mealOf 按 field 取出某天对应的那一餐（可能为 nil）。
func (d Day) mealOf(field string) *Meal {
	switch field {
	case "lunch":
		return d.Lunch
	case "fruit":
		return d.Fruit
	case "dinner":
		return d.Dinner
	default:
		return nil
	}
}

// setMeal 按 field 放入某天对应的那一餐。换指针、不改旧 Meal 内容——
// HistoryStore.Snapshot 浅拷贝的一致性依赖这一点。未知 field 静默忽略，
// 合法性由上游 validMealField 把关。
func (d *Day) setMeal(field string, m *Meal) {
	switch field {
	case "lunch":
		d.Lunch = m
	case "fruit":
		d.Fruit = m
	case "dinner":
		d.Dinner = m
	}
}

// validMealField 判断餐别是否合法（与 mealOrder 同源，不另列一份清单）。
func validMealField(field string) bool {
	for _, mk := range mealOrder {
		if mk.field == field {
			return true
		}
	}
	return false
}

// mealLabelOf 取餐别的中文标签（lunch→午餐），未知原样返回兜底。
func mealLabelOf(field string) string {
	for _, mk := range mealOrder {
		if mk.field == field {
			return mk.label
		}
	}
	return field
}

// renderMeal 把一餐渲染成一句可读文本，例如：
//
//	2025-12-26 晚餐(17:30)：煎三文鱼（1袋）；南瓜米饭（50g米，黄小米1小把）；...
//
// 这句话既是向量化的输入（喂给 embedder 算语义），也是工具直接返回给模型的内容——
// 让「存进库的」和「给模型看的」是同一种人话，调试时一眼能对上。
func renderMeal(date, label string, m *Meal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", date, label)
	if m.Time != "" {
		fmt.Fprintf(&b, "(%s)", m.Time)
	}
	b.WriteString("：")

	parts := make([]string, 0, len(m.Dishes))
	for _, dish := range m.Dishes {
		if dish.Detail != "" {
			parts = append(parts, fmt.Sprintf("%s（%s）", dish.Name, dish.Detail))
		} else {
			parts = append(parts, dish.Name)
		}
	}
	b.WriteString(strings.Join(parts, "；"))
	return b.String()
}

// BuildMealDocument 把「一餐」变成一条可入库的 Document。
//
// doc ID = date-mealField —— 这是和 HistoryStore.SetMeal 共用的同一把钥匙：
// record_meal 覆盖修正时，JSON 侧整餐替换、向量侧同 ID Upsert，两个视图不可能错位。
func BuildMealDocument(date, mealField string, m *Meal) *schema.Document {
	return &schema.Document{
		ID:      date + "-" + mealField,
		Content: renderMeal(date, mealLabelOf(mealField), m),
		MetaData: map[string]any{
			"date":     date,
			"mealType": mealField,
		},
	}
}

// BuildDocuments 把整段历史摊平成「一餐一条」的 schema.Document。
//
// 为什么一餐一条而不是一天一条：检索是「召回最相关的片段」，粒度越细命中越准——
// 问「最近吃过哪些鱼」时，希望召回的是「那一餐」，而不是「那一整天连水果带主食」。
// MetaData 里留下 date / mealType，下游想还原归属时取得到。
func BuildDocuments(days []Day) []*schema.Document {
	docs := make([]*schema.Document, 0, len(days)*len(mealOrder))
	for _, d := range days {
		for _, mk := range mealOrder {
			m := d.mealOf(mk.field)
			if m == nil || len(m.Dishes) == 0 {
				continue
			}
			docs = append(docs, BuildMealDocument(d.Date, mk.field, m))
		}
	}
	return docs
}
