package menu

// 这些是「离线、免 API」的白盒测试：只覆盖 menu 包里那几个纯函数
// （读盘、渲染、按天/按食材查、摊平成文档）。语义检索那条路依赖 embedder，
// 归 internal/vectorstore 的测试管，这里不碰——所以 `go test ./...` 始终离线可跑。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample 是一份手搓的迷你历史，刻意覆盖几种边界：
//   - 2025-12-18：有午餐 + 晚餐，没水果
//   - 2025-12-19：只有水果 + 晚餐，没午餐（对应真实历史里「漏记午餐」的日子）
//   - 2025-12-20：只有午餐，且带一道无 detail 的菜
//
// 按日期升序排列，和 history.json / recent_meals 的「取尾部即最近」约定一致。
var sample = []Day{
	{
		Date:   "2025-12-18",
		Lunch:  &Meal{Time: "11:30", Dishes: []Dish{{Name: "西兰花炒虾仁", Detail: "西兰花50g，虾仁3只"}}},
		Dinner: &Meal{Time: "17:30", Dishes: []Dish{{Name: "鳕鱼粥", Detail: "鳕鱼1块，米30g"}}},
	},
	{
		Date:   "2025-12-19",
		Fruit:  &Meal{Time: "15:00", Dishes: []Dish{{Name: "香蕉", Detail: ""}}},
		Dinner: &Meal{Time: "17:40", Dishes: []Dish{{Name: "南瓜米饭", Detail: "米50g，南瓜1块"}}},
	},
	{
		Date:  "2025-12-20",
		Lunch: &Meal{Time: "11:40", Dishes: []Dish{{Name: "羊肚菌鸡汤面", Detail: "羊肚菌2朵"}, {Name: "蒸蛋", Detail: ""}}},
	},
}

func TestLoadHistory(t *testing.T) {
	raw := `[
	  {"date":"2025-12-18","lunch":{"time":"11:30","dishes":[{"name":"西兰花炒虾仁","detail":"西兰花50g"}]},"dinner":{"time":"17:30","dishes":[{"name":"鳕鱼粥","detail":"鳕鱼1块"}]}},
	  {"date":"2025-12-19","fruit":{"time":"15:00","dishes":[{"name":"香蕉","detail":""}]}}
	]`
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("写测试文件失败: %v", err)
	}

	days, err := LoadHistory(path)
	if err != nil {
		t.Fatalf("LoadHistory 出错: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("期望 2 天，得到 %d", len(days))
	}
	// 第一天该有午餐 + 晚餐、没有水果。
	if days[0].Lunch == nil || days[0].Dinner == nil {
		t.Errorf("第一天 lunch/dinner 不该为 nil")
	}
	if days[0].Fruit != nil {
		t.Errorf("第一天没写 fruit，应解析成 nil，实际非 nil")
	}
	// 第二天只有水果——用指针区分「没这一餐」和「空餐」正是 Day 设计的用意。
	if days[1].Lunch != nil || days[1].Dinner != nil {
		t.Errorf("第二天没有 lunch/dinner，应为 nil")
	}
	if got := days[0].Lunch.Dishes[0].Name; got != "西兰花炒虾仁" {
		t.Errorf("菜名解析错误：%q", got)
	}
}

func TestLoadHistory_BadPath(t *testing.T) {
	if _, err := LoadHistory(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("读不存在的文件应报错")
	}
}

func TestMealOf(t *testing.T) {
	d := sample[0]
	if d.mealOf("lunch") != d.Lunch {
		t.Error(`mealOf("lunch") 应返回 Lunch 指针`)
	}
	if d.mealOf("fruit") != nil {
		t.Error("这天没有水果，mealOf 应返回 nil")
	}
	if d.mealOf("既不是午餐也不是晚餐") != nil {
		t.Error("未知 field 应返回 nil")
	}
}

func TestRenderMeal(t *testing.T) {
	got := renderMeal("2025-12-18", "晚餐", sample[0].Dinner)
	// 带时间的餐次应形如 "日期 标签(时间)：菜（明细）"。
	for _, want := range []string{"2025-12-18 晚餐(17:30)：", "鳕鱼粥（鳕鱼1块，米30g）"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderMeal 输出缺少 %q，实际：%q", want, got)
		}
	}

	// 无 detail 的菜只出菜名，不应带空括号 "（）"。
	got2 := renderMeal("2025-12-20", "午餐", sample[2].Lunch)
	if strings.Contains(got2, "蒸蛋（）") {
		t.Errorf("无明细的菜不该出现空括号，实际：%q", got2)
	}
	if !strings.Contains(got2, "蒸蛋") {
		t.Errorf("应包含无明细菜名「蒸蛋」，实际：%q", got2)
	}
}

