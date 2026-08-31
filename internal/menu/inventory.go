package menu

// 家庭库存 —— 路线图里「有状态可读写」的数据源，也是这个 agent 第一次拿到
// 「写世界」的能力：前面所有工具都只是读（查历史、查时令），从这里开始，
// 「买了 2 份鳕鱼」「用掉半个西兰花」会真实改变一份落盘的账本。
//
// 记账规则（按份数）：
//   - 每样食材一条：名称 + 份数（float64，0.5 = 半份）+ 单位（份/块/个/袋…）。
//   - 入库累加：同名再买份数直接加上去，像充值。
//   - 出库扣减：够扣就扣；不够扣就清零出清并明说——账本宁可归零，不记负数
//     （和资金账户不许透支是一个道理）。
//   - 扣到 0 的条目直接移除：0 份的东西不占货架。
//
// 持久化：一个 JSON 文件，每次变更全量重写（先写临时文件再 rename，原子替换，
// 断电也不会留半个文件）。家庭规模的数据量，全量重写绰绰有余。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// InventoryItem 是账本里的一条：某样食材现有多少份、什么时候进的账。
type InventoryItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"` // 计量单位，默认「份」
	// UpdatedAt 是最近一次【补货】的时刻（RFC3339）。入库/改数刷新，出库不刷新——
	// 它回答的是「这东西是什么时候买的」，吃掉一半不会让剩下那半变新鲜。
	//
	// 这个字段是库存真正的价值所在：份数永远不准（谁也不会记半根黄瓜），
	// 但「几天前买的」是系统自己就能记准、家长完全不用维护的事实。
	// 有了它，库存从「账本」变成「线索」——见 freshness.go。
	// omitempty + 空值容忍：旧账本没有这个字段，读出来是 unknown 档，不猜。
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// InventoryStore 是带锁、带落盘的库存账本。
// 用切片而不是 map：保持入库顺序，展示稳定（map 遍历顺序会抖）。
type InventoryStore struct {
	mu    sync.Mutex
	path  string
	items []InventoryItem
	// now 是可注入的时钟，只为让新鲜度相关的测试能造出「三天前买的」这种状态。
	// 生产路径永远是 time.Now（NewInventoryStore 里装配）。
	now func() time.Time
}

// NewInventoryStore 打开（或新建）账本。文件不存在不算错——空账本，第一次入库时落盘。
func NewInventoryStore(path string) (*InventoryStore, error) {
	s := &InventoryStore{path: path, now: time.Now}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取库存文件 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(raw, &s.items); err != nil {
		return nil, fmt.Errorf("解析库存文件 %s 失败: %w", path, err)
	}
	return s, nil
}

// List 返回账本快照（副本，调用方随便改不脏账）。keyword 非空时按名称子串过滤。
func (s *InventoryStore) List(keyword string) []InventoryItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InventoryItem, 0, len(s.items))
	for _, it := range s.items {
		if keyword == "" || strings.Contains(it.Name, keyword) {
			out = append(out, it)
		}
	}
	return out
}

// FreshInventoryItem 是一条库存 + 它的时间维度。派生数据，不落盘——
// 保鲜期表随时可能调整，存下来就成了要迁移的历史包袱；现算一遍成本约等于零。
type FreshInventoryItem struct {
	InventoryItem
	Freshness Freshness `json:"freshness"` // fresh / use_soon / stale / unknown
	Days      int       `json:"days"`      // 放了几天；-1 = 不详
	ShelfLife int       `json:"shelfLife"` // 这个品类的保鲜期（天），给界面显示进度用
	Category  string    `json:"category"`  // 判定用的品类，空串 = 没匹配到、按默认值算
}

