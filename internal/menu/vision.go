package menu

// vision.go —— 图片解析（F1/F2）的转写核心（Option A：视觉模型只做「图→结构化文字」）。
//
// 视觉模型（豆包视觉，见 llm.NewVisionChatModel）不进 ReAct 循环，只被这里当成一个
// 「看图吐文字」的纯函数用：给它图片 + 提示词，让它把订单截图/菜单图读成结构化数据。
// 好处：deepseek 的 tool-calling 一点不动，历史/会话里也只留文本、不反复重传大 base64。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// DescribeImage 让视觉模型「看图说话」：把图片（base64）+提示词组成一条多模态用户消息，
// Generate 返回文字。eino-ext openai 组件消费 MultiContent + data:URI（实测与裸 Ark 调用一致）。
func DescribeImage(ctx context.Context, cm model.BaseChatModel, imageBase64, mimeType, prompt string) (string, error) {
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	dataURI := "data:" + mimeType + ";base64," + imageBase64
	msg := &schema.Message{
		Role: schema.User,
		MultiContent: []schema.ChatMessagePart{
			{Type: schema.ChatMessagePartTypeText, Text: prompt},
			{Type: schema.ChatMessagePartTypeImageURL, ImageURL: &schema.ChatMessageImageURL{
				URL:    dataURI,
				Detail: schema.ImageURLDetailAuto,
			}},
		},
	}
	out, err := cm.Generate(ctx, []*schema.Message{msg})
	if err != nil {
		return "", fmt.Errorf("视觉模型解析图片失败: %w", err)
	}
	return out.Content, nil
}

const orderParsePrompt = `这是一张买菜/生鲜订单截图。请提取其中购买的食材，只输出一个 JSON 数组，
每个元素形如 {"name":"鳕鱼","quantity":2,"unit":"块"}：
- name 是食材名；quantity 是数量（数字，看不清就填 1）；unit 是单位（块/份/个/袋/盒/斤/g 等，没有就填「份」）。
- 只提取食材类商品，忽略配送费/包装袋/优惠券等非食材行。
- 直接输出 JSON 数组，不要任何解释文字，不要用 markdown 代码块包裹。`

// ParseOrderImage 把订单截图解析成库存条目列表（供家长确认后再入库，本函数不写任何账本）。
func ParseOrderImage(ctx context.Context, cm model.BaseChatModel, imageBase64, mimeType string) ([]InventoryItem, error) {
	raw, err := DescribeImage(ctx, cm, imageBase64, mimeType, orderParsePrompt)
	if err != nil {
		return nil, err
	}
	items, err := parseInventoryJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("解析订单结果失败（模型输出：%s）: %w", truncate(raw, 200), err)
	}
	return items, nil
}

// parseInventoryJSON 从模型输出里抠出 JSON 数组并解析——容忍模型给 markdown 围栏或前后加话：
// 取第一个 '[' 到最后一个 ']' 之间的内容。
func parseInventoryJSON(raw string) ([]InventoryItem, error) {
	s := strings.TrimSpace(raw)
	i, j := strings.Index(s, "["), strings.LastIndex(s, "]")
	if i < 0 || j < 0 || j < i {
		return nil, fmt.Errorf("模型输出里没找到 JSON 数组")
	}
	var rows []struct {
		Name     string  `json:"name"`
		Quantity float64 `json:"quantity"`
		Unit     string  `json:"unit"`
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &rows); err != nil {
		return nil, err
	}
	items := make([]InventoryItem, 0, len(rows))
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		qty := r.Quantity
		if qty <= 0 {
			qty = 1 // 数量看不清/缺失，兜底 1，家长在确认页可改
		}
		unit := strings.TrimSpace(r.Unit)
		if unit == "" {
			unit = "份"
		}
		items = append(items, InventoryItem{Name: name, Quantity: qty, Unit: unit})
	}
	return items, nil
}

// truncate 截断字符串（按 rune，不切坏中文），给错误信息带一小段模型原始输出便于排查。
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
