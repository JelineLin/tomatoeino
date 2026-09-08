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

	"tomato-platform/internal/menu"
)

// briefPrompt 是定时任务喂给 agent 的固定指令。
//
// 最后一句是硬约束：定时任务跑的时候没有人在线，agent 要是调了 ask_user，
// 问题会原样变成「简报」存下来——所以明令禁止追问，信息不足就按已有数据尽力给。
const briefPrompt = `请主动为今天生成一份「今日备餐简报」，家长早上会直接查看：
1. 先查最近几天吃了什么（避免重样），再查当月时令，再查一次家庭库存；
2. 给出今天的 午餐、水果、晚餐 建议，每餐 1~2 道，附关键做法/分量要点。
   优先把库存里【已经有的】食材用掉，别让家长为了做你推荐的菜专门再跑一趟；
   库存清单里标了「该吃了」的要优先安排进今天——那是再不吃就要浪费的东西；
   标了「可能已经吃完或坏了」的别当作确定有，真要用就在正文里提醒家长先确认；
3. 在写文字之前，先调用一次 propose_menu 把这三餐登记成结构化菜单（家长端要据此
   显示可编辑、可一键采纳的卡片）——这一步是必须的。登记时两件事不能省：
   · 每餐给 2 道 alternatives 备选菜，家长换菜时直接顶替主推，所以每道备选都要
     同样满足时令/库存/不重样，不要写成主推的变体；
   · 每道菜（含备选）凡是会吃掉库存里已有的食材，都在 uses 里列出食材名和份数——
     家长采纳这一餐时会照它自动出库；
4. 然后照常写文字版简报，正文只写主推的菜（备选留给卡片，不用写进正文），
   结尾用一句话点出今天的搭配思路。
注意：这是定时任务，没有人在线回答问题——绝对不要调用 ask_user，直接给出完整简报。`

// briefGenTimeout 单次生成的超时。agent 要跑好几轮工具+模型，给足余量。
const briefGenTimeout = 3 * time.Minute

