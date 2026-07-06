package main

// L1 轨迹回灌 + L2 服务端会话的离线测试：备忘的累积/截断、注入规则（只回灌最近一条）、
// 会话的存取/过期/裁剪、录制器的收录规则。不碰网络不碰模型，`go test ./...` 始终安全。

import (
	"strings"
	"testing"
	"time"

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

// ---- L2 服务端会话 ----

func TestSessionStore_MissHitAndCopy(t *testing.T) {
	st := newSessionStore(time.Minute)

	// 未知 id / 空 id 都应 miss。
	if _, ok := st.get("nope"); ok {
		t.Error("未知会话应 miss")
	}
	if _, ok := st.get(""); ok {
		t.Error("空 id 应 miss")
	}

	st.append("s1", []*schema.Message{schema.UserMessage("u1"), schema.AssistantMessage("a1", nil)})
	hist, ok := st.get("s1")
	if !ok || len(hist) != 2 {
		t.Fatalf("应命中且有 2 条，实际 ok=%v len=%d", ok, len(hist))
	}

	// get 返回的是副本：调用方 append 本轮输入不该污染库里的历史。
	_ = append(hist, schema.UserMessage("polluted"))
	hist2, _ := st.get("s1")
	if len(hist2) != 2 {
		t.Errorf("库里的历史被调用方 append 污染了，长度 %d", len(hist2))
	}
}

func TestSessionStore_ExpiryAndSweep(t *testing.T) {
	st := newSessionStore(time.Minute)
	fake := time.Date(2026, 7, 6, 12, 0, 0, 0, time.Local)
	st.now = func() time.Time { return fake }

	st.append("s1", []*schema.Message{schema.UserMessage("u1")})

	// 时钟拨快 2 分钟（超过 1 分钟 TTL），get 应 miss 且顺手删掉。
	fake = fake.Add(2 * time.Minute)
	if _, ok := st.get("s1"); ok {
		t.Error("过期会话应 miss")
	}

	// sweep 清扫路径：再造一个然后拨快时钟。
	st.append("s2", []*schema.Message{schema.UserMessage("u2")})
	fake = fake.Add(2 * time.Minute)
	st.sweep()
	st.now = time.Now // 恢复真实时钟后仍应 miss（已被 sweep 删除）
	if _, ok := st.get("s2"); ok {
		t.Error("sweep 后过期会话应已删除")
	}
}

func TestTrimHistory(t *testing.T) {
	// 不超限原样返回。
	short := []*schema.Message{schema.UserMessage("u"), schema.AssistantMessage("a", nil)}
	if got := trimHistory(short); len(got) != 2 {
		t.Errorf("不超限不该裁剪，得到 %d 条", len(got))
	}

	// 超限时裁到 user 边界：构造 70 条 user/assistant 交替（偶数下标是 user）。
	var long []*schema.Message
	for i := 0; i < 35; i++ {
		long = append(long, schema.UserMessage("u"), schema.AssistantMessage("a", nil))
	}
	got := trimHistory(long)
	if len(got) > maxSessionMessages {
		t.Errorf("裁剪后仍超上限：%d 条", len(got))
	}
	if got[0].Role != schema.User {
		t.Errorf("裁剪点必须落在 user 消息上，实际第一条是 %v", got[0].Role)
	}

	// 全是 tool 消息（找不到 user 边界）→ 宁可不裁。
	var tools []*schema.Message
	for i := 0; i < 70; i++ {
		tools = append(tools, schema.ToolMessage("r", "id"))
	}
	if got := trimHistory(tools); len(got) != 70 {
		t.Errorf("找不到 user 边界时不该裁剪，得到 %d 条", len(got))
	}
}

func TestTurnRecorder(t *testing.T) {
	r := &turnRecorder{}

	// tool 结果：收。
	r.add(schema.ToolMessage("查到了", "call-1"))
	// 带工具调用的 assistant 轮：收，且剥掉 reasoning。
	withCall := schema.AssistantMessage("我先查查", []schema.ToolCall{{ID: "call-1"}})
	withCall.ReasoningContent = "内心戏一大段"
	r.add(withCall)
	// 纯文本 assistant（最终答案）：不收——由主流单独攒，避免重复。
	r.add(schema.AssistantMessage("最终答案", nil))
	// nil：不炸。
	r.add(nil)

	if len(r.msgs) != 2 {
		t.Fatalf("应只收 2 条（tool + 带调用的 assistant），实际 %d", len(r.msgs))
	}
	for _, m := range r.msgs {
		if m.ReasoningContent != "" {
			t.Error("落库消息不该保留 reasoning")
		}
	}
	// 剥 reasoning 是拷贝后剥，不该改到原消息。
	if withCall.ReasoningContent == "" {
		t.Error("recorder 不该修改原消息（应浅拷贝）")
	}
}

func TestNewSessionID(t *testing.T) {
	a, b := newSessionID(), newSessionID()
	if a == b || len(a) != 32 {
		t.Errorf("会话 id 应为 32 位 hex 且互不相同：%q %q", a, b)
	}
}
