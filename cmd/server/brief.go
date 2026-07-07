// brief.go —— L4「主动 agent」的前半：定时产出每日备餐简报。
//
// 前三级（L1/L2/L3）都是「人问一句、agent 动一下」；从这里开始反过来——
// agent 在没人找它的时候自己干活：每天定点跑一次「生成今日简报」，
// 结果存起来等前端来取。像清算系统的日终批处理：不等客户查询才算，
// 到点自己跑批，报表备好等人来拿。
//
//   - 调度：DAILY_BRIEF_AT 环境变量（默认 07:00，设 off 关闭），
//     一个常驻 goroutine 睡到点、生成、再睡到明天。
//   - 产出：GET /api/brief 返回最近一份简报；?refresh=1 强制现做一份
//     （也是不用等到点就能验证整条链路的测试入口）。
//   - 推送（后半）：这里只负责「产出」，把简报推到手机（APNs/WebSocket）
//     是下一步，先把内容生产线跑通。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// briefPrompt 是定时任务喂给 agent 的固定指令。
//
// 最后一句是硬约束：定时任务跑的时候没有人在线，agent 要是调了 ask_user，
// 问题会原样变成「简报」存下来——所以明令禁止追问，信息不足就按已有数据尽力给。
const briefPrompt = `请主动为今天生成一份「今日备餐简报」，家长早上会直接查看：
1. 先查最近几天吃了什么（避免重样），再查当月时令；
2. 给出今天的 午餐、水果、晚餐 建议，每餐 1~2 道，附关键做法/分量要点；
3. 结尾用一句话点出今天的搭配思路。
注意：这是定时任务，没有人在线回答问题——绝对不要调用 ask_user，直接给出完整简报。`

// briefGenTimeout 单次生成的超时。agent 要跑好几轮工具+模型，给足余量。
const briefGenTimeout = 3 * time.Minute

// dailyBrief 是一份生成好的简报，/api/brief 原样吐给前端。
type dailyBrief struct {
	Date        string    `json:"date"`    // 简报对应的日期，如 2026-07-06
	Content     string    `json:"content"` // agent 生成的 Markdown 文本
	GeneratedAt time.Time `json:"generatedAt"`
}

// briefStore 只存「最近一份」。简报是易腐品——昨天的没有存档价值，
// 想要历史趋势应该去看 history.json，不是翻旧简报。
type briefStore struct {
	mu    sync.Mutex
	brief *dailyBrief
}

func (b *briefStore) get() *dailyBrief {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.brief
}

func (b *briefStore) set(d *dailyBrief) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.brief = d
}

// generateBrief 跑一次 agent 生成简报并落库。定时器和 ?refresh=1 共用这一条路。
// 用 Generate（非流式）：没有客户端在等打字机，一次拿全量最省事。
func (s *server) generateBrief(ctx context.Context) (*dailyBrief, error) {
	log.Printf("⏰ 开始生成今日简报…")
	msg, err := s.agent.Generate(ctx, []*schema.Message{schema.UserMessage(briefPrompt)})
	if err != nil {
		return nil, fmt.Errorf("生成简报失败: %w", err)
	}
	d := &dailyBrief{
		Date:        time.Now().Format("2006-01-02"),
		Content:     msg.Content,
		GeneratedAt: time.Now(),
	}
	s.briefs.set(d)
	log.Printf("⏰ 今日简报已生成（%d 字）", len([]rune(d.Content)))
	return d, nil
}

// handleBrief 返回最近一份简报；?refresh=1 强制现做。
// 没有简报又不要求现做时给 404 + 提示，让前端知道该怎么触发。
func (s *server) handleBrief(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Query().Get("refresh") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), briefGenTimeout)
		defer cancel()
		d, err := s.generateBrief(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeBriefJSON(w, d)
		return
	}

	if d := s.briefs.get(); d != nil {
		writeBriefJSON(w, d) // 可能是昨天的——Date 字段带着，新鲜度让前端自己判断
		return
	}
	http.Error(w, "还没有简报。等定时任务生成，或用 /api/brief?refresh=1 立即生成。", http.StatusNotFound)
}

func writeBriefJSON(w http.ResponseWriter, d *dailyBrief) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(d); err != nil {
		log.Printf("/api/brief 编码失败: %v", err)
	}
}

// runBriefScheduler 是常驻调度循环：睡到下一个触发点 → 生成 → 再睡。
// 放 goroutine 里随进程生死，不用专门收尾。
func (s *server) runBriefScheduler(at string) {
	hh, mm, ok := parseClock(at)
	if !ok {
		log.Printf("⏰ 每日简报定时已关闭（DAILY_BRIEF_AT=%q）", at)
		return
	}
	for {
		next := nextRunAt(time.Now(), hh, mm)
		log.Printf("⏰ 下次每日简报生成时间：%s", next.Format("2006-01-02 15:04"))
		time.Sleep(time.Until(next))

		ctx, cancel := context.WithTimeout(context.Background(), briefGenTimeout)
		if _, err := s.generateBrief(ctx); err != nil {
			log.Printf("⏰ 定时生成简报失败: %v", err) // 失败不中断调度，明天再试
		}
		cancel()
	}
}

// parseClock 解析 "HH:MM"。空串/"off"/格式不对都视为关闭——
// 配置错了宁可不跑，也不要在莫名其妙的时间跑。
func parseClock(s string) (hh, mm int, ok bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "off" {
		return 0, 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// nextRunAt 算下一个触发时刻：今天的 HH:MM 还没到就是今天，过了就是明天。
// 纯函数 + 传入 now，离线可测（不用等真实时钟走到七点）。
func nextRunAt(now time.Time, hh, mm int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
