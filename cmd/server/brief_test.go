package main

// brief.go 的离线测试：时钟解析、下次触发点计算、简报存取。
// 真正的生成链路要靠真实模型，归 live 冒烟管，这里只测纯函数。

import (
	"testing"
	"time"
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
