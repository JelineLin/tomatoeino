package menu

import "testing"

// 视觉模型的输出常不守规矩——审查抓到的：字符串数量、前后加话、markdown 围栏、多段数组。
// parseInventoryJSON 必须都能稳妥抠出，且单格坏值兜底而非整批失败。
func TestParseInventoryJSON_Robust(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []InventoryItem
	}{
		{
			name: "标准数字",
			raw:  `[{"name":"鳕鱼","quantity":2,"unit":"块"}]`,
			want: []InventoryItem{{Name: "鳕鱼", Quantity: 2, Unit: "块"}},
		},
		{
			name: "数量是字符串",
			raw:  `[{"name":"鳕鱼","quantity":"2","unit":"块"},{"name":"西兰花","quantity":"1","unit":"份"}]`,
			want: []InventoryItem{{Name: "鳕鱼", Quantity: 2, Unit: "块"}, {Name: "西兰花", Quantity: 1, Unit: "份"}},
		},
		{
			name: "数量带尾随单位 2块",
			raw:  `[{"name":"鳕鱼","quantity":"2块","unit":"块"}]`,
			want: []InventoryItem{{Name: "鳕鱼", Quantity: 2, Unit: "块"}},
		},
		{
			name: "数量缺失/看不清兜底1",
			raw:  `[{"name":"鳕鱼","quantity":"","unit":""}]`,
			want: []InventoryItem{{Name: "鳕鱼", Quantity: 1, Unit: "份"}},
		},
		{
			name: "前置带方括号废话",
			raw:  "根据订单截图[图1]提取如下：\n[{\"name\":\"鳕鱼\",\"quantity\":2,\"unit\":\"块\"}]",
			want: []InventoryItem{{Name: "鳕鱼", Quantity: 2, Unit: "块"}},
		},
		{
			name: "markdown 围栏",
			raw:  "```json\n[{\"name\":\"鳕鱼\",\"quantity\":2,\"unit\":\"块\"}]\n```",
			want: []InventoryItem{{Name: "鳕鱼", Quantity: 2, Unit: "块"}},
		},
		{
			name: "示例数组+真数组，只取第一段完整的",
			raw:  `示例 [{"name":"示例菜","quantity":1,"unit":"份"}]，实际见下`,
			want: []InventoryItem{{Name: "示例菜", Quantity: 1, Unit: "份"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseInventoryJSON(c.raw)
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("条数 %d != %d：%+v", len(got), len(c.want), got)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("第 %d 条 %+v != %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}