// ListFresh 列出库存并带上新鲜度。排序刻意是【越该吃的越靠前】：
// stale → use_soon → fresh → unknown，同档内放得久的在前。
// 界面和模型拿到的第一眼就是「最该处理的东西」，而不是入库顺序这种和决策无关的次序。
func (s *InventoryStore) ListFresh(keyword string) []FreshInventoryItem {
	items := s.List(keyword) // 复用已有的锁和过滤，不重复实现
	now := s.now()
	out := make([]FreshInventoryItem, 0, len(items))
	for _, it := range items {
		f, days := FreshnessOf(it, now)
		life, cat := classify(it.Name)
		out = append(out, FreshInventoryItem{
			InventoryItem: it, Freshness: f, Days: days, ShelfLife: life, Category: cat,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := freshnessRank(out[i].Freshness), freshnessRank(out[j].Freshness)
		if pi != pj {
			return pi < pj
		}
		return out[i].Days > out[j].Days
	})
	return out
}

// freshnessRank 决定排序优先级，数字越小越靠前。
func freshnessRank(f Freshness) int {
	switch f {
	case FreshnessStale:
		return 0
	case FreshnessUseSoon:
		return 1
	case FreshnessFresh:
		return 2
	default: // unknown 垫底：没有时间信息的东西不该抢占注意力
		return 3
	}
}

// Add 入库：同名累加份数（充值语义），新名字追加一条。unit 传空则沿用已有/默认「份」。
// 返回入库后的最新条目。
func (s *InventoryStore) Add(name string, qty float64, unit string) (InventoryItem, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return InventoryItem{}, fmt.Errorf("食材名不能为空")
	}
	if qty <= 0 {
		return InventoryItem{}, fmt.Errorf("入库份数必须大于 0，收到 %s", fmtQty(qty))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].Name == name {
			s.items[i].Quantity += qty
			if unit != "" {
				s.items[i].Unit = unit
			}
			// 补货刷新时间戳：又买了一份，这样东西就该按「今天买的」算。
			s.items[i].UpdatedAt = s.stamp()
			return s.items[i], s.save()
		}
	}
	if unit == "" {
		unit = "份"
	}
	it := InventoryItem{Name: name, Quantity: qty, Unit: unit, UpdatedAt: s.stamp()}
	s.items = append(s.items, it)
	return it, s.save()
}

// stamp 取当前时刻的 RFC3339 串。截断到秒——库存的时间精度到天就够了，
// 纳秒只会让 JSON 更难读。调用方必须已持有锁（now 本身无状态，但用法上跟着写路径走）。
func (s *InventoryStore) stamp() string {
	return s.now().Truncate(time.Second).Format(time.RFC3339)
}

