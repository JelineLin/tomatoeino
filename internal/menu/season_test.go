package menu

// season.go 的离线测试。时钟是注入的（makeSeasonal(now)），
// 所以能把「今天」拨到任意月份来断言，不用等真实季节轮转——
// 和清算测试里把交易日拨到月末/节假日验证日历逻辑是同一个玩法。

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fixedClock 返回一个永远指向 y 年 m 月的时钟。
func fixedClock(y int, m time.Month) func() time.Time {
	return func() time.Time {
		return time.Date(y, m, 15, 12, 0, 0, 0, time.Local)
	}
}

func TestSeasonTableComplete(t *testing.T) {
	// 全年 12 个月一条不缺，每条的四个字段都非空——
	// 表是纯数据，最容易在增删时漏一格，用测试兜住。
	for m := time.January; m <= time.December; m++ {
		e, ok := seasonTable[m]
		if !ok {
			t.Errorf("%d月 在时令表里缺席", int(m))
			continue
		}
		if len(e.Veg) == 0 || len(e.Fruit) == 0 || len(e.Aquatic) == 0 || e.Tip == "" {
			t.Errorf("%d月 的时令条目有空字段: %+v", int(m), e)
		}
	}
}

func TestMakeSeasonal_DefaultsToCurrentMonth(t *testing.T) {
	seasonal := makeSeasonal(fixedClock(2026, time.July))
	out, err := seasonal(context.Background(), seasonInput{Month: 0})
	if err != nil {
		t.Fatalf("seasonal 出错: %v", err)
	}
	if !strings.Contains(out, "7月时令") {
		t.Errorf("不传月份应默认当月(7月)，实际：%q", out)
	}
	// 7 月的招牌应季菜应该在列。
	if !strings.Contains(out, "丝瓜") {
		t.Errorf("7月应含丝瓜，实际：%q", out)
	}
}

func TestMakeSeasonal_ExplicitMonth(t *testing.T) {
	// 时钟在 7 月，但显式查 1 月——参数优先于时钟。
	seasonal := makeSeasonal(fixedClock(2026, time.July))
	out, err := seasonal(context.Background(), seasonInput{Month: 1})
	if err != nil {
		t.Fatalf("seasonal 出错: %v", err)
	}
	if !strings.Contains(out, "1月时令") {
		t.Errorf("显式传 month=1 应查 1 月，实际：%q", out)
	}
	if !strings.Contains(out, "白萝卜") {
		t.Errorf("1月应含白萝卜，实际：%q", out)
	}
}

func TestMakeSeasonal_InvalidMonth(t *testing.T) {
	seasonal := makeSeasonal(fixedClock(2026, time.July))
	out, err := seasonal(context.Background(), seasonInput{Month: 13})
	if err != nil {
		t.Fatalf("非法月份不该返回 error（要还给模型一句人话），却出错: %v", err)
	}
	if !strings.Contains(out, "不合法") {
		t.Errorf("month=13 应提示不合法，实际：%q", out)
	}
}

func TestRenderSeason(t *testing.T) {
	out := renderSeason(time.December, seasonTable[time.December])
	for _, want := range []string{"12月时令", "应季蔬菜", "应季水果", "应季水产", "备餐提示"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderSeason 缺少 %q，实际：%q", want, out)
		}
	}
}
