package menu

import (
	"context"
	"strings"
	"testing"
)

// callProposeMenu 直接调 propose_menu 工具闭包，并把一个 sink 挂进 ctx，
// 返回给模型的文案 + sink 收到的结构化菜单。
func callProposeMenu(t *testing.T, in proposeMenuInput) (string, *RecommendedMenu) {
	t.Helper()
	ctx, sink := WithMenuSink(context.Background())
	msg, err := makeProposeMenu()(ctx, in)
	if err != nil {
		t.Fatalf("propose_menu 不该返回 error（校验失败应走人话文案）：%v", err)
	}
	return msg, sink.Get()
}

// 日期必须是具体 YYYY-MM-DD——审查抓到的坑：非 ISO 串会污染历史的字符串升序。
func TestProposeMenu_RejectsNonISODate(t *testing.T) {
	for _, bad := range []string{"今天", "2026/07/09", "", "2026-13-40"} {
		msg, menu := callProposeMenu(t, proposeMenuInput{
			Date:  bad,
			Meals: []proposedMealIn{{Meal: "lunch", Dishes: []proposedDishIn{{Name: "面"}}}},
		})
		if menu != nil {
			t.Errorf("日期 %q 非法时不该登记菜单，却拿到 %+v", bad, menu)
		}
		if !strings.Contains(msg, "失败") {
			t.Errorf("日期 %q 非法时文案应提示失败，实际：%s", bad, msg)
		}
	}
}

// 同一餐别只登记一次——否则前端按餐别唯一渲染/采纳时会串卡片。
func TestProposeMenu_DedupsMealField(t *testing.T) {
	_, menu := callProposeMenu(t, proposeMenuInput{
		Date: "2026-07-09",
		Meals: []proposedMealIn{
			{Meal: "dinner", Dishes: []proposedDishIn{{Name: "鳕鱼羹"}}},
			{Meal: "dinner", Dishes: []proposedDishIn{{Name: "冬瓜汤"}}}, // 重复餐别，应被跳过
			{Meal: "lunch", Dishes: []proposedDishIn{{Name: "丝瓜软饭"}}},
		},
	})
	if menu == nil {
		t.Fatal("有效输入却没登记菜单")
	}
	seen := map[string]int{}
	for _, m := range menu.Meals {
		seen[m.Meal]++
	}
	if seen["dinner"] != 1 {
		t.Errorf("dinner 应去重成 1 条，实际 %d 条", seen["dinner"])
	}
	// 保留的应是第一条（鳕鱼羹）。
	for _, m := range menu.Meals {
		if m.Meal == "dinner" && (len(m.Dishes) == 0 || m.Dishes[0].Name != "鳕鱼羹") {
			t.Errorf("去重应保留首条 dinner（鳕鱼羹），实际 %+v", m.Dishes)
		}
	}
}

// 没挂 sink（如聊天流程）时：不登记、也不谎称有卡片。
func TestProposeMenu_NoSinkDoesNotClaimCard(t *testing.T) {
	msg, err := makeProposeMenu()(context.Background(), proposeMenuInput{
		Date:  "2026-07-09",
		Meals: []proposedMealIn{{Meal: "lunch", Dishes: []proposedDishIn{{Name: "面"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "可编辑卡片") && !strings.Contains(msg, "无法") {
		t.Errorf("无 sink 时不该承诺有可编辑卡片，实际：%s", msg)
	}
}

// 备选菜要原样登记下来——它是「换菜」的全部依据，丢了前端就只能回头再问一次模型。
func TestProposeMenu_KeepsAlternatives(t *testing.T) {
	_, m := callProposeMenu(t, proposeMenuInput{
		Date: "2026-07-09",
		Meals: []proposedMealIn{{
			Meal:   "lunch",
			Dishes: []proposedDishIn{{Name: "丝瓜软饭"}},
			Alternatives: []proposedDishIn{
				{Name: "冬瓜虾仁面", Detail: "一小碗"},
				{Name: "番茄鸡蛋疙瘩汤"},
				{Name: ""}, // 空名应被丢掉，不占备选位
			},
		}},
	})
	if m == nil {
		t.Fatal("有效输入却没登记菜单")
	}
	alts := m.Meals[0].Alternatives
	if len(alts) != 2 {
		t.Fatalf("空名备选应被丢弃后剩 2 道，实际 %d 道：%+v", len(alts), alts)
	}
	if alts[0].Name != "冬瓜虾仁面" || alts[0].Detail != "一小碗" {
		t.Errorf("备选内容串了：%+v", alts[0])
	}
}

// 模型漏填备选不该让整餐作废——主推是好的就得留下，前端拿到空数组不显示切换入口即可。
func TestProposeMenu_MissingAlternativesStillRegisters(t *testing.T) {
	_, m := callProposeMenu(t, proposeMenuInput{
		Date:  "2026-07-09",
		Meals: []proposedMealIn{{Meal: "dinner", Dishes: []proposedDishIn{{Name: "鳕鱼羹"}}}},
	})
	if m == nil || len(m.Meals) != 1 {
		t.Fatalf("漏填备选时应照常登记，实际 %+v", m)
	}
	if len(m.Meals[0].Alternatives) != 0 {
		t.Errorf("没给备选时应为空，实际 %+v", m.Meals[0].Alternatives)
	}
}

// uses 是自动出库的依据：份数非正的格子必须丢掉——宁可少扣一样，也不能把账扣错。
func TestProposeMenu_DropsInvalidUses(t *testing.T) {
	_, m := callProposeMenu(t, proposeMenuInput{
		Date: "2026-07-09",
		Meals: []proposedMealIn{{
			Meal: "dinner",
			Dishes: []proposedDishIn{{
				Name: "鳕鱼羹",
				Uses: []ingredientUseIn{
					{Name: "鳕鱼", Qty: 1},
					{Name: "西兰花", Qty: 0},  // 份数为 0，丢
					{Name: "  ", Qty: 2},    // 空名，丢
					{Name: "胡萝卜", Qty: -1}, // 负数，丢
				},
			}},
		}},
	})
	if m == nil {
		t.Fatal("有效输入却没登记菜单")
	}
	uses := m.Meals[0].Dishes[0].Uses
	if len(uses) != 1 || uses[0].Name != "鳕鱼" || uses[0].Qty != 1 {
		t.Errorf("只该留下鳕鱼 1 份，实际 %+v", uses)
	}
}

// 一餐里两道菜都用到同一样食材时必须累加——分开扣会因为「扣完第一次就出清」而少扣。
func TestMergeUses_SumsSameIngredient(t *testing.T) {
	merged := MergeUses([]Dish{
		{Name: "西兰花泥", Uses: []IngredientUse{{Name: "西兰花", Qty: 0.5}}},
		{Name: "杂蔬软饭", Uses: []IngredientUse{{Name: "西兰花", Qty: 0.5}, {Name: "胡萝卜", Qty: 1}}},
		{Name: "白灼菜心"}, // 没有 uses，不该影响合并
	})
	got := map[string]float64{}
	for _, u := range merged {
		got[u.Name] = u.Qty
	}
	if got["西兰花"] != 1 {
		t.Errorf("西兰花应合并成 1 份，实际 %v", got["西兰花"])
	}
	if got["胡萝卜"] != 1 || len(merged) != 2 {
		t.Errorf("合并结果不对：%+v", merged)
	}
}
