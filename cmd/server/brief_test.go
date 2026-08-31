package main

// brief.go 的离线测试：时钟解析、下次触发点计算、简报存取。
// 真正的生成链路要靠真实模型，归 live 冒烟管，这里只测纯函数。

import (
	"testing"
	"time"

	"tomatoeino/internal/menu"
)

func TestParseClock(t *testing.T) {
	cases := []struct {
		in     string
		hh, mm int
		ok     bool
	}{
		{"07:00", 7, 0, true},
		{"23:59", 23, 59, true},
		{" 7:30 ", 7, 30, true},
		{"off", 0, 0, false},
		{"OFF", 0, 0, false},
		{"", 0, 0, false},
		{"25:00", 0, 0, false}, // 小时越界
		{"07:60", 0, 0, false}, // 分钟越界
		{"0700", 0, 0, false},  // 没冒号
		{"aa:bb", 0, 0, false},
	}
	for _, c := range cases {
		hh, mm, ok := parseClock(c.in)
		if ok != c.ok || hh != c.hh || mm != c.mm {
			t.Errorf("parseClock(%q) = (%d,%d,%v)，期望 (%d,%d,%v)", c.in, hh, mm, ok, c.hh, c.mm, c.ok)
		}
	}
}

func TestNextRunAt(t *testing.T) {
	// 现在是 06:30，目标 07:00 → 今天。
	now := time.Date(2026, 7, 6, 6, 30, 0, 0, time.Local)
	next := nextRunAt(now, 7, 0)
	if next.Day() != 6 || next.Hour() != 7 {
		t.Errorf("还没到点应排今天 07:00，实际 %s", next)
	}

	// 现在是 08:00，目标 07:00 → 明天。
	now = time.Date(2026, 7, 6, 8, 0, 0, 0, time.Local)
	next = nextRunAt(now, 7, 0)
	if next.Day() != 7 || next.Hour() != 7 {
		t.Errorf("过点了应排明天 07:00，实际 %s", next)
	}

	// 正好压在触发点上 → 排明天（不要立刻再跑一次，防重复触发）。
	now = time.Date(2026, 7, 6, 7, 0, 0, 0, time.Local)
	next = nextRunAt(now, 7, 0)
	if next.Day() != 7 {
		t.Errorf("压点应排明天，实际 %s", next)
	}
}

func TestBriefStore(t *testing.T) {
	b := &briefStore{}
	if b.get() != nil {
		t.Error("初始应为空")
	}
	d := &dailyBrief{Date: "2026-07-06", Content: "简报内容"}
	b.set(d)
	if got := b.get(); got == nil || got.Date != "2026-07-06" {
		t.Errorf("存取不一致：%+v", got)
	}
}

// briefStore.replaceDish：换对位置、不污染旧快照、边界一律拒绝。
func TestBriefStore_ReplaceDish(t *testing.T) {
	newStore := func() *briefStore {
		b := &briefStore{}
		b.set(&dailyBrief{
			Date: "2026-08-10",
			Menu: &menu.RecommendedMenu{
				Date: "2026-08-10",
				Meals: []menu.ProposedMeal{
					{Meal: "lunch", Dishes: []menu.Dish{{Name: "番茄面"}, {Name: "炒西兰花"}}},
					{Meal: "dinner", Dishes: []menu.Dish{{Name: "鳕鱼羹"}}},
				},
			},
		})
		return b
	}

	// 换 lunch 的第 1 道，其余原样。
	b := newStore()
	before := b.get().Menu // 换之前的快照，下面要验证它没被就地改掉
	nm := b.replaceDish("2026-08-10", "lunch", 1, menu.Dish{Name: "蒸南瓜"})
	if nm == nil {
		t.Fatal("合法替换不该返回 nil")
	}
	if got := nm.Meals[0].Dishes[1].Name; got != "蒸南瓜" {
		t.Errorf("第 1 道应换成蒸南瓜，实际 %s", got)
	}
	if got := nm.Meals[0].Dishes[0].Name; got != "番茄面" {
		t.Errorf("第 0 道不该动，实际 %s", got)
	}
	if got := nm.Meals[1].Dishes[0].Name; got != "鳕鱼羹" {
		t.Errorf("dinner 不该动，实际 %s", got)
	}
	// 切片克隆：旧快照必须还是旧内容（copy 出来的 ProposedMeal 与旧的共用底层数组，
	// 不克隆 Dishes 就会把已经交出去的那份一起改掉）。
	if got := before.Meals[0].Dishes[1].Name; got != "炒西兰花" {
		t.Errorf("旧快照被就地改了：%s", got)
	}

	// 边界：日期对不上、餐别不存在、下标越界，一律不动。
	for _, c := range []struct {
		desc       string
		date, meal string
		idx        int
	}{
		{"日期对不上", "2026-08-11", "lunch", 0},
		{"餐别不存在", "2026-08-10", "breakfast", 0},
		{"下标越界", "2026-08-10", "lunch", 2},
		{"下标为负", "2026-08-10", "lunch", -1},
	} {
		b := newStore()
		if got := b.replaceDish(c.date, c.meal, c.idx, menu.Dish{Name: "X"}); got != nil {
			t.Errorf("%s 时应返回 nil，实际换成了 %+v", c.desc, got.Meals)
		}
	}
}
