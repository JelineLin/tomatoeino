// 例子 02：备餐 ReAct agent（命令行版）
//
// 学习目标：把前面学的零件——ChatModel（例 01）、Embedder + 向量库（L1/L2）、
// embedding 原理（例 03）——串成一个真正的 agent：模型不再只是「生成」，而是会
// 自主决定调哪个工具去查宝宝的吃饭历史，再据此作答（Reason + Act 循环）。
//
// 它和 cmd/server 共用同一套业务核心 internal/menu，区别只是这里走命令行、那里走 HTTP。
//
// 运行（须在仓库根目录，且 .env 配好 chat + embedding 凭证）：
//
//	go run ./examples/02_menu_agent                         # 用内置示例问题
//	go run ./examples/02_menu_agent 明天晚饭别跟这周重样      # 自带问题
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/cloudwego/eino/schema"

	"tomatoeino/internal/menu"
)

const historyPath = "examples/02_menu_agent/data/history.json"

// inventoryPath 和 cmd/server 共用同一本库存账（运行时数据，已 gitignore）。
const inventoryPath = "data/inventory.json"

func main() {
	ctx := context.Background()

	// 装配 agent：读历史+库存 → 灌向量库 → 建模型+工具 → ReAct agent。
	agent, days, _, err := menu.BuildAgent(ctx, historyPath, inventoryPath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("已加载历史 %d 天，agent 就绪。\n\n", len(days))

	// 命令行参数即问题；没传就用一个示例问题。
	question := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if question == "" {
		question = "看看最近几天吃了啥，帮我安排明天的午餐和晚餐，尽量不重样、荤素搭配。"
	}
	fmt.Printf("【问】%s\n\n【答】", question)

	// 和 example 01 一样的流式读法：Recv 循环直到 io.EOF；StreamReader 必须 Close。
	stream, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage(question)})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(chunk.Content)
	}
	fmt.Println()
}
