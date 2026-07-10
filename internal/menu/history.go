package menu

// HistoryStore —— 把裸 []Day 升级成带锁、可写、可落盘的历史账本。
//
// 为什么需要它：record_meal（采纳入库）让历史第一次有了「运行时写」。
// 原来 LoadHistory 读出的切片被工具闭包按值捕获、被 /api/history 直接引用，
// 是一张启动时拍下的照片——没有任何人能一致地改它。HistoryStore 让内存里
// 只有一份权威数据，所有读者每次现取快照，不留第二份陈旧拷贝。
//
// 写入语义（和设计评审定稿一致）：
//   - SetMeal 整餐覆盖：同天同餐再次写入 = 完全替换（家长「变卦可改」）；
//     天不存在则按日期升序插入新的一天（recent_meals 取尾部依赖升序）。
//   - 落盘照抄 InventoryStore 范本：临时文件 + rename 原子替换；
//     额外多一道 .bak——写之前把旧文件复制一份，模型传错日期覆盖了
//     正确记录时有最近一版可回退（历史脱离 git 管辖后的低成本兜底）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// HistoryStore 是带锁、带落盘的吃饭历史账本。
type HistoryStore struct {
	mu   sync.Mutex
	path string
	days []Day // 始终保持按 Date 升序
}

// NewHistoryStore 打开（或新建）历史账本。文件不存在不算错——空历史，
// 新用户从零起步，record_meal 会一餐一餐把它自举出来。
func NewHistoryStore(path string) (*HistoryStore, error) {
	s := &HistoryStore{path: path}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取历史文件 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(raw, &s.days); err != nil {
		return nil, fmt.Errorf("解析历史文件 %s 失败: %w", path, err)
	}
	// 防御：来源文件可能没排好序，进门先排一次，之后的插入维持有序。
	sort.Slice(s.days, func(i, j int) bool { return s.days[i].Date < s.days[j].Date })
	return s, nil
}

// Snapshot 返回全量历史的副本（按日期升序）。
//
// 副本是浅拷贝：Day 里的 *Meal 指针是共享的——安全的前提是「写入只换指针、
// 绝不原地改 Meal 内容」（SetMeal 正是这么做的），所以旧快照永远内部一致。
// 调用方只读渲染即可，不要往快照里的 Meal 写东西。
func (s *HistoryStore) Snapshot() []Day {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Day, len(s.days))
	copy(out, s.days)
	return out
}

