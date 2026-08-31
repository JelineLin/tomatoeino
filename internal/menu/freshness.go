package menu

// freshness.go —— 给库存加上时间维度。
//
// 这一步要解决的是账本的根本矛盾：份数永远不会准。半根黄瓜没人会去记 0.5，
// 漏扣一次账就和冰箱对不上，一旦对不上，agent 拿着不存在的食材推荐，推荐失去可信度，
// 家长更不想维护——账本就此废掉。
//
// 出路是把库存从「账本」降级成「线索」：不追求数量准，只追求【时机准】。
// 「西兰花是 4 天前买的、该吃了」这条信息，比「西兰花还剩 0.5 份」有用得多，
// 而且它只依赖入库时间——一个家长永远不用维护、系统自己就能记准的事实。
//
// 这也正是「时令 + 库存 + 搭配」里库存那一维真正该提供的东西：不是清点货架，
// 是告诉 agent 哪几样再不吃就要坏了。
//
// 和 season.go 一样是「计算型」：一张静态表 + 当前时间，没有外部依赖。

import (
	"strconv"
	"strings"
	"time"
)

// Freshness 是一条库存的新鲜度分档。
type Freshness string

const (
	FreshnessFresh   Freshness = "fresh"   // 还新鲜，正常安排
	FreshnessUseSoon Freshness = "use_soon" // 该吃了：过了保鲜期的一半，优先消耗
	FreshnessStale   Freshness = "stale"   // 可能已经吃完/坏了：超过保鲜期，别再当确定有
	FreshnessUnknown Freshness = "unknown" // 没有入库时间（旧数据/手工加的），不做判断
)

// shelfLife 是按品类的保鲜期（天）。刻意只分粗档——精确到小时既不可能也没意义，
// 我们要的只是「还早 / 该吃了 / 别指望了」三档。
//
// 分类靠菜名关键词匹配，从具体到宽泛依次尝试（见 classify）。匹配不到就用
// defaultShelfLife，宁可给个中庸值，也不要因为表里没收录就完全失去时间信号。
type shelfLifeRule struct {
	keywords []string
	days     int
	category string
}

// shelfLifeTable 顺序有意义：先匹配的优先。两条排序纪律：
//   ① 「冷冻」「速冻」这类修饰词排最前——冷冻虾仁该按冷冻算，不该按水产的 2 天算；
//   ② 会被别的规则误吞的具体食材，要排在那条规则【之前】。典型是玉米：
//      主食那条有「米」，子串匹配会把玉米判成能放一个月（线上真实库存里抓到的）。
//
// 关键词一律是子串匹配，所以宁可写长（「大米」而不是「米」），也别图省事写短——
// 短关键词的误伤是静默的：家长看不出为什么玉米一直不提醒吃。
var shelfLifeTable = []shelfLifeRule{
	{[]string{"冷冻", "速冻", "冰冻"}, 30, "冷冻"},
	// 玉米/糯米椒之类要抢在「主食」前面，否则被「米」吞掉。
	{[]string{"玉米", "甜豆", "荷兰豆"}, 7, "嫩菜"},
	{[]string{"生菜", "菠菜", "小白菜", "青菜", "油菜", "茼蒿", "苋菜", "韭菜", "香菜", "芹菜", "空心菜", "娃娃菜", "上海青", "鸡毛菜"}, 3, "叶菜"},
	{[]string{"卷心菜", "包菜", "圆白菜", "紫甘蓝", "大白菜"}, 10, "耐放菜"},
	{[]string{"西兰花", "花菜", "菜花", "西蓝花", "芦笋", "豆苗", "秋葵", "豌豆", "毛豆", "四季豆", "豇豆"}, 5, "嫩菜"},
	{[]string{"蘑菇", "香菇", "金针菇", "杏鲍菇", "平菇", "木耳", "银耳"}, 5, "菌菇"},
	{[]string{"西红柿", "番茄", "黄瓜", "茄子", "青椒", "彩椒", "丝瓜", "苦瓜", "冬瓜", "南瓜", "西葫芦"}, 7, "瓜果类"},
	{[]string{"土豆", "红薯", "紫薯", "山药", "萝卜", "胡萝卜", "洋葱", "莲藕", "芋头", "生姜", "大蒜"}, 14, "根茎"},
	{[]string{"鱼", "虾", "蟹", "贝", "蛤", "鱿鱼", "鳕", "鲈", "带鱼", "黄鱼", "海鲜"}, 2, "水产"},
	{[]string{"猪肉", "牛肉", "羊肉", "鸡肉", "鸭肉", "牛排", "肉末", "里脊", "排骨", "棒骨", "筒骨", "龙骨", "鸡胸", "鸡腿", "翅", "鸡爪", "肉"}, 3, "鲜肉"},
	{[]string{"豆腐", "豆干", "腐竹", "豆皮", "豆制品"}, 3, "豆制品"},
	{[]string{"牛奶", "酸奶", "奶酪"}, 7, "奶制品"},
	{[]string{"鸡蛋", "鸭蛋", "鹌鹑蛋", "蛋"}, 20, "蛋类"},
	{[]string{"草莓", "蓝莓", "树莓", "杨梅", "荔枝", "葡萄", "桃", "李", "杏"}, 4, "娇嫩水果"},
	{[]string{"香蕉", "芒果", "牛油果", "火龙果", "猕猴桃", "梨", "橙", "橘", "苹果", "柚"}, 10, "耐放水果"},
	// 只写具体的主食名，不写裸「米」「面」——「米」会吞玉米/小米椒，「面」会吞面包糠之类。
	{[]string{"大米", "粳米", "小米", "糙米", "挂面", "意面", "面条", "米粉", "河粉", "馒头", "年糕", "面粉"}, 30, "主食"},
}

