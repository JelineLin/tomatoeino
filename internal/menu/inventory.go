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
	"strconv"
	"strings"
	"sync"
)

// InventoryItem 是账本里的一条：某样食材现有多少份。
type InventoryItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"` // 计量单位，默认「份」
}

// InventoryStore 是带锁、带落盘的库存账本。
// 用切片而不是 map：保持入库顺序，展示稳定（map 遍历顺序会抖）。
type InventoryStore struct {
	mu    sync.Mutex
	path  string
	items []InventoryItem
}

// NewInventoryStore 打开（或新建）账本。文件不存在不算错——空账本，第一次入库时落盘。
func NewInventoryStore(path string) (*InventoryStore, error) {
	s := &InventoryStore{path: path}
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
			return s.items[i], s.save()
		}
	}
	if unit == "" {
		unit = "份"
	}
	it := InventoryItem{Name: name, Quantity: qty, Unit: unit}
	s.items = append(s.items, it)
	return it, s.save()
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
			return s.items[i], s.save()
		}
	}
	if unit == "" {
		unit = "份"
	}
	it := InventoryItem{Name: name, Quantity: qty, Unit: unit}
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
