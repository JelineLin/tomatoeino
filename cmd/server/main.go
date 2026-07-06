// 命令 server：把「幼儿备餐 ReAct agent」包成 HTTP 后端，给 iOS App 用。
//
// 三个端点：
//   - POST /api/chat    ：和 agent 对话，结果用 SSE 流式吐（像 example 01 的 Stream）
//   - GET  /api/history ：返回结构化历史菜单，给前端「历史」tab 渲染
//   - GET  /healthz     ：存活探针
//
// 必须从仓库根目录跑（默认历史路径是相对路径，且 internal/llm 在 init 里读根目录的 .env）：
//
//	go run ./cmd/server
//
// 需要在 .env 里配好 chat + embedding 凭证（见 CLAUDE.md / .env.example）。
// 没配 key 时会在启动建向量库这一步直接报错退出——这是预期行为，不是 bug。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"tomatoeino/internal/menu"
)

// defaultHistoryPath 是 history.json 相对仓库根目录的位置，可用 HISTORY_PATH 覆盖。
const defaultHistoryPath = "examples/02_menu_agent/data/history.json"

// server 持有装配好的 agent 和历史数据。agent 内部是编译好的 eino 图，
// 可并发处理多个请求，所以这里一个实例全局共享即可。
type server struct {
	agent *react.Agent
	days  []menu.Day
}

func main() {
	// 装配 agent 用一个独立的启动 ctx；服务运行期的取消由下面的信号驱动，两者互不影响。
	agent, days, err := menu.BuildAgent(context.Background(), envOr("HISTORY_PATH", defaultHistoryPath))
	if err != nil {
		log.Fatalf("装配 agent 失败: %v", err)
	}

	srv := &server{agent: agent, days: days}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/api/history", srv.handleHistory)
	mux.HandleFunc("/api/seasonal", srv.handleSeasonal)
	mux.HandleFunc("/api/chat", srv.handleChat)

	httpServer := &http.Server{
		Addr:    ":" + envOr("PORT", "8080"),
		Handler: withCORS(mux),
		// 只限「读完请求头」的时间，挡掉 slowloris（把连接吊着迟迟不发完头）。
		// 故意不设 WriteTimeout——SSE 是长连接，设了会把正常的流式回答拦腰截断。
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 收到 Ctrl-C / SIGTERM 时 ctx.Done() 触发，进入优雅关闭。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ListenAndServe 阻塞，扔进 goroutine；主协程去等信号。
	go func() {
		log.Printf("备餐 agent 后端启动于 %s（已加载历史 %d 天）", httpServer.Addr, len(days))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	<-ctx.Done()
	stop() // 恢复默认信号处理：万一关闭卡住，再按一次 Ctrl-C 能强杀。
	log.Println("收到关闭信号，开始优雅关闭（最多等 10s 让在途的 SSE 请求收尾）…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("优雅关闭超时/出错: %v", err)
	} else {
		log.Println("已优雅关闭。")
	}
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok")
}

// handleHistory 直接把 []Day JSON 返回——前端历史 tab 拿去渲染。
func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(s.days); err != nil {
		log.Printf("/api/history 编码失败: %v", err)
	}
}

// handleSeasonal 返回某个月的时令清单，给前端「时令」tab 用。
// ?month=1..12 指定月份，不传默认当前月。数据和 agent 的 seasonal_produce
// 工具同源（menu.SeasonFor），一张表两个出口，不会出现两边说法不一致。
func (s *server) handleSeasonal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}
	m := int(time.Now().Month())
	if q := r.URL.Query().Get("month"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 12 {
			http.Error(w, "month 必须是 1~12", http.StatusBadRequest)
			return
		}
		m = n
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(menu.SeasonFor(time.Month(m))); err != nil {
		log.Printf("/api/seasonal 编码失败: %v", err)
	}
}

// chatRequest 是前端发来的对话体：一串多轮消息（system 由 agent 自己注入，前端不传）。
type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"` // "user" / "assistant"
	Content string `json:"content"`
	// Context 是这条助手消息那一轮的「工具备忘」（L1 轨迹回灌）：
	// 服务端在流末尾以 context 事件下发，客户端存着、下一轮随历史原样带回，
	// 服务端把最近一条注入上下文——追问时 agent 不必把同样的数据重查一遍。
	// 服务端自己不存任何东西，状态由客户端携带，无状态设计不破。
	Context string `json:"context,omitempty"`
}

// sseEvent 是发给前端的一个增量片段。
//
// 三类：thinking 是「过程」（思考模型的 reasoning + 工具调用轨迹），answer 是「结论」，
// context 是「备忘」（本轮工具轨迹的完整记录，流末尾发一次，供下一轮回灌）。
// 分开发的意义在于：前端可以把过程渲染成灰色可折叠区，答案照常进气泡——
// 用户在 agent 查工具的空窗期也能看到它在干什么，而不是对着「…」干等。
type sseEvent struct {
	Type string `json:"type"` // "thinking" | "answer" | "context"
	Text string `json:"text"`
}

