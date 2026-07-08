package menu

// 这是路线图里「计算型数据源」的第一课：不调外部 API、不进向量库，
// 一张静态时令表 + 当前日期就能成为一个工具——像清算系统里的「交易日历」，
// 数据本身是死的，价值在于用「今天」去查出那一条。
//
// 表按月份组织（中国通用时令，偏华东），内容只挑适合幼儿的食材：
// 好嚼、低敏、常见易买；螃蟹/贝类等易敏食材刻意不进表。

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// seasonEntry 是某个月的时令食材清单。
type seasonEntry struct {
	Veg     []string // 应季蔬菜
	Fruit   []string // 应季水果
	Aquatic []string // 应季水产（挑幼儿适合的）
	Tip     string   // 这个时节的备餐提示
}

// seasonTable 一月一条，全年 12 条。改时令认知只动这张表，工具逻辑不用碰。
var seasonTable = map[time.Month]seasonEntry{
	time.January: {
		Veg:     []string{"大白菜", "白萝卜", "菠菜", "山药", "南瓜"},
		Fruit:   []string{"橙子", "砂糖橘", "苹果"},
		Aquatic: []string{"鳕鱼", "鲈鱼", "带鱼"},
		Tip:     "深冬适合炖煮，根茎类炖到软烂，汤面暖胃。",
	},
	time.February: {
		Veg:     []string{"菠菜", "白萝卜", "山药", "豌豆苗", "芥蓝"},
		Fruit:   []string{"草莓", "砂糖橘", "苹果"},
		Aquatic: []string{"鲈鱼", "黄鱼"},
		Tip:     "冬末春初绿叶菜多起来；草莓上市，流水多洗一会儿再给。",
	},
	time.March: {
		Veg:     []string{"荠菜", "菠菜", "莴笋", "芦笋", "春笋"},
		Fruit:   []string{"草莓", "菠萝"},
		Aquatic: []string{"鳜鱼", "鲈鱼"},
		Tip:     "春笋纤维粗，取嫩尖切碎煮软；荠菜适合剁馅做小馄饨。",
	},
	time.April: {
		Veg:     []string{"芦笋", "豌豆", "蚕豆", "莴笋", "菠菜"},
		Fruit:   []string{"草莓", "枇杷"},
		Aquatic: []string{"鳜鱼", "虾"},
		Tip:     "豆类务必煮透压软更好消化；蚕豆首次少量尝试观察反应。",
	},
	time.May: {
		Veg:     []string{"豌豆", "黄瓜", "西葫芦", "苋菜"},
		Fruit:   []string{"樱桃", "枇杷", "桑葚"},
		Aquatic: []string{"虾", "黄鱼"},
		Tip:     "樱桃一定去核切半防呛噎；桑葚易染色但营养好。",
	},
	time.June: {
		Veg:     []string{"黄瓜", "西红柿", "丝瓜", "毛豆", "苋菜"},
		Fruit:   []string{"杨梅", "桃子", "西瓜"},
		Aquatic: []string{"鲈鱼", "虾"},
		Tip:     "入夏清淡为主，瓜类利水；西瓜适量、常温不贪凉。",
	},
	time.July: {
		Veg:     []string{"丝瓜", "冬瓜", "空心菜", "玉米", "毛豆"},
		Fruit:   []string{"桃子", "葡萄", "西瓜"},
		Aquatic: []string{"虾", "鲈鱼"},
		Tip:     "天热食欲差，可做瓜类汤面、蔬菜粥；葡萄去皮去籽切半。",
	},
	time.August: {
		Veg:     []string{"冬瓜", "丝瓜", "茄子", "玉米", "秋葵"},
		Fruit:   []string{"葡萄", "桃子", "梨"},
		Aquatic: []string{"虾", "鲈鱼"},
		Tip:     "立秋前后仍热，冬瓜汤解腻；梨可以蒸软了吃。",
	},
	time.September: {
		Veg:     []string{"南瓜", "莲藕", "山药", "西兰花", "菠菜"},
		Fruit:   []string{"梨", "葡萄", "石榴"},
		Aquatic: []string{"鲈鱼", "带鱼"},
		Tip:     "入秋干燥，藕和梨润燥；石榴籽多，榨汁过滤后再给。",
	},
	time.October: {
		Veg:     []string{"莲藕", "南瓜", "山药", "西兰花", "白萝卜"},
		Fruit:   []string{"苹果", "梨", "柿子", "猕猴桃"},
		Aquatic: []string{"鲈鱼", "带鱼"},
		Tip:     "柿子别空腹、少量；根茎类蒸炖皆宜。",
	},
	time.November: {
		Veg:     []string{"大白菜", "白萝卜", "菠菜", "山药", "西兰花"},
		Fruit:   []string{"苹果", "橙子", "猕猴桃"},
		Aquatic: []string{"带鱼", "鳕鱼"},
		Tip:     "转凉多炖煮，萝卜炖汤又软又甜。",
	},
	time.December: {
		Veg:     []string{"大白菜", "白萝卜", "菠菜", "红薯", "山药"},
		Fruit:   []string{"橙子", "苹果", "砂糖橘"},
		Aquatic: []string{"鳕鱼", "带鱼"},
		Tip:     "深冬暖食为主，红薯山药蒸软可直接当主食搭配。",
	},
}