// SetMeal 整餐覆盖式写入 date 那天的 mealField（lunch/fruit/dinner）。
// 返回：
//   - stored：真正落库的那一餐（含从旧餐结转来的反馈）。调用方必须用它、而不是自己
//     传进来的 m，去重建向量 doc（BuildMealDocument）——否则覆盖已有反馈的餐时，向量
//     索引会用不带反馈的 m 渲染，造成「JSON 有反馈、语义检索无反馈」的两视图错位。
//   - replaced：那天那餐原本是否已有记录（true=这次是覆盖修正），供工具措辞用。
func (s *HistoryStore) SetMeal(date, mealField string, m Meal) (stored *Meal, replaced bool, err error) {
	if !validMealField(mealField) {
		return nil, false, fmt.Errorf("餐别必须是 lunch/fruit/dinner 之一，收到 %q", mealField)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stored, replaced = s.setMealLocked(date, mealField, m)
	if err := s.save(); err != nil {
		return stored, replaced, err
	}
	return stored, replaced, nil
}

// setMealLocked 是 SetMeal / ImportDays 共用的写核心：找到或新建那天、整餐覆盖（结转旧反馈）、
// 维持升序。调用方【必须已持锁】且已保证 mealField 合法；本函数【不落盘】——批量导入好一次落盘。
// 返回真正落库的那一餐（含结转反馈）供上层 Upsert。
func (s *HistoryStore) setMealLocked(date, mealField string, m Meal) (stored *Meal, replaced bool) {
	idx := -1
	for i := range s.days {
		if s.days[i].Date == date {
			idx = i
			break
		}
	}
	if idx >= 0 {
		old := s.days[idx].mealOf(mealField)
		replaced = old != nil
		// 结转反馈：整餐覆盖时不带 feedback，别把家长已录的反馈抹掉。
		if old != nil && old.Feedback != nil && m.Feedback == nil {
			m.Feedback = old.Feedback
		}
		s.days[idx].setMeal(mealField, &m) // 换指针，不原地改旧 Meal——旧快照仍一致
	} else {
		var d Day
		d.Date = date
		d.setMeal(mealField, &m)
		s.days = append(s.days, d)
		// ISO 日期（YYYY-MM-DD）的字符串序就是时间序，直接按字符串排。
		sort.Slice(s.days, func(i, j int) bool { return s.days[i].Date < s.days[j].Date })
	}
	return &m, replaced
}

// WrittenMeal 记录 ImportDays 真正写入的一餐，供上层重建向量 doc（Upsert）。
type WrittenMeal struct {
	Date  string
	Field string
	Meal  *Meal
}

// ImportDays 批量导入多天多餐（F1 导入历史用）：一把锁内合并、【一次落盘】
//（比逐餐 SetMeal 少 N 次文件写）。同天同餐覆盖并结转旧反馈；非法日期、无菜的餐自动跳过，
// 不污染历史的字符串升序。返回真正写入的餐（供 Upsert）+ 新增/覆盖计数。
func (s *HistoryStore) ImportDays(days []Day) (written []WrittenMeal, added, replaced int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, d := range days {
		date := strings.TrimSpace(d.Date)
		if _, e := time.Parse("2006-01-02", date); e != nil {
			continue // 非法日期跳过
		}
		for _, mk := range mealOrder {
			m := d.mealOf(mk.field)
			if m == nil || len(m.Dishes) == 0 {
				continue
			}
			stored, wasReplaced := s.setMealLocked(date, mk.field, *m)
			if wasReplaced {
				replaced++
			} else {
				added++
			}
			written = append(written, WrittenMeal{Date: date, Field: mk.field, Meal: stored})
		}
	}
	if len(written) == 0 {
		return nil, 0, 0, nil // 没有可导入的有效餐——不落盘
	}
	if err := s.save(); err != nil {
		return written, added, replaced, err
	}
	return written, added, replaced, nil
}

// SetFeedback 给 date 那天的 mealField 记一条反馈（fb 传 nil 表示清除反馈）。
// 返回更新后的那一餐，供上层重建向量 doc（Upsert）。
//
// 保持 Snapshot 浅拷贝不变量：克隆整餐、在副本上写反馈、再换指针，绝不原地改现有 *Meal
// ——否则此前拿到快照的读者会被写脏。那一餐不存在时报错（不能给没记录的餐加反馈）。
func (s *HistoryStore) SetFeedback(date, mealField string, fb *Feedback) (*Meal, error) {
	if !validMealField(mealField) {
		return nil, fmt.Errorf("餐别必须是 lunch/fruit/dinner 之一，收到 %q", mealField)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.days {
		if s.days[i].Date == date {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%s 没有记录，无法加反馈", date)
	}
	old := s.days[idx].mealOf(mealField)
	if old == nil {
		return nil, fmt.Errorf("%s 的%s没有记录，无法加反馈", date, mealLabelOf(mealField))
	}

	clone := *old       // 浅拷贝：Dishes 切片共享（只读、不改内容），安全
	clone.Feedback = fb // 只在副本上写反馈
	s.days[idx].setMeal(mealField, &clone)

	if err := s.save(); err != nil {
		return nil, err
	}
	return &clone, nil
}

// save 全量落盘：先留 .bak，再走临时文件 + rename 原子替换。调用方必须已持有锁。
func (s *HistoryStore) save() error {
	raw, err := json.MarshalIndent(s.days, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化历史失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建历史目录失败: %w", err)
	}
	// .bak：覆盖写坏时的最近一版回退。尽力而为——留不下备份不该挡记账。
	if old, err := os.ReadFile(s.path); err == nil {
		_ = os.WriteFile(s.path+".bak", old, 0o644)
	}
	tmp, err := os.CreateTemp(dir, ".history-*.json")
	if err != nil {
		return fmt.Errorf("创建临时历史文件失败: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("写临时历史文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时历史文件失败: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("落盘历史失败: %w", err)
	}
	return nil
}
