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
	"io"
	"log"
	"net/http"
	"os"

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
	ctx := context.Background()

	historyPath := envOr("HISTORY_PATH", defaultHistoryPath)
	agent, days, err := menu.BuildAgent(ctx, historyPath)
	if err != nil {
		log.Fatalf("装配 agent 失败: %v", err)
	}

	srv := &server{agent: agent, days: days}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/api/history", srv.handleHistory)
	mux.HandleFunc("/api/chat", srv.handleChat)

	addr := ":" + envOr("PORT", "8080")
	log.Printf("备餐 agent 后端启动于 %s（已加载历史 %d 天）", addr, len(days))
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
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

// chatRequest 是前端发来的对话体：一串多轮消息（system 由 agent 自己注入，前端不传）。
type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"` // "user" / "assistant"
	Content string `json:"content"`
}

// handleChat 跑 agent.Stream，把每个 token 按 SSE 吐给前端。
//
// SSE 协议约定（和 iOS 端 APIClient 对齐）：
//   - 每个增量片段一行：data: <JSON 字符串>\n\n   —— token 用 JSON 编码，
//     这样含换行/引号也安全（SSE 的 data 字段本身不能直接带裸换行）。
//   - 结束：data: [DONE]\n\n
//   - 出错：data: [ERROR]<说明>\n\n  然后照常补一个 [DONE]
func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		http.Error(w, "messages 不能为空", http.StatusBadRequest)
		return
	}

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
	stream, err := s.agent.Stream(ctx, toEinoMessages(req.Messages))
	if err != nil {
		writeSSEError(w, flusher, "启动失败: "+err.Error())
		writeSSEDone(w, flusher)
		return
	}
	defer stream.Close() // StreamReader 必须 Close，否则可能泄漏底层连接/goroutine

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break // 流正常结束
		}
		if err != nil {
			writeSSEError(w, flusher, err.Error())
			break
		}
		if chunk.Content == "" {
			continue // 工具调用等中间块可能没有文本内容，跳过
		}
		writeSSEToken(w, flusher, chunk.Content)
	}
	writeSSEDone(w, flusher)
}

// toEinoMessages 把前端消息转成 eino 的 []*schema.Message。
// 角色是 assistant 的当历史助手回复，其余一律当用户输入。
func toEinoMessages(in []chatMessage) []*schema.Message {
	out := make([]*schema.Message, 0, len(in))
	for _, m := range in {
		if schema.RoleType(m.Role) == schema.Assistant {
			out = append(out, schema.AssistantMessage(m.Content, nil))
		} else {
			out = append(out, schema.UserMessage(m.Content))
		}
	}
	return out
}

func writeSSEToken(w io.Writer, f http.Flusher, token string) {
	b, _ := json.Marshal(token) // 编成 JSON 字符串，含换行/引号也安全
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
