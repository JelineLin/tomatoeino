package menu

// inventory_parse.go —— 一句话入库：把家长随口说的「买了两块鳕鱼、一个西兰花」
// 解析成库存条目。
//
// 为什么要有它：入库原本只有「截图订单 → 视觉模型解析」一条路，四步起跳，还在
// 相册里堆一地垃圾截图——摩擦全在这一端，家长自然就不记了，账本跟着失准。
// 说一句话是最短的入库动作，iOS 端接现成的语音输入（SpeechDictator），
// 家长买完菜在电梯里说一句就完事。
//
// 和 vision.go 的 ParseOrderImage 是同一件事的两个入口：图片走视觉模型、文字走聊天
// 模型，出口都汇到 parseInventoryJSON。模型输出不守规矩的那套宽容策略（前后加话、
// markdown 围栏、quantity 给成字符串）因此只写一遍，两个入口一起受益。

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// textParsePrompt 针对【语音转写】调教：口语数量（「两」「半个」「一包」）、
// 同一样说两遍、以及顺口带出的废话，都要能扛住。
const textParsePrompt = `家长刚买完菜，用一句话说了买了什么（多半是语音转写，可能有错别字和口语化的数量）。
请提取其中的食材，只输出一个 JSON 数组，每个元素形如 {"name":"鳕鱼","quantity":2,"unit":"块"}：
- name 是食材名，去掉「买了」「还有」这类口语；
- quantity 用阿拉伯数字（「两」→2、「半个」→0.5、「一打」→12）；没说数量就填 1；
- unit 取家长说的单位（块/份/个/袋/盒/斤/g 等），没说就填「份」；
- 只提取买回来的食材，其他闲话一律忽略；同一样食材说了多次就合并成一条；
- 一样食材都没提到就输出空数组 []。
- 直接输出 JSON 数组，不要任何解释文字，不要用 markdown 代码块包裹。

家长说：`

// ParseInventoryText 把一句话解析成库存条目列表，供家长确认后再批量入库。
// 和 ParseOrderImage 一样：本函数【不写任何账本】，解析结果必须过一遍家长的眼睛——
// 模型听错一个字就多记一样东西，静默改账比不记还糟。
func ParseInventoryText(ctx context.Context, cm model.BaseChatModel, text string) ([]InventoryItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("没有可解析的文字")
	}
	out, err := cm.Generate(ctx, []*schema.Message{schema.UserMessage(textParsePrompt + text)})
	if err != nil {
		return nil, fmt.Errorf("模型解析入库文字失败: %w", err)
	}
	items, err := parseInventoryJSON(out.Content)
	if err != nil {
		return nil, fmt.Errorf("解析入库文字结果失败（模型输出：%s）: %w", truncate(out.Content, 200), err)
	}
	return items, nil
}
