package menu

// 库存账本的离线测试：入库累加、出库扣减/清零出清、模糊匹配/歧义、
// 落盘-重开一致性（真正的持久化验证）。全部走 t.TempDir()，不碰真实账本。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *InventoryStore {
	t.Helper()
	s, err := NewInventoryStore(filepath.Join(t.TempDir(), "inv.json"))
	if err != nil {
		t.Fatalf("建库存失败: %v", err)
	}
	return s
}

func TestInventory_AddAccumulates(t *testing.T) {
	s := newTestStore(t)

	it, err := s.Add("鳕鱼", 2, "块")
	if err != nil || it.Quantity != 2 || it.Unit != "块" {
		t.Fatalf("首次入库异常: %+v err=%v", it, err)
	}
	// 同名累加（充值语义），不传单位沿用已有。
	it, err = s.Add("鳕鱼", 1.5, "")
	if err != nil || it.Quantity != 3.5 || it.Unit != "块" {
		t.Fatalf("累加异常: %+v err=%v", it, err)
	}
	// 新名字默认单位「份」。
	it, _ = s.Add("西兰花", 0.5, "")
	if it.Unit != "份" || it.Quantity != 0.5 {
		t.Errorf("默认单位应为份: %+v", it)
	}
	// 非法份数。
	if _, err := s.Add("空气", 0, ""); err == nil {
		t.Error("份数 0 应报错")
	}
	if _, err := s.Add("  ", 1, ""); err == nil {
		t.Error("空名应报错")
	}
}

func TestInventory_ConsumeAndDeplete(t *testing.T) {
	s := newTestStore(t)
	s.Add("鳕鱼", 3, "块")

	// 正常扣减。
	it, depleted, err := s.Consume("鳕鱼", 1)
	if err != nil || depleted || it.Quantity != 2 {
		t.Fatalf("扣减异常: %+v depleted=%v err=%v", it, depleted, err)
	}
	// 超扣：清零出清，不记负数。
	it, depleted, err = s.Consume("鳕鱼", 99)
	if err != nil || !depleted || it.Quantity != 0 {
		t.Fatalf("超扣应清零出清: %+v depleted=%v err=%v", it, depleted, err)
	}
	// 出清后条目消失。
	if len(s.List("")) != 0 {
		t.Errorf("出清后不该还有条目: %v", s.List(""))
	}
	// 扣不存在的。
	if _, _, err := s.Consume("牛肉", 1); err == nil {
		t.Error("库存没有的食材应报错")
	}
}

func TestInventory_ConsumeFuzzyAndAmbiguous(t *testing.T) {
	s := newTestStore(t)
	s.Add("鳕鱼块", 2, "块")

	// 子串匹配：说「鳕鱼」能命中「鳕鱼块」。
	it, _, err := s.Consume("鳕鱼", 1)
	if err != nil || it.Name != "鳕鱼块" || it.Quantity != 1 {
		t.Fatalf("子串匹配失败: %+v err=%v", it, err)
	}

	// 歧义：两条都含「鱼」时应报歧义并列出候选。
	s.Add("三文鱼", 1, "块")
	_, _, err = s.Consume("鱼", 1)
	if err == nil || !strings.Contains(err.Error(), "多条") {
		t.Errorf("多重匹配应报歧义: %v", err)
	}
}

func TestInventory_PersistRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inv.json")
	s1, _ := NewInventoryStore(path)
	s1.Add("鳕鱼", 2, "块")
	s1.Add("西红柿", 3, "个")
	s1.Consume("鳕鱼", 0.5)

	// 重开一个 store（模拟服务重启），账目应完全一致。
	s2, err := NewInventoryStore(path)
	if err != nil {
		t.Fatalf("重开账本失败: %v", err)
	}
	items := s2.List("")
	if len(items) != 2 {
		t.Fatalf("重开后应有 2 条，实际 %d", len(items))
	}
	if items[0].Name != "鳕鱼" || items[0].Quantity != 1.5 || items[0].Unit != "块" {
		t.Errorf("鳕鱼账目不一致: %+v", items[0])
	}
	if items[1].Name != "西红柿" || items[1].Quantity != 3 {
		t.Errorf("西红柿账目不一致: %+v", items[1])
	}
}

func TestInventory_ListKeywordAndCopy(t *testing.T) {
	s := newTestStore(t)
	s.Add("鳕鱼", 2, "块")
	s.Add("西兰花", 1, "")

	if got := s.List("鱼"); len(got) != 1 || got[0].Name != "鳕鱼" {
		t.Errorf("关键词过滤失败: %v", got)
	}
	// List 返回副本：改它不该影响账本。
	all := s.List("")
	all[0].Quantity = 999
	if s.List("")[0].Quantity == 999 {
		t.Error("List 应返回副本，账本被外部修改了")
	}
}

func TestFmtQty(t *testing.T) {
	if fmtQty(2) != "2" || fmtQty(0.5) != "0.5" || fmtQty(1.25) != "1.25" {
		t.Errorf("份数渲染异常: %s %s %s", fmtQty(2), fmtQty(0.5), fmtQty(1.25))
	}
}

func TestInventoryTools(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 空账本提示。
	list := makeListInventory(s)
	if out, _ := list(ctx, invListInput{}); !strings.Contains(out, "空") {
		t.Errorf("空账本应有提示: %q", out)
	}

	// 入库 → 列表可见。
	add := makeAddInventory(s)
	if out, _ := add(ctx, invAddInput{Name: "鳕鱼", Quantity: 2, Unit: "块"}); !strings.Contains(out, "2块") {
		t.Errorf("入库回执应带份数: %q", out)
	}
	if out, _ := list(ctx, invListInput{}); !strings.Contains(out, "鳕鱼：2块") {
		t.Errorf("列表应显示鳕鱼: %q", out)
	}

	// 出库回执。
	consume := makeConsumeInventory(s)
	if out, _ := consume(ctx, invConsumeInput{Name: "鳕鱼", Quantity: 0.5}); !strings.Contains(out, "1.5块") {
		t.Errorf("出库回执应带剩余: %q", out)
	}
	// 出清回执。
	if out, _ := consume(ctx, invConsumeInput{Name: "鳕鱼", Quantity: 9}); !strings.Contains(out, "用完") {
		t.Errorf("出清应明说: %q", out)
	}
	// 工具层的错误要变成人话（nil error + 提示文本），让模型能自我修正。
	if out, err := consume(ctx, invConsumeInput{Name: "牛肉", Quantity: 1}); err != nil || !strings.Contains(out, "失败") {
		t.Errorf("出库失败应还人话不还 error: %q err=%v", out, err)
	}
}
