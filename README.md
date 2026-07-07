# tomatoeino

学习 [CloudWeGo **eino**](https://github.com/cloudwego/eino)（Go 的 LLM 应用框架）的练手仓库。
`examples/` 下是一组逐个递进的小例子；在此之上，把「幼儿备餐」做成了一个完整的
**ReAct agent 后端 + SwiftUI iOS 前端**。

## 这是 agent，不是 AIGC

后端不是「给输入吐内容」的一次性生成，而是一个会自主决策的 agent：模型先想清楚要什么信息，
再去调工具查宝宝的真实吃饭历史（语义检索 / 看最近几天 / 按食材找），据此作答（Reason + Act 循环）。
它产出的回答本身仍是 AIGC——agent 是「控制结构」维度，AIGC 是「产出」维度，两者不冲突。

> 当前工具都是只读的（检索型），所以是一个偏「轻」的 tool-use agent。
> 路线图：后续接 买菜/超市实时品类、季节时令、家庭库存（含读写），逐步变成完整 agent。

## 结构

```
internal/llm/         连模型的唯一出口：NewChatModel / NewToolCallingChatModel / NewEmbedder
internal/vectorstore/ 从零写的内存向量库（cosine 检索），实现 eino 的 retriever.Retriever
internal/menu/        备餐 agent 业务核心：领域类型 + 知识库 + 工具 + ReAct 装配
cmd/server/           HTTP 后端：SSE 流式 /api/chat + REST /api/history + /healthz
examples/02_menu_agent/  同一个 agent 的命令行版 demo
ios/MenuAgent/        SwiftUI App（聊天 tab + 历史 tab），完整 Xcode 工程
```

## 跑起来

### 1. 配置

```bash
cp .env.example .env   # 填入 OPENAI_API_KEY 等（chat + embedding 凭证，见 CLAUDE.md）
```

后端启动时会把整段历史一次性向量化灌进内存库，所以**必须配好可用的 embedding 凭证**，否则启动即报错。

### 2. 后端（须在仓库根目录跑，才能找到 .env 和默认历史路径）

```bash
go run ./cmd/server          # 默认监听 :8080，可用 PORT 覆盖
```

冒烟自测：

```bash
curl localhost:8080/healthz                                   # ok
curl localhost:8080/api/history                               # 历史菜单 JSON
curl -N -X POST localhost:8080/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"明天晚饭别跟这周重样"}]}'   # SSE 流式 token
```

不想开服务，也可以命令行直接体验 agent：

```bash
go run ./examples/02_menu_agent 看看最近吃了啥，帮我安排明天的午餐和晚餐
```

### 3. iOS App

先让后端在本机 `:8080` 跑着，然后：

```bash
open ios/MenuAgent/MenuAgent.xcodeproj
```

在 Xcode 里选一个 iOS 模拟器，Run。模拟器的 `localhost` 直连 Mac 本机后端
（已在 Info.plist 用 `NSAllowsLocalNetworking` 放行 http）。真机调试时把
`ios/MenuAgent/MenuAgent/APIClient.swift` 里的 `baseURL` 改成 Mac 的局域网 IP。

## 其它命令

```bash
go build ./...                  # 全量编译
go vet ./...                    # 静态检查
go test ./...                   # 测试（离线，自动跳过需真实 API 的用例）
```
