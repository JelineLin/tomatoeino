package menu

// 新鲜度：分档阈值、旧数据不猜、排序口径。全离线，注入固定时钟。

import (
	"testing"
	"time"
)

func atDaysAgo(now time.Time, d int) string {
	return now.AddDate(0, 0, -d).Format(time.RFC3339)
}

// 分档阈值：过半 → 该吃了；超期 → 可能没了。按品类保鲜期各自算。
func TestFreshnessOf_Buckets(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name string // 食材名决定保鲜期：菠菜=叶菜3天，胡萝卜=根茎14天
		days int
		want Freshness
	}{
		{"菠菜", 0, FreshnessFresh},
		{"菠菜", 1, FreshnessFresh},   // 1*2=2 < 3
		{"菠菜", 2, FreshnessUseSoon}, // 2*2=4 >= 3
		{"菠菜", 3, FreshnessUseSoon}, // 正好到期日仍算「该吃了」
		{"菠菜", 4, FreshnessStale},
		{"胡萝卜", 5, FreshnessFresh},
		{"胡萝卜", 7, FreshnessUseSoon},
		{"胡萝卜", 20, FreshnessStale},
		{"鳕鱼", 1, FreshnessUseSoon}, // 水产 2 天，隔天就该吃
		{"鳕鱼", 3, FreshnessStale},
	}
	for _, c := range cases {
		it := InventoryItem{Name: c.name, Quantity: 1, Unit: "份", UpdatedAt: atDaysAgo(now, c.days)}
		got, days := FreshnessOf(it, now)
		if got != c.want {
			t.Errorf("%s 放了 %d 天：期望 %s，实际 %s", c.name, c.days, c.want, got)
		}
		if days != c.days {
			t.Errorf("%s 天数算错：期望 %d，实际 %d", c.name, c.days, days)
		}
	}
}

// 冷冻修饰词要盖过食材本身——冷冻虾仁该按 30 天算，不是水产的 2 天。
func TestFreshnessOf_FrozenBeatsIngredient(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	it := InventoryItem{Name: "冷冻虾仁", Quantity: 1, Unit: "袋", UpdatedAt: atDaysAgo(now, 5)}
	if got, _ := FreshnessOf(it, now); got != FreshnessFresh {
		t.Errorf("冷冻虾仁放 5 天应仍新鲜，实际 %s", got)
	}
}

// 没有入库时间就【不猜】：当成很久以前会让 agent 无端避开家里真有的东西，
// 当成刚买的又会一直催着吃。
func TestFreshnessOf_UnknownWhenNoTimestamp(t *testing.T) {
	now := time.Now()
	for _, bad := range []string{"", "   ", "昨天", "2026/08/01"} {
		it := InventoryItem{Name: "菠菜", Quantity: 1, Unit: "份", UpdatedAt: bad}
		got, days := FreshnessOf(it, now)
		if got != FreshnessUnknown || days != -1 {
			t.Errorf("UpdatedAt=%q 应为 unknown/-1，实际 %s/%d", bad, got, days)
		}
	}
}

// ListFresh 排序：stale → use_soon → fresh → unknown，同档内放得久的在前。
func TestListFresh_OrdersByUrgency(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	s := newTestStore(t)
	s.now = func() time.Time { return now }

	// 直接铺账本，绕开 Add 的「入库即刷新时间戳」。
	s.items = []InventoryItem{
		{Name: "苹果", Quantity: 1, Unit: "个", UpdatedAt: atDaysAgo(now, 1)},  // 耐放水果 10 天 → fresh
		{Name: "陈年不明物", Quantity: 1, Unit: "份"},                            // 无时间戳 → unknown
		{Name: "菠菜", Quantity: 1, Unit: "份", UpdatedAt: atDaysAgo(now, 9)},  // 叶菜 3 天 → stale
		{Name: "胡萝卜", Quantity: 1, Unit: "根", UpdatedAt: atDaysAgo(now, 8)}, // 根茎 14 天 → use_soon
	}

	got := make([]string, 0, 4)
	for _, it := range s.ListFresh("") {
		got = append(got, it.Name)
	}
	want := []string{"菠菜", "胡萝卜", "苹果", "陈年不明物"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序应为 %v，实际 %v", want, got)
		}
	}
}

// 入库刷新时间戳、出库不刷新——吃掉一半不会让剩下那半变新鲜。
func TestInventory_ConsumeDoesNotRefreshTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	s := newTestStore(t)
	s.now = func() time.Time { return now }

	if _, err := s.Add("西兰花", 2, "份"); err != nil {
		t.Fatal(err)
	}
	stamped := s.List("")[0].UpdatedAt
	if stamped == "" {
		t.Fatal("入库应写上时间戳")
	}

	// 三天后吃掉一份：时间戳不该动。
	s.now = func() time.Time { return now.AddDate(0, 0, 3) }
	if _, _, err := s.Consume("西兰花", 1); err != nil {
		t.Fatal(err)
	}
	if after := s.List("")[0].UpdatedAt; after != stamped {
		t.Errorf("出库不该刷新时间戳：原 %s，现 %s", stamped, after)
	}

	// 但补货要刷新。
	if _, err := s.Add("西兰花", 1, ""); err != nil {
		t.Fatal(err)
	}
	if after := s.List("")[0].UpdatedAt; after == stamped {
		t.Error("补货应刷新时间戳")
	}
}

// 分类器的误伤防线：短关键词（米/面/肉）会静默吞掉别的食材，
// 这里钉死线上真实库存里抓到过的那几个。
func TestClassify_RealWorldNames(t *testing.T) {
	cases := []struct {
		name string
		cat  string
		days int
	}{
		{"甜脆玉米", "嫩菜", 7},        // 曾被「米」误判成主食（30天）
		{"卷心菜", "耐放菜", 10},
		{"鸡中翅膀", "鲜肉", 3},
		{"香糯紫薯", "根茎", 14},
		{"黑猪棒骨", "鲜肉", 3},
		{"澳洲谷饲肉眼牛排", "鲜肉", 3},
		{"空心菜(嫩尖)", "叶菜", 3},
		{"切片冬瓜", "瓜果类", 7},
		{"有机迷你水果小洋葱", "根茎", 14},
		{"鸡蛋", "蛋类", 20},
		{"冷冻虾仁", "冷冻", 30},      // 修饰词必须压过水产
		{"东北大米", "主食", 30},
	}
	for _, c := range cases {
		days, cat := classify(c.name)
		if cat != c.cat || days != c.days {
			t.Errorf("%s：期望 %s/%d 天，实际 %s/%d 天", c.name, c.cat, c.days, cat, days)
		}
	}
}