// dailyBrief 是一份生成好的简报，/api/brief 原样吐给前端。
type dailyBrief struct {
	Date    string `json:"date"`    // 简报对应的日期，如 2026-07-06
	Content string `json:"content"` // agent 生成的 Markdown 文本
	// Menu 是 agent 经 propose_menu 登记的结构化推荐菜单——前端据此显示可编辑、可一键
	// 采纳入库的卡片。agent 没调 propose_menu（如降级）时为 nil，前端只显示 Content 文本。
	Menu        *menu.RecommendedMenu `json:"menu,omitempty"`
	GeneratedAt time.Time             `json:"generatedAt"`
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

// markApplied 把「家长（可能编辑后）采纳了某餐」回写进简报缓存：换上采纳时真正入库的
// 时间/菜品，并标 Applied——重新拉简报时卡片显示的就是实际采纳的版本，而不是原推荐
//（否则家长编辑完看到卡片没变，以为「编辑没保存」；重进 App 连已采纳状态都丢）。
//
// 克隆换指针，不原地改：get() 交出去的指针可能正被 handleBrief 在锁外序列化，
// 原地改会数据竞争（和 HistoryStore 的快照纪律同一口径）。
// 日期对不上（简报是昨天的、采纳的是明天的餐）就不动——简报只描述它自己那天。
func (b *briefStore) markApplied(date, mealField string, stored *menu.Meal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.brief == nil || b.brief.Menu == nil || b.brief.Menu.Date != date {
		return
	}
	old := b.brief.Menu
	nm := &menu.RecommendedMenu{Date: old.Date, Meals: make([]menu.ProposedMeal, len(old.Meals))}
	copy(nm.Meals, old.Meals)
	for i := range nm.Meals {
		if nm.Meals[i].Meal != mealField {
			continue
		}
		nm.Meals[i].Time = stored.Time
		nm.Meals[i].Dishes = stored.Dishes
		nm.Meals[i].Applied = true
	}
	nb := *b.brief
	nb.Menu = nm
	b.brief = &nb
}

// replaceDish 把简报缓存里某餐的第 idx 道菜换成 next，返回换完的整份菜单。
// 找不到（简报换了天、餐别不存在、下标越界）返回 nil，调用方据此报 409/400。
//
// 克隆换指针，不原地改——理由同 markApplied：get() 交出去的指针可能正被序列化。
func (b *briefStore) replaceDish(date, mealField string, idx int, next menu.Dish) *menu.RecommendedMenu {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.brief == nil || b.brief.Menu == nil || b.brief.Menu.Date != date {
		return nil
	}
	old := b.brief.Menu
	nm := &menu.RecommendedMenu{Date: old.Date, Meals: make([]menu.ProposedMeal, len(old.Meals))}
	copy(nm.Meals, old.Meals)
	for i := range nm.Meals {
		if nm.Meals[i].Meal != mealField {
			continue
		}
		if idx < 0 || idx >= len(nm.Meals[i].Dishes) {
			return nil
		}
		// 菜品切片也要克隆：copy 出来的 ProposedMeal 和旧的共用同一个底层数组，
		// 直接改会把旧快照一起改掉（切片克隆坑，反馈那轮踩过一次）。
		dishes := make([]menu.Dish, len(nm.Meals[i].Dishes))
		copy(dishes, nm.Meals[i].Dishes)
		dishes[idx] = next
		nm.Meals[i].Dishes = dishes
		nb := *b.brief
		nb.Menu = nm
		b.brief = &nb
		return nm
	}
	return nil
}

// replaceDishPrompt 拼「只换这一道」的指令。
//
// 刻意把工具面收窄在文字里而不是真去改 agent 的工具集：换一道菜要的上下文
// （库存/时令/最近吃了什么）和整份简报是同一批，重新装配一个精简 agent 不值当；
// 真正要防的是模型顺手把整餐重排了，那靠指令说清楚就够。
func replaceDishPrompt(mealLabel, oldDish string, siblings []string, instruction string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "家长想把今天%s里的「%s」换掉，请另选一道顶替它。\n", mealLabel, oldDish)
	b.WriteString("步骤：\n")
	b.WriteString("1. 先查一次家庭库存、当月时令，再看看最近几天吃了什么；\n")
	b.WriteString("2. 新菜要能顶替原来那道在这一餐里的角色（主食/蛋白/蔬菜别错位）；\n")
	if len(siblings) > 0 {
		fmt.Fprintf(&b, "3. 这一餐里保留不动的还有：%s——新菜不要和它们重复或撞味；\n", strings.Join(siblings, "、"))
	} else {
		b.WriteString("3. 这一餐没有别的菜要考虑；\n")
	}
	b.WriteString("4. 别和最近几天吃过的重样；库存里标了「该吃了」的优先用掉；\n")
	b.WriteString("5. 选定后调用一次 propose_dish 登记这道菜（记得带 uses），然后用一两句话告诉家长换成了什么、为什么。\n")
	if s := strings.TrimSpace(instruction); s != "" {
		fmt.Fprintf(&b, "\n家长对这次替换的额外要求：%s\n", s)
	}
	b.WriteString("\n注意：只换这一道菜，不要重新安排整餐，不要调用 propose_menu 或 record_meal，也绝对不要调用 ask_user。")
	return b.String()
}

// generateBrief 跑一次 agent 生成简报并落库（多用户后按 workspace 生成）。
// 定时器和 ?refresh=1 共用这一条路。用 Generate（非流式）：没有客户端在等打字机。
// adjustBriefPrompt 在标准简报指令基础上，附上「当前简报原文 + 家长的调整要求」。
// 走全量重生成而非局部修补：propose_menu 的结构化登记必须完整重走一遍，
// 前端的可编辑/可采纳卡片才不会缺块。
func adjustBriefPrompt(cur *dailyBrief, instruction string) string {
	var b strings.Builder
	b.WriteString(briefPrompt)
	if cur != nil {
		b.WriteString("\n\n这是你早前生成的今日简报：\n")
		b.WriteString(cur.Content)
	}
	b.WriteString("\n\n家长看过简报后提出了调整要求，请在满足要求的前提下重新生成完整简报，" +
		"没被点名的部分尽量保持原安排：\n")
	b.WriteString(instruction)
	return b.String()
}