// Consume 出库：按名称找到条目扣减份数。
//
// 名称匹配从严到宽：先精确，再子串（库存里「西兰花」能被「半个西兰花的西兰花」
// 这类说法命中）。子串命中多条时报歧义，把候选摆出来让模型说清楚——
// 宁可多一轮，也不能扣错账。
// 返回：扣减后的条目快照（出清时 Quantity=0）、是否出清、错误。
func (s *InventoryStore) Consume(name string, qty float64) (InventoryItem, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return InventoryItem{}, false, fmt.Errorf("食材名不能为空")
	}
	if qty <= 0 {
		return InventoryItem{}, false, fmt.Errorf("出库份数必须大于 0，收到 %s", fmtQty(qty))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.items {
		if s.items[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 { // 精确没中，退一步子串匹配
		var hits []int
		for i := range s.items {
			if strings.Contains(s.items[i].Name, name) || strings.Contains(name, s.items[i].Name) {
				hits = append(hits, i)
			}
		}
		switch len(hits) {
		case 1:
			idx = hits[0]
		case 0:
			return InventoryItem{}, false, fmt.Errorf("库存里没有「%s」", name)
		default:
			names := make([]string, 0, len(hits))
			for _, i := range hits {
				names = append(names, s.items[i].Name)
			}
			return InventoryItem{}, false, fmt.Errorf("「%s」匹配到多条库存：%s，请说明具体是哪一个", name, strings.Join(names, "、"))
		}
	}

	it := &s.items[idx]
	if qty >= it.Quantity {
		// 不够扣：清零出清，不记负数。
		out := InventoryItem{Name: it.Name, Quantity: 0, Unit: it.Unit}
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		return out, true, s.save()
	}
	it.Quantity -= qty
	return *it, false, s.save()
}

// ConsumedItem 是一条出库结果，回给前端做提示（「西兰花用掉 1 份，还剩 0.5 份」）。
type ConsumedItem struct {
	Name      string  `json:"name"`      // 账本里的真实名字（可能和请求的略有出入——Consume 允许子串匹配）
	Qty       float64 `json:"qty"`       // 这次扣了多少份
	Unit      string  `json:"unit"`      // 账本单位
	Remaining float64 `json:"remaining"` // 扣完还剩多少
	Depleted  bool    `json:"depleted"`  // 扣完出清（不够扣也算——账上归零，不记负数）
}

// ConsumeAll 按一张出库清单批量扣减，返回扣成的条目和没扣成的名字。
//
// 单条扣不动【不算错】，只记进 missed 继续往下走。理由：库存在这个系统里是「线索」
// 而不是「账本」——家长采纳一餐是主行为，扣库存是副作用，绝不能因为账上没那样东西
// （模型写错名字、家长手动删过、上次早就扣光）反过来让采纳失败。
//
// 逐条走 Consume 而不是自己遍历：宽松匹配（精确→子串→歧义报错）和「不够扣就出清、
// 不记负数」的语义只在 Consume 里写一遍，这里不复制。
func (s *InventoryStore) ConsumeAll(uses []IngredientUse) (consumed []ConsumedItem, missed []string) {
	for _, u := range uses {
		name := strings.TrimSpace(u.Name)
		if name == "" || u.Qty <= 0 {
			continue
		}
		it, depleted, err := s.Consume(name, u.Qty)
		if err != nil {
			missed = append(missed, name)
			continue
		}
		consumed = append(consumed, ConsumedItem{
			Name:      it.Name,
			Qty:       u.Qty,
			Unit:      it.Unit,
			Remaining: it.Quantity,
			Depleted:  depleted,
		})
	}
	return consumed, missed
}

// Set 把某样食材设为【精确份数】（界面编辑用，区别于 Add 的累加充值）。
// name 不存在则新增一条。qty 必须 > 0——要删除整条走 Remove，别用 Set 0。
// unit 传空则沿用已有/默认「份」。返回设置后的条目。
func (s *InventoryStore) Set(name string, qty float64, unit string) (InventoryItem, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return InventoryItem{}, fmt.Errorf("食材名不能为空")
	}
	if qty <= 0 {
		return InventoryItem{}, fmt.Errorf("份数必须大于 0（删除整条请用删除），收到 %s", fmtQty(qty))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].Name == name {
			s.items[i].Quantity = qty
			if unit != "" {
				s.items[i].Unit = unit
			}
			// 界面上手改份数也刷新时间戳：家长愿意去数一遍，说明他刚看过冰箱，
			// 这个数字此刻是可信的——比几天前那次入库更值得当作「最新观测」。
			s.items[i].UpdatedAt = s.stamp()
			return s.items[i], s.save()
		}
	}
	if unit == "" {
		unit = "份"
	}
	it := InventoryItem{Name: name, Quantity: qty, Unit: unit, UpdatedAt: s.stamp()}
	s.items = append(s.items, it)
	return it, s.save()
}

// Remove 按精确名称删除整条库存（界面划删用）。不存在则报错，让调用方知道没删到。
func (s *InventoryStore) Remove(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("食材名不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].Name == name {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return s.save()
		}
	}
	return fmt.Errorf("库存里没有「%s」", name)
}

// save 全量落盘：临时文件 + rename 原子替换。调用方必须已持有锁。
func (s *InventoryStore) save() error {
	raw, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化库存失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建库存目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".inventory-*.json")
	if err != nil {
		return fmt.Errorf("创建临时库存文件失败: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("写临时库存文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时库存文件失败: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("落盘库存失败: %w", err)
	}
	return nil
}

// fmtQty 把份数渲染成人话：整数不带小数点（2份），小数保留原样（0.5份）。
func fmtQty(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}

// renderInventoryItem 渲染一条库存，例：「鳕鱼：2块」「西兰花：0.5份」。
func renderInventoryItem(it InventoryItem) string {
	return fmt.Sprintf("%s：%s%s", it.Name, fmtQty(it.Quantity), it.Unit)
}