// defaultShelfLife 是表里匹配不到时的兜底天数。取 7 天：比叶菜宽松、比根茎严格，
// 错的方向也安全——最多让 agent 早一点提醒吃掉，不会让它拿着两周前的东西当新鲜的用。
const defaultShelfLife = 7

// classify 按菜名判定保鲜期和品类。匹配不到返回默认值和空品类。
func classify(name string) (days int, category string) {
	n := strings.TrimSpace(name)
	for _, rule := range shelfLifeTable {
		for _, kw := range rule.keywords {
			if strings.Contains(n, kw) {
				return rule.days, rule.category
			}
		}
	}
	return defaultShelfLife, ""
}

// FreshnessOf 算一条库存在 now 时刻的新鲜度分档和已放置天数。
//
// 没有入库时间（旧数据、直接改过 JSON）→ unknown，天数返回 -1：
// 这时候【不猜】。把没有时间戳的东西当成很久以前买的，会让 agent 无端避开
// 家里明明有的食材；当成刚买的又会一直催着吃。不知道就说不知道。
func FreshnessOf(it InventoryItem, now time.Time) (Freshness, int) {
	if strings.TrimSpace(it.UpdatedAt) == "" {
		return FreshnessUnknown, -1
	}
	t, err := time.Parse(time.RFC3339, it.UpdatedAt)
	if err != nil {
		return FreshnessUnknown, -1
	}
	// 按自然日算，不按 24 小时：家长心里的「昨天买的」是日历上的昨天。
	days := int(now.Truncate(24 * time.Hour).Sub(t.Truncate(24 * time.Hour)).Hours() / 24)
	if days < 0 {
		days = 0 // 时钟回拨/时区差，别出现负数天
	}
	life, _ := classify(it.Name)
	switch {
	case days > life:
		return FreshnessStale, days
	case days*2 >= life:
		// 过半就提醒，不等到最后一天——最后一天才说，家长当天没空做就真浪费了。
		return FreshnessUseSoon, days
	default:
		return FreshnessFresh, days
	}
}

// freshnessLabel 把分档渲染成给模型和界面看的人话。
func freshnessLabel(f Freshness, days int) string {
	switch f {
	case FreshnessUseSoon:
		return "该吃了（放了" + strconv.Itoa(days) + "天）"
	case FreshnessStale:
		return "可能已经吃完或坏了（放了" + strconv.Itoa(days) + "天）"
	case FreshnessFresh:
		if days <= 0 {
			return "今天刚买"
		}
		return "放了" + strconv.Itoa(days) + "天"
	default:
		return "入库时间不详"
	}
}