func generateBrief(ctx context.Context, uid string, ws *workspace) (*dailyBrief, error) {
	return generateBriefWith(ctx, uid, ws, briefPrompt)
}

// generateBriefWith 按给定 prompt 生成并落存简报——定时任务/refresh 用标准 prompt，
// 「智能调整」用 adjustBriefPrompt 拼出来的带指令版，落存路径完全一致。
func generateBriefWith(ctx context.Context, uid string, ws *workspace, prompt string) (*dailyBrief, error) {
	log.Printf("⏰ [%s] 开始生成今日简报…", uid)
	// 往 ctx 挂一个菜单收集器：agent 若调 propose_menu，结构化菜单会写进 sink，
	// Generate 返回后读走随简报下发（复用 trace.go 的 ctx 贯穿机制）。
	ctx, sink := menu.WithMenuSink(ctx)
	msg, err := ws.agent.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return nil, fmt.Errorf("生成简报失败: %w", err)
	}
	d := &dailyBrief{
		Date:    time.Now().Format("2006-01-02"),
		Content: msg.Content,
		Menu:    sink.Get(), // agent 没调 propose_menu 则 nil，前端退化为只显示文字
		// 截断到秒：Go 默认按 RFC3339Nano 序列化（带纳秒小数），
		// 而 Swift 的 .iso8601 解码策略不认小数秒——去掉小数两边都省事。
		GeneratedAt: time.Now().Truncate(time.Second),
	}
	ws.briefs.set(d)
	log.Printf("⏰ [%s] 今日简报已生成（%d 字，结构化菜单：%v）", uid, len([]rune(d.Content)), d.Menu != nil)
	return d, nil
}

