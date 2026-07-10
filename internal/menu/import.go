package menu

// import.go —— F1「导入历史菜单」的解析核心（文字 + 图片两条路，都产出结构化 []Day）。
//
// 文字走文本模型（deepseek），图片走视觉模型（豆包视觉，见 vision.go）——两者都只做
// 「原始输入 → 结构化 JSON → []Day」的转写，不写任何账本。真正入库由家长在前端确认后走
// /api/history/import（ImportDays 批量落盘）。preview→确认→再入库，防坏解析静默污染历史。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const historyParsePrompt = `请把下面这份「宝宝的吃饭历史」整理成 JSON 数组，每个元素是一天：
{"date":"YYYY-MM-DD","lunch":{"time":"12:00","dishes":[{"name":"菜名","detail":"做法/分量，可空"}]},"fruit":{...},"dinner":{...}}
规则：
- date 必须是具体的 YYYY-MM-DD；能推断出年份就补全，实在看不出日期的整段跳过。
- 三餐 lunch(午餐)/fruit(水果)/dinner(晚餐)：哪餐没有就省略该键；每餐 dishes 至少一道菜。
- 只输出 JSON 数组本身，不要任何解释文字，不要用 markdown 代码块包裹。`

// promptWithToday 在解析提示后附上今天的日期——模型自己不知道今年几几年，不给它
// 「7月5日」这类没写年份的日期就无从补全 YYYY（和 agent 每轮注入当天日期同一个道理）。
func promptWithToday() string {
	return historyParsePrompt + fmt.Sprintf("\n（今天是 %s，据此推断没写年份的日期。）", time.Now().Format("2006-01-02"))
}

// ParseHistoryText 用文本模型把家长粘贴的历史文字整理成结构化 []Day（预览用，不写库）。
func ParseHistoryText(ctx context.Context, cm model.BaseChatModel, text string) ([]Day, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("导入内容为空")
	}
	out, err := cm.Generate(ctx, []*schema.Message{
		schema.UserMessage(promptWithToday() + "\n\n历史内容：\n" + text),
	})
	if err != nil {
		return nil, fmt.Errorf("解析历史文字失败: %w", err)
	}
	return parseHistoryJSON(out.Content)
}

// ParseHistoryImage 用视觉模型把菜单/历史截图整理成结构化 []Day（预览用，不写库）。
func ParseHistoryImage(ctx context.Context, cm model.BaseChatModel, imageBase64, mimeType string) ([]Day, error) {
	raw, err := DescribeImage(ctx, cm, imageBase64, mimeType, promptWithToday())
	if err != nil {
		return nil, err
	}
	return parseHistoryJSON(raw)
}

// mealJSON 是解析中间态（对齐模型输出的一餐）。
type mealJSON struct {
	Time   string      `json:"time"`
	Dishes []dishInput `json:"dishes"`
}

// parseHistoryJSON 把模型输出整理成 []Day：复用 extractJSONArray 抠数组（耐围栏/前后废话），
// 校验日期、清洗菜品、丢掉无效项。
func parseHistoryJSON(raw string) ([]Day, error) {
	body, err := extractJSONArray(raw)
	if err != nil {
		return nil, fmt.Errorf("模型输出里没找到有效结果（%s）", truncate(raw, 160))
	}
	var rows []struct {
		Date   string    `json:"date"`
		Lunch  *mealJSON `json:"lunch"`
		Fruit  *mealJSON `json:"fruit"`
		Dinner *mealJSON `json:"dinner"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		return nil, fmt.Errorf("解析历史 JSON 失败（%s）: %w", truncate(body, 160), err)
	}

	days := make([]Day, 0, len(rows))
	for _, r := range rows {
		date := strings.TrimSpace(r.Date)
		if _, e := time.Parse("2006-01-02", date); e != nil {
			continue // 日期非法/缺失，整天跳过（不污染历史升序）
		}
		d := Day{Date: date, Lunch: toMeal(r.Lunch), Fruit: toMeal(r.Fruit), Dinner: toMeal(r.Dinner)}
		if d.Lunch == nil && d.Fruit == nil && d.Dinner == nil {
			continue // 三餐全空
		}
		days = append(days, d)
	}
	// 合并同日期的多条（模型偶尔把一天拆成两条）——预览里每个日期只出现一次，
	// Day.id=date 不重复，前端 ForEach/左滑删不会错位。
	days = mergeDaysByDate(days)
	if len(days) == 0 {
		return nil, fmt.Errorf("没解析出有效的历史记录（日期需 YYYY-MM-DD、每餐至少一道菜）。模型输出：%s", truncate(body, 200))
	}
	return days, nil
}

// toMeal 把解析中间态转成领域 Meal，清洗空菜名；无有效菜品返回 nil（那一餐不算）。
func toMeal(mj *mealJSON) *Meal {
	if mj == nil {
		return nil
	}
	dishes := make([]Dish, 0, len(mj.Dishes))
	for _, d := range mj.Dishes {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			continue
		}
		dishes = append(dishes, Dish{Name: name, Detail: strings.TrimSpace(d.Detail)})
	}
	if len(dishes) == 0 {
		return nil
	}
	return &Meal{Time: strings.TrimSpace(mj.Time), Dishes: dishes}
}
