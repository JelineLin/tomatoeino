package menu

// trace.go —— 请求级 trace-id 的运输通道：B 方案（ctx 贯穿）那节 eino 课的
// 20 行低风险版本。机制和多租户 ctx 贯穿完全同构：
//
//	HTTP 中间件把 id 塞进请求 ctx
//	  → agent.Stream(ctx, ...) 进入编译期定型的 ReAct 图
//	  → eino 把 ctx 原样传进每个工具闭包（tool_node.go 的 InvokableRun(ctx,...)）
//	  → 工具日志带上 id，一轮对话的所有 🔧 调用在日志里串成一条链
//
// 差别只在承载的东西：这里是「可观测性」（丢了顶多日志缺个 id，零风险），
// 多租户正确性依然由 workspace 结构保证——ctx 是编译图世界里唯一的请求级通道，
// 这一课学到了，正确性不押在「每个入口都记得注入」的纪律上。

import (
	"context"
	"log"
)

type traceIDKey struct{}

// WithTraceID 把请求级 trace-id 放进 ctx，由 HTTP 层在请求入口调用。
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, id)
}

// traceIDFrom 取出 trace-id；没有（如 CLI 直跑、单测）时给 "-" 占位。
func traceIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDKey{}).(string); ok && id != "" {
		return id
	}
	return "-"
}

// toolLog 是所有工具日志的统一出口：🔧 [trace] 工具名(参数)。
func toolLog(ctx context.Context, format string, args ...any) {
	log.Printf("🔧 [%s] "+format, append([]any{traceIDFrom(ctx)}, args...)...)
}
