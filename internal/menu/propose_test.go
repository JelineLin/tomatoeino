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
			Meals: []proposedMealIn{{Meal: "lunch", Dishes: []dishInput{{Name: "面"}}}},
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
			{Meal: "dinner", Dishes: []dishInput{{Name: "鳕鱼羹"}}},
			{Meal: "dinner", Dishes: []dishInput{{Name: "冬瓜汤"}}}, // 重复餐别，应被跳过
			{Meal: "lunch", Dishes: []dishInput{{Name: "丝瓜软饭"}}},
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
		Meals: []proposedMealIn{{Meal: "lunch", Dishes: []dishInput{{Name: "面"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "可编辑卡片") && !strings.Contains(msg, "无法") {
		t.Errorf("无 sink 时不该承诺有可编辑卡片，实际：%s", msg)
	}
}