// handleChat 跑 agent.Stream，把思考过程和答案按 SSE 连续吐给前端。
//
// 数据有两个来源，各自一个 goroutine 生产，汇到一个 channel 后由本函数单线程写出
// （http.ResponseWriter 不能并发写）：
//   - MessageFuture：agent 每一步的消息流——中间轮的 reasoning、工具调用参数、
//     工具返回结果，都从这里拿，作为 thinking 事件；
//   - agent.Stream 的主流：只含最终答案轮，作为 answer 事件。
//     中间轮的 Content 故意不转发，避免和主流重复。
//
// SSE 协议约定（和 iOS 端 APIClient 对齐）：
//   - 每个片段一行：data: {"type":"thinking|answer|context","text":"..."}\n\n
//     （context 只在流末尾发一次，是本轮工具轨迹备忘，客户端下轮带回）
//   - 结束：data: [DONE]\n\n
//   - 出错：data: [ERROR]<说明>\n\n  然后照常补一个 [DONE]
func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}

	// 给请求体设 1MB 上限：对话历史再长也到不了这个量级，挡掉异常/恶意的超大 body。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON（或超过 1MB）: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		http.Error(w, "messages 不能为空", http.StatusBadRequest)
		return
	}
	log.Printf("/api/chat：收到 %d 条消息，开始跑 agent", len(req.Messages))

	// SSE 需要能逐块 flush；拿不到 Flusher 说明这个 ResponseWriter 不支持流式。
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "服务器不支持流式响应", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// 用请求的 context：前端断开连接时，agent 这边能一起取消，不空转。
	ctx := r.Context()

	// WithMessageFuture 让 agent 把每一步产生的消息（含中间轮）异步交给 future，
	// 主流照旧只吐最终答案。两者共用同一次运行，不会让模型跑两遍。
	opt, future := react.WithMessageFuture()
	stream, err := s.agent.Stream(ctx, toEinoMessages(req.Messages), opt)
	if err != nil {
		writeSSEError(w, flusher, "启动失败: "+err.Error())
		writeSSEDone(w, flusher)
		return
	}
	defer stream.Close() // StreamReader 必须 Close，否则可能泄漏底层连接/goroutine

	// 事件汇聚 channel。带缓冲：生产侧（模型/工具）和消费侧（网络写出）速度不一致，
	// 缓冲让两边不必锁步——和撮合系统里下游写盘慢不该拖住行情接收是一个道理。
	events := make(chan sseEvent, 64)
	var wg sync.WaitGroup

	// L1 轨迹回灌：备忘收集器。thinking 侧生产者边转发边把工具轨迹存进来。
	digest := &turnDigest{}

	// 生产者 1：思考侧。中间轮的 reasoning / 工具轨迹 → thinking 事件。
	wg.Add(1)
	go func() {
		defer wg.Done()
		forwardThinking(future, events, digest)
	}()

	// 生产者 2：答案侧。主流的 Content → answer 事件。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				events <- sseEvent{Type: "error", Text: err.Error()}
				return
			}
			if chunk.Content != "" {
				events <- sseEvent{Type: "answer", Text: chunk.Content}
			}
		}
	}()

	// 两个生产者都收工后关 channel，让下面的写出循环自然退出。
	go func() {
		wg.Wait()
		close(events)
	}()

	for ev := range events {
		if ev.Type == "error" {
			writeSSEError(w, flusher, ev.Text)
			continue
		}
		writeSSEEvent(w, flusher, ev)
	}

	// 备忘在 channel 关闭（两个生产者都收工）之后才读，天然无竞态。
	// 有工具轨迹才发 context 事件——没调工具的轮次没有备忘可回灌。
	if d := digest.String(); d != "" {
		writeSSEEvent(w, flusher, sseEvent{Type: "context", Text: d})
	}
	writeSSEDone(w, flusher)
}

// forwardThinking 把 agent 中间步骤转成 thinking 事件。
//
// future 交出来的是「每一步一个消息流」：模型轮（可能带 reasoning 和工具调用）
// 或工具结果。reasoning 逐 chunk 直转（这是「连续返回」的关键——思考模型的
// 推理文本边生成边下发）；工具调用参数是分片流式的，拼完整再发一行摘要。
func forwardThinking(future react.MessageFuture, events chan<- sseEvent, digest *turnDigest) {
	iter := future.GetMessageStreams()
	for {
		ms, ok, err := iter.Next()
		if err != nil {
			return // 运行错误由答案侧上报，这里安静退出即可
		}
		if !ok {
			return // 所有步骤都交付完了
		}
		forwardStep(ms, events, digest)
	}
}

