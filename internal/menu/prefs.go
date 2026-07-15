package menu

// prefs.go —— 把散落在历史里的食用反馈归纳成「口味偏好规则」。
//
// 两个消费方，一份口径（和时令表「聊天里说的 = tab 里看的」是同一个纪律）：
//   - HTTP /api/profile：档案页展示「偏好规律」，家长能看到 agent 依据什么在调频率；
//   - agent 人设注入：每次请求把摘要写进 system prompt，推荐时直接生效——
//     不依赖检索恰好召回那几餐，频率调节是全局的。
//
// 归纳纪律（对齐产品口径「不要事无巨细」）：
//   - 按【菜名精确聚合】：同名菜的多次反馈合并成一条规则，数次数不数细节；
//   - 不爱吃 ≠ 拉黑：规则的建议措辞是「降低频率」，绝不生成「禁止」——
//     硬禁忌只有档案里的过敏原，那是另一个层级的约束；
//   - 有上限：展示/注入都截断到最活跃的若干条，防止反馈越攒越多把人设撑爆。

import (
	"fmt"
	"sort"
	"strings"
)

// PrefRule 是归纳出的一条口味规则：某道菜的反馈计数 + 一句给人看的建议。
type PrefRule struct {
	Name     string `json:"name"`     // 菜名
	Likes    int    `json:"likes"`    // 「爱吃」次数
	Dislikes int    `json:"dislikes"` // 「不爱吃」次数
	Oks      int    `json:"oks"`      // 「一般」次数
	Advice   string `json:"advice"`   // 人话建议（前端直接展示）
}

// maxPrefRules 限制归纳输出的条数——最活跃的排前面，尾部长尾砍掉（不要事无巨细）。
const maxPrefRules = 12

// BuildPrefRules 扫全量历史，按菜名聚合反馈，产出排序后的规则列表。
//
// 计数口径：菜级反馈按菜计；旧数据的餐级反馈视为「当时整餐的口碑」，
// 摊给这餐里【没有自己菜级反馈】的每道菜（有菜级的以菜级为准，不重复计）。
func BuildPrefRules(days []Day) []PrefRule {
	type agg struct {
		likes, dislikes, oks int
		lastSeen             string // 最近一次被反馈的日期，同活跃度时新的排前
	}
	byName := map[string]*agg{}

	count := func(name, rating, date string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		a := byName[name]
		if a == nil {
			a = &agg{}
			byName[name] = a
		}
		switch rating {
		case "like":
			a.likes++
		case "dislike":
			a.dislikes++
		case "ok":
			a.oks++
		default:
			return // 未知取值不计入规则（渲染层会兜底展示，这里保持口径干净）
		}
		if date > a.lastSeen {
			a.lastSeen = date
		}
	}

	for _, d := range days {
		for _, mk := range mealOrder {
			m := d.mealOf(mk.field)
			if m == nil {
				continue
			}
			for _, dish := range m.Dishes {
				switch {
				case dish.Feedback != nil:
					count(dish.Name, dish.Feedback.Rating, d.Date)
				case m.Feedback != nil: // 旧餐级反馈摊给没有菜级反馈的菜
					count(dish.Name, m.Feedback.Rating, d.Date)
				}
			}
		}
	}

	rules := make([]PrefRule, 0, len(byName))
	lastSeen := map[string]string{}
	for name, a := range byName {
		rules = append(rules, PrefRule{
			Name: name, Likes: a.likes, Dislikes: a.dislikes, Oks: a.oks,
			Advice: prefAdvice(a.likes, a.dislikes),
		})
		lastSeen[name] = a.lastSeen
	}
	// 活跃度（被反馈的总次数）降序，其次最近反馈的排前，最后按名字稳定兜底。
	sort.Slice(rules, func(i, j int) bool {
		ai := rules[i].Likes + rules[i].Dislikes + rules[i].Oks
		aj := rules[j].Likes + rules[j].Dislikes + rules[j].Oks
		if ai != aj {
			return ai > aj
		}
		if lastSeen[rules[i].Name] != lastSeen[rules[j].Name] {
			return lastSeen[rules[i].Name] > lastSeen[rules[j].Name]
		}
		return rules[i].Name < rules[j].Name
	})
	if len(rules) > maxPrefRules {
		rules = rules[:maxPrefRules]
	}
	return rules
}

// prefAdvice 由计数产出一句建议。措辞刻意「调频率、不拉黑」——这是产品口径，
// 前端展示和 agent 注入用的都是同一句话。
func prefAdvice(likes, dislikes int) string {
	switch {
	case likes > dislikes:
		return "爱吃，可适当多安排（保持多样，别顿顿都是）"
	case dislikes > likes:
		return "不太爱吃，降低出现频率、别连着推（可偶尔换做法再试，不必完全排除）"
	default:
		return "口碑不一，正常安排、留意观察"
	}
}

// renderPrefRules 把规则摘要渲染成注入人设的一段话；没有可归纳的反馈返回空串（不注入）。
// 只写「爱吃清单 / 不爱吃清单」两行结论，细节计数不进 prompt——模型要的是倾向，不是台账。
func renderPrefRules(days []Day) string {
	rules := BuildPrefRules(days)
	if len(rules) == 0 {
		return ""
	}
	var likes, dislikes []string
	for _, r := range rules {
		switch {
		case r.Likes > r.Dislikes:
			likes = append(likes, r.Name)
		case r.Dislikes > r.Likes:
			dislikes = append(dislikes, r.Name)
		}
	}
	if len(likes) == 0 && len(dislikes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【宝宝口味偏好】（依据家长的食用反馈自动归纳）")
	if len(likes) > 0 {
		fmt.Fprintf(&b, "爱吃：%s——可适当提高这些菜/做法的复现频率，但保持食材多样。", strings.Join(likes, "、"))
	}
	if len(dislikes) > 0 {
		fmt.Fprintf(&b, "不太爱吃：%s——降低出现频率、别连着推，但【不必完全排除】，可偶尔换个做法再试。", strings.Join(dislikes, "、"))
	}
	return b.String()
}