// handleBrief 返回最近一份简报；?refresh=1 强制现做。
// 没有简报又不要求现做时给 404 + 提示，让前端知道该怎么触发。
func (s *server) handleBrief(w http.ResponseWriter, r *http.Request) {
	ws, err := s.ws(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// POST = 智能调整：带家长的自然语言指令，基于当前简报重新生成。
	// （用 POST 而非新路径，和库存的「POST + 语义字段」同一套路，CORS 方法集不用动。）
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var req struct {
			Instruction string `json:"instruction"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体不是合法 JSON："+err.Error(), http.StatusBadRequest)
			return
		}
		ins := strings.TrimSpace(req.Instruction)
		if ins == "" {
			http.Error(w, "instruction 不能为空", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), briefGenTimeout)
		defer cancel()
		d, err := generateBriefWith(ctx, userIDFrom(r), ws, adjustBriefPrompt(ws.briefs.get(), ins))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeBriefJSON(w, d)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET/POST", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Query().Get("refresh") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), briefGenTimeout)
		defer cancel()
		d, err := generateBrief(ctx, userIDFrom(r), ws)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeBriefJSON(w, d)
		return
	}

	if d := ws.briefs.get(); d != nil {
		writeBriefJSON(w, d) // 可能是昨天的——Date 字段带着，新鲜度让前端自己判断
		return
	}
	http.Error(w, "还没有简报。等定时任务生成，或用 /api/brief?refresh=1 立即生成。", http.StatusNotFound)
}

// replaceDishRequest 是「换掉某一道菜」的请求体。
type replaceDishRequest struct {
	Meal        string `json:"meal"`        // lunch/fruit/dinner
	DishIndex   int    `json:"dishIndex"`   // 这一餐里第几道（0 起）
	Instruction string `json:"instruction"` // 可选：家长对这次替换的额外要求
}

// replaceDishResponse 回新菜 + 换完的整份菜单（前端直接替换本地状态）+ agent 的一句说明。
type replaceDishResponse struct {
	Dish menu.Dish             `json:"dish"`
	Menu *menu.RecommendedMenu `json:"menu"`
	Note string                `json:"note"`
}

// replaceDishTimeout 比整份简报短得多：只换一道菜，工具最多跑三四轮。
// 给足 2 分钟是为了容忍慢网关，但正常应该 20~40 秒回来。
const replaceDishTimeout = 2 * time.Minute

// handleReplaceDish 只重新生成【一道菜】，其余原样不动。
//
// 这是「调整菜单」的第二级：备选菜（propose_menu 一并带回的 Alternatives）是第一级，
// 零请求零延迟，吃掉大部分「这道不想吃，换一个」；两道备选都不满意才落到这里。
// 第三级才是 POST /api/brief 的整份重生成（「重摇整天」）。
// 三级从便宜到贵，绝大多数调整应该止步于第一级。
func (s *server) handleReplaceDish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	ws, err := s.ws(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req replaceDishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON："+err.Error(), http.StatusBadRequest)
		return
	}

	cur := ws.briefs.get()
	if cur == nil || cur.Menu == nil {
		http.Error(w, "当前没有结构化菜单可改，先生成一份简报", http.StatusConflict)
		return
	}
	// 先在本地找到那道菜：拿到菜名和同餐的其他菜喂给 prompt，也顺便把越界挡在调模型之前。
	var target *menu.ProposedMeal
	for i := range cur.Menu.Meals {
		if cur.Menu.Meals[i].Meal == req.Meal {
			target = &cur.Menu.Meals[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "简报里没有「"+req.Meal+"」这一餐", http.StatusBadRequest)
		return
	}
	if req.DishIndex < 0 || req.DishIndex >= len(target.Dishes) {
		http.Error(w, "dishIndex 越界", http.StatusBadRequest)
		return
	}
	oldDish := target.Dishes[req.DishIndex]
	siblings := make([]string, 0, len(target.Dishes))
	for i, d := range target.Dishes {
		if i != req.DishIndex {
			siblings = append(siblings, d.Name)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), replaceDishTimeout)
	defer cancel()
	ctx, sink := menu.WithDishSink(ctx)
	prompt := replaceDishPrompt(mealLabel(req.Meal), oldDish.Name, siblings, req.Instruction)
	log.Printf("🔁 [%s] 换菜：%s / %s", userIDFrom(r), req.Meal, oldDish.Name)
	msg, err := ws.agent.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		http.Error(w, "换菜失败："+err.Error(), http.StatusBadGateway)
		return
	}
	next := sink.Get()
	if next == nil {
		// 模型没调 propose_dish（多半是绕开工具直接用文字答了）。不猜、不硬解析它的自由文本——
		// 把原话带回去让家长自己看，总比把一句散文当成菜名塞进卡片强。
		http.Error(w, "agent 没有给出可用的替换菜，请再试一次或换个说法。它说："+truncateRunes(msg.Content, 200), http.StatusBadGateway)
		return
	}

	nm := ws.briefs.replaceDish(cur.Menu.Date, req.Meal, req.DishIndex, *next)
	if nm == nil {
		// 期间简报被重新生成/换了天，位置对不上了。不强行写，让前端重拉。
		http.Error(w, "简报已变化，请刷新后重试", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(replaceDishResponse{Dish: *next, Menu: nm, Note: msg.Content}); err != nil {
		log.Printf("/api/brief/dish 编码失败: %v", err)
	}
}

// mealLabel 把餐别字段渲染成中文，喂给 prompt 用（前端各有各的一份，这里只服务模型）。
func mealLabel(field string) string {
	switch field {
	case "lunch":
		return "午餐"
	case "fruit":
		return "水果"
	case "dinner":
		return "晚餐"
	default:
		return field
	}
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

		// 逐户【串行】生成——1.6GB 小机不能并行跑 N 个推理，API 也有限速；
		// 07:00 没人急着看第 N 家的简报。副作用恰好是全体用户的每日预热：
		// reg.get 会把没建的 workspace 建起来，之后白天首访无冷启动。
		for _, uid := range s.reg.allUIDs() {
			ctx, cancel := context.WithTimeout(context.Background(), briefGenTimeout)
			ws, err := s.reg.get(ctx, uid)
			if err == nil {
				_, err = generateBrief(ctx, uid, ws)
			}
			if err != nil {
				log.Printf("⏰ [%s] 定时生成简报失败: %v", uid, err) // 单户失败不断链
			}
			cancel()
		}
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
