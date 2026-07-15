package menu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 造一餐的快捷方式。
func mealOfDishes(dishes ...Dish) *Meal {
	return &Meal{Time: "12:00", Dishes: dishes}
}

func fb(rating string) *Feedback { return &Feedback{Rating: rating} }

func TestBuildPrefRules_菜级优先_餐级摊派(t *testing.T) {
	days := []Day{
		{
			Date: "2026-07-01",
			// 餐级 dislike（旧数据形态）：摊给没有菜级反馈的「白粥」；
			// 「清蒸鳕鱼」自带菜级 like，以菜级为准、不重复计。
			Lunch: &Meal{
				Time:     "12:00",
				Feedback: fb("dislike"),
				Dishes: []Dish{
					{Name: "清蒸鳕鱼", Feedback: fb("like")},
					{Name: "白粥"},
				},
			},
		},
		{
			Date:  "2026-07-02",
			Lunch: mealOfDishes(Dish{Name: "清蒸鳕鱼", Feedback: fb("like")}),
		},
	}

	rules := BuildPrefRules(days)
	byName := map[string]PrefRule{}
	for _, r := range rules {
		byName[r.Name] = r
	}

	if got := byName["清蒸鳕鱼"]; got.Likes != 2 || got.Dislikes != 0 {
		t.Fatalf("清蒸鳕鱼应为 2 like / 0 dislike，got %+v", got)
	}
	if got := byName["白粥"]; got.Dislikes != 1 {
		t.Fatalf("白粥应从餐级摊到 1 dislike，got %+v", got)
	}
	// 产品口径：不拉黑。dislike 的建议必须是「降频」而非「排除」。
	if a := byName["白粥"].Advice; !strings.Contains(a, "降低出现频率") || !strings.Contains(a, "不必完全排除") {
		t.Fatalf("dislike 建议措辞必须是降频不拉黑，got %q", a)
	}
	if a := byName["清蒸鳕鱼"].Advice; !strings.Contains(a, "多安排") {
		t.Fatalf("like 建议应为适当多安排，got %q", a)
	}
}

func TestBuildPrefRules_截断到上限(t *testing.T) {
	var days []Day
	// 造 maxPrefRules+5 道各带一条反馈的菜。
	for i := 0; i < maxPrefRules+5; i++ {
		days = append(days, Day{
			Date:  "2026-07-01",
			Lunch: mealOfDishes(Dish{Name: strings.Repeat("菜", i+1), Feedback: fb("like")}),
		})
	}
	if got := len(BuildPrefRules(days)); got != maxPrefRules {
		t.Fatalf("规则条数应截断到 %d，got %d", maxPrefRules, got)
	}
}

func TestRenderPrefRules_两行结论(t *testing.T) {
	days := []Day{
		{Date: "2026-07-01", Lunch: mealOfDishes(
			Dish{Name: "南瓜粥", Feedback: fb("like")},
			Dish{Name: "西兰花", Feedback: fb("dislike")},
		)},
	}
	got := renderPrefRules(days)
	for _, want := range []string{"南瓜粥", "西兰花", "不必完全排除", "提高", "口味偏好"} {
		if !strings.Contains(got, want) {
			t.Fatalf("注入摘要缺少 %q：%s", want, got)
		}
	}
	if renderPrefRules(nil) != "" {
		t.Fatal("无反馈时应返回空串（不注入）")
	}
}

func TestSetDishFeedback_快照不变量与结转(t *testing.T) {
	dir := t.TempDir()
	hs, err := NewHistoryStore(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := hs.SetMeal("2026-07-01", "lunch", Meal{
		Time:   "12:00",
		Dishes: []Dish{{Name: "清蒸鳕鱼"}, {Name: "白粥"}},
	}); err != nil {
		t.Fatal(err)
	}

	before := hs.Snapshot() // 写入前的快照，事后验证没被写脏

	m, err := hs.SetDishFeedback("2026-07-01", "lunch", "清蒸鳕鱼", fb("like"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Dishes[0].Feedback == nil || m.Dishes[0].Feedback.Rating != "like" {
		t.Fatalf("返回的餐应带上菜级反馈，got %+v", m.Dishes[0])
	}
	if before[0].Lunch.Dishes[0].Feedback != nil {
		t.Fatal("旧快照被写脏了——SetDishFeedback 必须克隆 Dishes 切片")
	}

	// 菜名不存在：报错并列出候选。
	if _, err := hs.SetDishFeedback("2026-07-01", "lunch", "不存在的菜", fb("like")); err == nil ||
		!strings.Contains(err.Error(), "清蒸鳕鱼") {
		t.Fatalf("菜名不存在应报错并列出这餐的菜名，got %v", err)
	}

	// 整餐覆盖（改分量）：同名菜的菜级反馈按名结转，新菜不带。
	stored, _, err := hs.SetMeal("2026-07-01", "lunch", Meal{
		Time:   "12:30",
		Dishes: []Dish{{Name: "清蒸鳕鱼", Detail: "加量"}, {Name: "米饭"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Dishes[0].Feedback == nil || stored.Dishes[0].Feedback.Rating != "like" {
		t.Fatal("整餐覆盖应按菜名结转菜级反馈")
	}
	if stored.Dishes[1].Feedback != nil {
		t.Fatal("新菜不该凭空带上反馈")
	}

	// 落盘可回读。
	raw, _ := os.ReadFile(filepath.Join(dir, "history.json"))
	if !strings.Contains(string(raw), `"feedback"`) {
		t.Fatal("菜级反馈应已落盘")
	}
}

func TestRenderMeal_菜级反馈标注(t *testing.T) {
	m := &Meal{Time: "12:00", Dishes: []Dish{
		{Name: "清蒸鳕鱼", Detail: "去刺", Feedback: &Feedback{Rating: "like", Note: "吃光了"}},
		{Name: "白粥"},
	}}
	got := renderMeal("2026-07-01", "午餐", m)
	if !strings.Contains(got, "清蒸鳕鱼（去刺）〔宝宝爱吃：吃光了〕") {
		t.Fatalf("菜级反馈应紧跟那道菜渲染，got %s", got)
	}
	if strings.Contains(got, "白粥〔") {
		t.Fatalf("没反馈的菜不该有标注，got %s", got)
	}
}
