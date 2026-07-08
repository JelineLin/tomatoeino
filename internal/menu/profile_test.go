package menu

// 档案（建档）的离线测试：store 往返、月龄计算、人设渲染、工具合并语义。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileStore_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	ps, err := NewProfileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ps.Get().IsEmpty() {
		t.Error("缺文件应是空档案")
	}

	want := Profile{BabyName: "小番茄", BirthDate: "2025-03-10", Allergies: []string{"鸡蛋", "芒果"}}
	if err := ps.Set(want); err != nil {
		t.Fatal(err)
	}
	// 重开读回。
	ps2, err := NewProfileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := ps2.Get()
	if got.BabyName != "小番茄" || got.BirthDate != "2025-03-10" || len(got.Allergies) != 2 {
		t.Errorf("往返不一致: %+v", got)
	}

	// Get 返回副本：外面改切片不脏账。
	got.Allergies[0] = "被污染"
	if ps2.Get().Allergies[0] != "鸡蛋" {
		t.Error("Get 应返回副本，账被外部修改污染了")
	}
}

func TestAgeMonths(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.Local)
	cases := []struct {
		birth string
		want  int
		ok    bool
	}{
		{"2025-03-10", 15, true}, // 差 2 天满 16 个月 → 15
		{"2025-07-08", 12, true}, // 恰好整年
		{"2026-07-01", 0, true},  // 当月出生
		{"2027-01-01", 0, false}, // 未来
		{"不是日期", 0, false},
	}
	for _, c := range cases {
		got, ok := ageMonths(c.birth, now)
		if ok != c.ok || got != c.want {
			t.Errorf("ageMonths(%q) = (%d,%v)，期望 (%d,%v)", c.birth, got, ok, c.want, c.ok)
		}
	}
}

func TestRenderProfile(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.Local)
	if renderProfile(Profile{}, now) != "" {
		t.Error("空档案不该渲染出内容（不注入人设）")
	}
	out := renderProfile(Profile{
		BirthDate: "2025-03-10",
		Allergies: []string{"鸡蛋", "芒果"},
		Dislikes:  []string{"香菜"},
	}, now)
	for _, want := range []string{"15 个月大", "鸡蛋、芒果", "绝对", "香菜"} {
		if !strings.Contains(out, want) {
			t.Errorf("渲染缺 %q：%q", want, out)
		}
	}
}

func TestUpdateProfileTool(t *testing.T) {
	ctx := context.Background()
	ps, _ := NewProfileStore(filepath.Join(t.TempDir(), "profile.json"))
	update := makeUpdateProfile(ps)

	// 建档。
	out, err := update(ctx, profileInput{BirthDate: "2025-03-10", Allergies: []string{"鸡蛋", " 芒果 "}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已更新宝宝档案") {
		t.Errorf("回执不对: %q", out)
	}
	if p := ps.Get(); p.Allergies[1] != "芒果" { // trim 生效
		t.Errorf("过敏原应去空白: %v", p.Allergies)
	}

	// 部分更新：只传 dislikes，生日/过敏原保持原值。
	if _, err := update(ctx, profileInput{Dislikes: []string{"香菜"}}); err != nil {
		t.Fatal(err)
	}
	p := ps.Get()
	if p.BirthDate != "2025-03-10" || len(p.Allergies) != 2 || len(p.Dislikes) != 1 {
		t.Errorf("部分更新不该动其它字段: %+v", p)
	}

	// 整组覆盖：过敏原换新组。
	if _, err := update(ctx, profileInput{Allergies: []string{"花生"}}); err != nil {
		t.Fatal(err)
	}
	if p := ps.Get(); len(p.Allergies) != 1 || p.Allergies[0] != "花生" {
		t.Errorf("数组应整组覆盖: %v", p.Allergies)
	}

	// 坏参数：人话回执不是 error。
	for _, in := range []profileInput{
		{BirthDate: "三月十号"},
		{BirthDate: "2999-01-01"},
		{}, // 空输入
	} {
		out, err := update(ctx, in)
		if err != nil {
			t.Errorf("坏参数应人话回执: %v", err)
		}
		if !strings.Contains(out, "建档失败") {
			t.Errorf("坏参数回执应说明原因: %q", out)
		}
	}
}