// Season 是一个月的时令清单的对外形态（JSON 字段小写，直接喂给 HTTP 层）。
// seasonEntry 是包内存储格式，这里是给前端的「报文格式」——两者分开，
// 以后表结构怎么改，对外契约不动。
type Season struct {
	Month   int      `json:"month"`
	Veg     []string `json:"veg"`
	Fruit   []string `json:"fruit"`
	Aquatic []string `json:"aquatic"`
	Tip     string   `json:"tip"`
}

// SeasonFor 返回某个月的时令清单。工具走 renderSeason（人话给模型），
// HTTP 端点走这里（结构化给前端），同一张表两个出口。
func SeasonFor(m time.Month) Season {
	e := seasonTable[m]
	return Season{
		Month:   int(m),
		Veg:     e.Veg,
		Fruit:   e.Fruit,
		Aquatic: e.Aquatic,
		Tip:     e.Tip,
	}
}

// renderSeason 把一个月的时令条目渲染成模型读得懂的人话。
func renderSeason(m time.Month, e seasonEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d月时令：\n", int(m))
	fmt.Fprintf(&b, "- 应季蔬菜：%s\n", strings.Join(e.Veg, "、"))
	fmt.Fprintf(&b, "- 应季水果：%s\n", strings.Join(e.Fruit, "、"))
	fmt.Fprintf(&b, "- 应季水产：%s\n", strings.Join(e.Aquatic, "、"))
	fmt.Fprintf(&b, "- 备餐提示：%s", e.Tip)
	return b.String()
}

// ---- seasonal_produce 工具 ----

type seasonInput struct {
	Month int `json:"month" jsonschema:"description=要查询的月份 1~12；不传或 0 表示当前月份"`
}

// makeSeasonal 返回工具闭包。「现在几点」通过 now 注入而不是直接调 time.Now——
// 和清算系统把「今天是哪个交易日」做成可注入是同一个道理：测试时能把时钟
// 拨到任意月份，断言输出稳定，不用等真实季节轮转。
func makeSeasonal(now func() time.Time) func(context.Context, seasonInput) (string, error) {
	return func(ctx context.Context, in seasonInput) (string, error) {
		m := in.Month
		if m == 0 {
			m = int(now().Month())
		}
		toolLog(ctx, "seasonal_produce(month=%d)", m)
		if m < 1 || m > 12 {
			return fmt.Sprintf("月份 %d 不合法，请传 1~12（不传则默认当前月份）。", m), nil
		}
		return renderSeason(time.Month(m), seasonTable[time.Month(m)]), nil
	}
}
