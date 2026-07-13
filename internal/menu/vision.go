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
	"strconv"
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

// parseInventoryJSON 从模型输出里稳妥地抠出 JSON 数组并解析。
// 对模型输出的不守规矩要宽容（视觉模型常越界）：
//   - 前后加话/加 markdown 围栏 → extractJSONArray 只取真正的数组段；
//   - 某格 quantity 给成字符串（"2"、"2块"）→ flexQty 逐格兜底，绝不因一格拖垮整批。
func parseInventoryJSON(raw string) ([]InventoryItem, error) {
	body, err := extractJSONArray(raw)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Name     string  `json:"name"`
		Quantity flexQty `json:"quantity"`
		Unit     string  `json:"unit"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		return nil, err
	}
	items := make([]InventoryItem, 0, len(rows))
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		unit := strings.TrimSpace(r.Unit)
		if unit == "" {
			unit = "份"
		}
		items = append(items, InventoryItem{Name: name, Quantity: float64(r.Quantity), Unit: unit})
	}
	return items, nil
}

// flexQty 宽松解析数量：接受 JSON 数字(2)、也接受字符串("2"、"2块"、null)——
// 单格解析不了兜底 1（家长在确认页可改），UnmarshalJSON 永不报错，不拖垮整批。
type flexQty float64

func (q *flexQty) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	*q = flexQty(parseQty(s))
	return nil
}

// parseQty 从字符串开头抠出数字（容忍 "2块" "2.5" 这类尾随单位），失败/非正兜底 1。
func parseQty(s string) float64 {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] == '.' || (s[end] >= '0' && s[end] <= '9')) {
		end++
	}
	if end == 0 {
		return 1
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil || f <= 0 {
		return 1
	}
	return f
}

// extractJSONArray 从模型输出里稳妥地抠出【第一个完整的】JSON 数组。
// 比"第一个[到最后一个]"耐造：① 先剥 ```json 围栏；② 找一个 '[' 且其后紧跟 '{' 或 ']'
// 作为数组起点（跳过 "根据订单[图1]" 这类前置括号废话）；③ 从起点做括号匹配（跳字符串内的括号）
// 找到配对的 ']'，这样即便模型给了「示例数组 + 真数组」两段，也只取第一段完整的。
func extractJSONArray(raw string) (string, error) {
	s := stripCodeFence(strings.TrimSpace(raw))
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\r' || s[j] == '\t') {
			j++
		}
		if j < len(s) && (s[j] == '{' || s[j] == ']') {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("模型输出里没找到 JSON 数组")
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("JSON 数组未闭合")
}

// stripCodeFence 去掉 ```json … ``` / ``` … ``` 围栏（只剥围栏，不动内容）。
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:] // 去掉首行 ```xxx
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// truncate 截断字符串（按 rune，不切坏中文），给错误信息带一小段模型原始输出便于排查。
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