func TestBuildDocuments(t *testing.T) {
	docs := BuildDocuments(sample)
	// 一餐一条、跳过缺席餐：18 号 2 餐 + 19 号 2 餐 + 20 号 1 餐 = 5。
	if len(docs) != 5 {
		t.Fatalf("期望 5 条文档，得到 %d", len(docs))
	}

	byID := map[string]bool{}
	for _, d := range docs {
		byID[d.ID] = true
	}
	if !byID["2025-12-18-lunch"] {
		t.Error("缺少 2025-12-18-lunch 文档")
	}
	// 19 号没有午餐，就不该有对应文档。
	if byID["2025-12-19-lunch"] {
		t.Error("19 号没午餐，不该生成 2025-12-19-lunch 文档")
	}

	// MetaData 里应留下 date / mealType，供下游还原归属。
	for _, d := range docs {
		if d.ID == "2025-12-18-dinner" {
			if d.MetaData["date"] != "2025-12-18" || d.MetaData["mealType"] != "dinner" {
				t.Errorf("MetaData 不正确：%v", d.MetaData)
			}
		}
	}
}

func TestMakeRecent(t *testing.T) {
	ctx := context.Background()
	recent := makeRecent(sample)

	// days<=0 默认回看 3 天；本例正好 3 天，应含全部日期。
	out, err := recent(ctx, recentInput{Days: 0})
	if err != nil {
		t.Fatalf("recent 出错: %v", err)
	}
	for _, date := range []string{"2025-12-18", "2025-12-19", "2025-12-20"} {
		if !strings.Contains(out, date) {
			t.Errorf("默认 3 天应包含 %s，实际：%q", date, out)
		}
	}

	// days=1 只取最近一天（尾部），不该带更早的日期。
	out1, _ := recent(ctx, recentInput{Days: 1})
	if !strings.Contains(out1, "2025-12-20") {
		t.Errorf("最近 1 天应含 2025-12-20，实际：%q", out1)
	}
	if strings.Contains(out1, "2025-12-18") {
		t.Errorf("最近 1 天不该含更早的 2025-12-18，实际：%q", out1)
	}

	// days 超过总天数应被夹到总数，不 panic。
	if _, err := recent(ctx, recentInput{Days: 999}); err != nil {
		t.Errorf("超量回看应正常返回，却出错: %v", err)
	}

	// 空历史应给出友好提示而非崩。
	empty := makeRecent(nil)
	if out, _ := empty(ctx, recentInput{Days: 3}); !strings.Contains(out, "历史为空") {
		t.Errorf("空历史应提示「历史为空」，实际：%q", out)
	}
}

func TestMakeFindByIngredient(t *testing.T) {
	ctx := context.Background()
	find := makeFindByIngredient(sample)

	// 命中菜名。
	if out, _ := find(ctx, ingredientInput{Ingredient: "鳕鱼"}); !strings.Contains(out, "鳕鱼粥") {
		t.Errorf("查『鳕鱼』应命中鳕鱼粥，实际：%q", out)
	}
	// 命中 detail 里的关键词。
	if out, _ := find(ctx, ingredientInput{Ingredient: "南瓜"}); !strings.Contains(out, "南瓜米饭") {
		t.Errorf("查『南瓜』应命中南瓜米饭，实际：%q", out)
	}
	// 查不到应明确说没有，而不是空串。
	if out, _ := find(ctx, ingredientInput{Ingredient: "牛肉"}); !strings.Contains(out, "没有出现过") {
		t.Errorf("查不存在的食材应提示没有，实际：%q", out)
	}
	// 空关键词应引导用户补充。
	if out, _ := find(ctx, ingredientInput{Ingredient: "  "}); !strings.Contains(out, "请给") {
		t.Errorf("空关键词应引导补充，实际：%q", out)
	}
}

func TestMealContains(t *testing.T) {
	m := sample[0].Dinner // 鳕鱼粥（鳕鱼1块，米30g）
	if !mealContains(m, "鳕鱼") {
		t.Error("菜名含『鳕鱼』应为 true")
	}
	if !mealContains(m, "米30g") {
		t.Error("明细含『米30g』应为 true")
	}
	if mealContains(m, "牛肉") {
		t.Error("不含『牛肉』应为 false")
	}
}