// forwardStep 消费一步的消息流。
func forwardStep(ms *schema.StreamReader[*schema.Message], events chan<- sseEvent, digest *turnDigest) {
	defer ms.Close()

	var chunks []*schema.Message
	for {
		m, err := ms.Recv()
		if err != nil {
			break // io.EOF 或错误都停止收集；错误由答案侧上报
		}
		// 思考模型的推理内容：边收边转发，保持打字机的连续感。
		if m.ReasoningContent != "" {
			events <- sseEvent{Type: "thinking", Text: m.ReasoningContent}
		}
		chunks = append(chunks, m)
	}

	// 拼回完整消息，提取需要「攒齐了才有意义」的部分（工具名 + 完整参数）。
	full, err := schema.ConcatMessages(chunks)
	if err != nil || full == nil {
		return
	}
	switch full.Role {
	case schema.Tool:
		// 工具结果原文可能很长（一堆 JSON），前端只需要知道「查到了」，给个摘要；
		// 完整结果进备忘——那才是下一轮回灌时真正值钱的部分。
		events <- sseEvent{
			Type: "thinking",
			Text: fmt.Sprintf("✅ %s 返回了 %d 字\n", full.ToolName, len([]rune(full.Content))),
		}
		digest.addResult(full.ToolName, full.Content)
	case schema.Assistant:
		for _, tc := range full.ToolCalls {
			events <- sseEvent{
				Type: "thinking",
				Text: fmt.Sprintf("🔧 调用 %s %s\n", tc.Function.Name, tc.Function.Arguments),
			}
			digest.addCall(tc.Function.Name, tc.Function.Arguments)
		}
	}
}

// ---- L1 轨迹回灌：工具备忘 ----

// maxDigestResultRunes 限制每条工具结果进备忘的长度，防备忘无限膨胀。
// 现有工具单条结果几百字，1000 字符的帽子正常打不到，只防极端情况。
const maxDigestResultRunes = 1000

// turnDigest 累积一轮 agent 的完整工具轨迹：调了什么（名字+参数）、查到什么（结果原文）。
//
// 它是「对话连续性」的最轻实现：服务端不存任何会话状态，把本轮查到的数据打包
// 交给客户端保管，下一轮随历史带回来再注入上下文——像把清算现场的关键单据
// 塞给客户带走，下次来直接出示，柜台不用重新调档。
//
// 只被 thinking 侧单个 goroutine 写入；读取发生在 events channel 关闭之后
// （close 是同步点），所以不需要锁。
type turnDigest struct {
	b strings.Builder
}

func (d *turnDigest) addCall(name, args string) {
	fmt.Fprintf(&d.b, "🔧 %s %s\n", name, args)
}

func (d *turnDigest) addResult(name, content string) {
	fmt.Fprintf(&d.b, "[%s 结果]\n%s\n", name, truncateRunes(content, maxDigestResultRunes))
}

func (d *turnDigest) String() string { return d.b.String() }

// truncateRunes 按字符数截断——中文场景按字节截会把一个汉字剁成半个。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…（已截断）"
}

// toEinoMessages 把前端消息转成 eino 的 []*schema.Message。
// 角色是 assistant 的当历史助手回复，其余一律当用户输入。
//
// L1 轨迹回灌：只注入「最近一条」带备忘的助手消息的工具轨迹——工具数据有时效性，
// 且相邻轮次查的内容高度重叠（recent_meals 每轮都差不多），全量回灌只会白白撑大上下文。
// 注入形式是紧跟在那条助手回复后面的 system 消息，明确告诉模型「这是你自己查到过的数据」。
func toEinoMessages(in []chatMessage) []*schema.Message {
	lastCtx := -1
	for i, m := range in {
		if schema.RoleType(m.Role) == schema.Assistant && m.Context != "" {
			lastCtx = i
		}
	}

	out := make([]*schema.Message, 0, len(in)+1)
	for i, m := range in {
		if schema.RoleType(m.Role) == schema.Assistant {
			out = append(out, schema.AssistantMessage(m.Content, nil))
			if i == lastCtx {
				out = append(out, schema.SystemMessage(
					"【工具备忘】以下是你上一轮回答时亲自调用工具查到的真实数据（不是编造的），"+
						"本轮可以直接引用它作答；只有当备忘不够回答本轮问题、或数据可能已过期时，才需要再次调用工具：\n"+
						m.Context))
			}
		} else {
			out = append(out, schema.UserMessage(m.Content))
		}
	}
	return out
}

func writeSSEEvent(w io.Writer, f http.Flusher, ev sseEvent) {
	b, _ := json.Marshal(ev) // 编成 JSON，text 含换行/引号也安全
	_, _ = io.WriteString(w, "data: "+string(b)+"\n\n")
	f.Flush()
}

func writeSSEError(w io.Writer, f http.Flusher, msg string) {
	_, _ = io.WriteString(w, "data: [ERROR]"+msg+"\n\n")
	f.Flush()
}

func writeSSEDone(w io.Writer, f http.Flusher) {
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	f.Flush()
}

// withCORS 给所有响应加跨域头，并直接放行预检 OPTIONS——
// 方便模拟器/浏览器/调试工具直连本机后端。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
