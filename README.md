# tomato-platform

统一账号下的多产品 AI 应用平台。当前包含「幼儿备餐」和「English Coach」两个
独立产品，并保留一组用于学习 [CloudWeGo **eino**](https://github.com/cloudwego/eino)
（Go 的 LLM 应用框架）的递进示例。

## 这是 agent，不是 AIGC

后端不是「给输入吐内容」的一次性生成，而是一个会自主决策的 agent：模型先想清楚要什么信息，
再去调工具查宝宝的真实吃饭历史（语义检索 / 看最近几天 / 按食材找），据此作答（Reason + Act 循环）。
它产出的回答本身仍是 AIGC——agent 是「控制结构」维度，AIGC 是「产出」维度，两者不冲突。

> 当前工具都是只读的（检索型），所以是一个偏「轻」的 tool-use agent。
> 路线图：后续接 买菜/超市实时品类、季节时令、家庭库存（含读写），逐步变成完整 agent。

## 结构

```
internal/llm/         连模型的唯一出口：NewChatModel / NewToolCallingChatModel / NewEmbedder
internal/platformdb/  PostgreSQL 连接基础设施（只连库，不自动执行 migration）
internal/vectorstore/ 从零写的内存向量库（cosine 检索），实现 eino 的 retriever.Retriever
internal/menu/        备餐 agent 业务核心：领域类型 + 知识库 + 工具 + ReAct 装配
cmd/account-server/   统一身份与客户平台入口，默认监听 :8460
cmd/server/           HTTP 后端：SSE 流式 /api/chat + REST /api/history + /healthz
internal/english/     英语课程、SQLite 学习账本、进度规则、转写对齐与模型生成
cmd/english-server/   独立 English Coach API / generate-today 命令，默认监听 :8450
english-web/          独立 Next.js 静态前端（今日课程、朗读、历史、趋势、周报、档案）
desktop/              Wails macOS 桌面壳（内嵌 Web UI，安全代理到同一个 English 后端）
examples/02_menu_agent/  同一个 agent 的命令行版 demo
ios/MenuAgent/        SwiftUI App（聊天 tab + 历史 tab），完整 Xcode 工程
migrations/postgres/  PostgreSQL 版本化 migration（生产环境由开发者手动执行）
```

## 统一账号平台（建设中）

平台统一使用 PostgreSQL 保存身份、客户、家庭、产品权限、授权、设备会话和审计数据。
第一版 migration 已放在 `migrations/postgres/`；业务进程不会在启动时自动改表。

首次建库时，由开发者确认目标数据库后手动执行：

```bash
psql "$PLATFORM_DATABASE_URL" -f migrations/postgres/000001_account.up.sql
```

当前 `account-server` 已提供：

- `POST /v1/auth/apple/challenges`：签发一次性 nonce，防止 Apple 凭证重放；
- `POST /v1/auth/apple`：校验 Apple 签名、issuer、audience、有效期和 nonce，并创建统一账户；
- `POST /v1/auth/refresh`：轮换一次性 Refresh Token；
- `GET /v1/me` 与 `POST /v1/auth/logout`：查询统一身份和撤销设备会话；
- `/healthz` 与 `/readyz`：进程和 PostgreSQL 就绪探针。

Refresh Token 只把 SHA-256 摘要写入 PostgreSQL，原文只在签发响应中返回。Apple
authorization code 换取/安全保存 Apple refresh token、账号删除时向 Apple 撤销授权，
以及旧 `API_TOKEN` 用户映射仍属于后续阶段，当前版本还不能视为 App Store 认证闭环。

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

统一账号平台基础进程：

```bash
go run ./cmd/account-server  # 默认 :8460，需要 PLATFORM_DATABASE_URL、ACCOUNT_TOKEN_SECRET、APPLE_CLIENT_IDS
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

### 4. English Coach（独立实例）

English Coach 复用 `.env` 中的 Key 和 Base URL，但通过 `ENGLISH_OPENAI_MODEL`
固定自己的文本模型；SQLite、录音、提示词、前端和端口均不与 menuagent 共用。
朗读上传会通过 `ffprobe` 校验真实时长，并用 `ffmpeg` 转成 16kHz 单声道 WAV，
因此部署主机需要安装 ffmpeg（未安装时录音仍会保存，但本次评测返回降级状态）。

```bash
go run ./cmd/english-server          # 默认 :8450
go run ./cmd/english-server generate-today

cd english-web
npm install                          # 首次安装；随后可使用 npm ci
npm run dev                          # 开发时把 /api 代理到 :8450
```

自动生成由 `deploy/englishcoach-generate.timer` 在工作日 08:00 触发。DNS、证书、
Nginx 和 systemd 文件启用属于服务器变更，需确认后手动执行。

课程内容默认使用每周混合安排：周一新概念英语能力路径、周二 China Daily、
周三 GitHub Engineering / Google Search Central / Cloudflare 等官方技术源、周四
IELTS Academic/General 风格、周五按个人问题词复习。新闻与技术课程只读取标题、
摘要和原文链接，再生成 250～350 词的 CEFR 分级原创短文；不会把第三方文章或
商业教材课文整篇保存进应用。每课会持久化来源、发布日期、能力点和改写说明，
最近 30 个外部 URL 会参与去重；来源暂时不可用时安全降级为同主题原创课程。

### 5. English Coach macOS 桌面端

桌面端复用同一套 Web UI 和同一个远端后端，继续使用现有访问码；不会把模型密钥
或第二份 SQLite 数据库打进应用。默认通过 `https://jelinelin.com/api/english/*`
连接独立 English Coach 进程，本地联调可覆盖：

```bash
ENGLISH_DESKTOP_API_URL=http://127.0.0.1:8450 make desktop-build
open desktop/build/bin/EnglishCoach.app
```

构建阶段需要 npm 和 Wails，最终 `.app` 运行时不需要 Node。Wails 打包会执行框架资源/
绑定生成步骤，因此按本仓库约定由开发者手动运行 `make desktop-build`。

## 其它命令

```bash
go build ./...                  # 全量编译
go vet ./...                    # 静态检查
go test ./...                   # 测试（离线，自动跳过需真实 API 的用例）
```
