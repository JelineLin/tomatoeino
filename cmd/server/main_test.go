package main

// L1 轨迹回灌的离线测试：备忘的累积/截断、注入规则（只回灌最近一条）。
// 不碰网络不碰模型，`go test ./...` 始终安全。

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTruncateRunes(t *testing.T) {
	// 不超长原样返回。
	if got := truncateRunes("短", 10); got != "短" {
		t.Errorf("不超长不该截断，得到 %q", got)
	}
	// 超长按「字符」截，不能把汉字剁成半个。
	got := truncateRunes("一二三四五", 3)
	if !strings.HasPrefix(got, "一二三") || !strings.Contains(got, "截断") {
		t.Errorf("应截为前 3 个字符并带截断标记，得到 %q", got)
	}
}

func TestTurnDigest(t *testing.T) {
	d := &turnDigest{}
	if d.String() != "" {
		t.Error("空备忘应为空串（没调工具的轮次不发 context 事件）")
	}

	d.addCall("recent_meals", `{"days":5}`)
	d.addResult("recent_meals", "最近 5 天的菜单：……")
	out := d.String()
	for _, want := range []string{"🔧 recent_meals", `{"days":5}`, "[recent_meals 结果]", "最近 5 天的菜单"} {
		if !strings.Contains(out, want) {
			t.Errorf("备忘缺少 %q，实际：%q", want, out)
		}
	}
}

func TestToEinoMessages_NoContext(t *testing.T) {
	in := []chatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	out := toEinoMessages(in)
	if len(out) != 3 {
		t.Fatalf("没有备忘时不该多出消息，期望 3 条得到 %d", len(out))
	}
	for _, m := range out {
		if m.Role == schema.System {
			t.Error("没有备忘时不该注入 system 消息")
		}
	}
	// 角色映射照旧。
	if out[0].Role != schema.User || out[1].Role != schema.Assistant || out[2].Role != schema.User {
		t.Errorf("角色映射错误：%v %v %v", out[0].Role, out[1].Role, out[2].Role)
	}
}

func TestToEinoMessages_InjectsLatestContextOnly(t *testing.T) {
	in := []chatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1", Context: "旧备忘"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2", Context: "新备忘"},
		{Role: "user", Content: "u3"},
	}
	out := toEinoMessages(in)

	// 5 条原始消息 + 1 条注入的 system 备忘 = 6。
	if len(out) != 6 {
		t.Fatalf("期望 6 条，得到 %d", len(out))
	}

	var sysCount int
	var sysIdx int
	for i, m := range out {
		if m.Role == schema.System {
			sysCount++
			sysIdx = i
		}
	}
	if sysCount != 1 {
		t.Fatalf("应只注入 1 条 system 备忘（最近的），实际 %d 条", sysCount)
	}
	// 注入位置：紧跟在 a2（原第 4 条，注入后下标 3）后面 → 下标 4。
	if sysIdx != 4 {
		t.Errorf("备忘应紧跟最近的助手消息，期望下标 4，实际 %d", sysIdx)
	}
	if !strings.Contains(out[sysIdx].Content, "新备忘") {
		t.Errorf("注入的应是最近一条备忘，实际：%q", out[sysIdx].Content)
	}
	if strings.Contains(out[sysIdx].Content, "旧备忘") {
		t.Error("旧备忘不该被注入（时效性 + 内容高度重叠）")
	}
	if !strings.Contains(out[sysIdx].Content, "工具备忘") {
		t.Errorf("注入的 system 消息应带【工具备忘】标头，实际：%q", out[sysIdx].Content)
	}
}
