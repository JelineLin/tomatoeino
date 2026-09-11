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
internal/platformauth/ 业务服务通过 account-server 实时校验统一会话与产品权限
internal/platformpurge/ 账号删除的跨进程清除链：账户侧发起、产品侧承接（共用一套语义）
internal/observability/ 三个后端共用的 JSON 日志、request ID 与 HTTP 访问日志
internal/vectorstore/ 从零写的内存向量库（cosine 检索），实现 eino 的 retriever.Retriever
internal/menu/        备餐 agent 业务核心：领域类型 + 知识库 + 工具 + ReAct 装配
cmd/account-server/   统一身份与客户平台入口，默认监听 :8460
cmd/server/           HTTP 后端：SSE 流式 /api/chat + REST /api/history + /healthz
cmd/menu-data-migrate/ 把指定旧用户的 Menu JSON 显式迁移到平台 UUID
internal/english/     英语课程、PostgreSQL/SQLite 学习账本、进度规则、转写对齐与模型生成
cmd/english-server/   独立 English Coach API / generate-today 命令，默认监听 :8450
cmd/english-data-migrate/ 把指定旧用户的 English SQLite 数据显式迁移到平台 UUID
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
psql "$PLATFORM_DATABASE_URL" -f migrations/postgres/000002_apple_credentials_and_deletion.up.sql
psql "$PLATFORM_DATABASE_URL" -f migrations/postgres/000003_menu_business_data.up.sql
psql "$PLATFORM_DATABASE_URL" -f migrations/postgres/000004_english_business_data.up.sql
psql "$PLATFORM_DATABASE_URL" -f migrations/postgres/000005_observability_context.up.sql
```

当前 `account-server` 已提供：

- `POST /v1/auth/apple/challenges`：签发一次性 nonce，防止 Apple 凭证重放；
- `POST /v1/auth/apple`：校验身份令牌和 authorization code、加密保存 Apple refresh token，并创建统一账户；
- `POST /v1/auth/refresh`：轮换一次性 Refresh Token；
- `GET /v1/me` 与 `POST /v1/auth/logout`：查询统一身份和撤销设备会话；
- `DELETE /v1/me`：立即冻结账号和全部会话，后台撤销 Apple 授权、清除各产品业务数据，最后硬删除账户数据；
- `/healthz` 与 `/readyz`：进程和 PostgreSQL 就绪探针。

三个 HTTP 进程默认输出结构化 JSON 日志。入口会接受合法的 `X-Request-ID` 或生成新的
128-bit ID，并在响应、Menu/English 到 account-server 的实时鉴权，以及账号删号到产品
清除请求之间持续传递。访问日志记录 `service/request_id/user_id/method/path/status/duration_ms`
等字段，不记录 query string、Authorization、请求体、邮箱或原始 IP。账户安全审计表也保存
同一个 request ID；`000005` 会把删号申请的 ID 固化到后台任务，使 Apple 撤权、产品清除和
最终硬删除在原 HTTP 请求结束后仍能串成一条链。`LOG_FORMAT=text` 可用于本地阅读，
`LOG_LEVEL=debug|info|warn|error` 控制级别。

配置 `ACCOUNT_BASE_URL` 后，Menu Agent 与 English Coach 都接受平台 Access Token：业务请求
实时调用 `GET /v1/me` 校验会话，并分别要求 `menu` / `english` 产品权限。登出、冻结或删除
账号会立即阻断后续业务请求；原有 `users.json` / `API_TOKEN` 暂时保留为旧数据迁移通道。
配置 `MENU_DATABASE_URL` 后，平台用户的餐次、库存和宝宝档案分别存入 `menu.meals`、
`menu.inventory_items` 和 `menu.profiles`；每日简报与启动预热从 PostgreSQL 恢复用户名册。
这个连接必须指向包含 `account.users` 的同一个 PostgreSQL database。未配置时仍兼容
`DATA_DIR/users/<uuid>/`，方便本地开发和分阶段切换。

删除账号会连业务数据一起清干净：`DELETE /v1/me` 先冻结账号、撤销全部会话，后台任务
逐一撤销 Apple 授权，再按 `MENU_BASE_URL` / `ENGLISH_BASE_URL` 调各产品的
`DELETE /internal/v1/users/<uuid>`（认 `PLATFORM_INTERNAL_TOKEN` 共享密钥，不认用户会话），
**全部清除成功之后**才硬删账户数据——顺序反了的话，`account.users` 一没，业务数据就成了
没人认领的孤儿。任何一步失败都整单退避重试，各产品的清除接口因此都是幂等的：
Menu 事务删除 PostgreSQL 数据（兼容清理旧目录）并给该租户立墓碑，在删除前锁住并停用
三类 store，确保飞行中的旧请求不能把数据写回来；
English 在一个事务里删掉九张表的记录并连录音文件一起清除。`/internal/` 只该走本机环回，
反向代理要挡掉（见 `deploy/nginx-english.conf`）。

已有 Menu JSON 必须在确认“旧目录属于哪个平台用户”后手动迁移。工具不会覆盖已有数据库
数据，也不会删除源文件。例如把旧 `home` 数据归到某个已存在的统一账户：

```bash
go run ./cmd/menu-data-migrate \
  --source-user home \
  --user-id c733a5d7-7b65-49ac-b6d2-872fd57a4ce6
```

切换顺序是：手动应用 `000003` → 逐户运行迁移工具并核对 → 配置 `MENU_DATABASE_URL`
并重启 Menu 服务。不要先启用数据库再迁移，否则旧 UUID 目录会暂时显示成空账户。

English Coach 的生产数据使用同一 database 下独立的 `english` schema，九张表覆盖学习档案、
课程、阅读/口语尝试、弱项、周报、计划版本和定时任务。旧 token 阶段的 `home` 等标识仍可
短期共存，所以 `english.user_id` 保留为文本；规范 UUID 会同时生成外键关联 `account.users`，
在产品清除接口之外提供最终级联兜底。仍不能绕开清除链直接删账号，因为录音文件也要由
English 服务删除。迁移旧 SQLite 时先停止 English Web 与定时生成任务，
再逐户执行（工具只读源库、不会覆盖目标已有数据、不会删除源文件）：

```bash
go run ./cmd/english-data-migrate \
  --source-db data/english/learning.db \
  --source-user home \
  --user-id c733a5d7-7b65-49ac-b6d2-872fd57a4ce6
```

切换顺序是：手动应用 `000004` → 停止 English 写入 → 逐户迁移并核对 → 配置
`ENGLISH_DATABASE_URL` → 重启 Web 与定时任务并检查 `/readyz`。课程和口语记录会在目标库
重建主键，并同步重映射阅读记录、口语记录和单词结果的外键，避免与目标库已有序列冲突。

平台 Refresh Token 只把 SHA-256 摘要写入 PostgreSQL，原文只在签发响应中返回。Apple
refresh token 使用 AES-256-GCM 加密，并绑定 Apple subject 与 Client ID；账号删除任务采用
数据库租约和退避重试。旧 `API_TOKEN` 用户到平台账户的映射迁移、客户端登录界面
和 account-server 本身的部署仍属于后续阶段，因此当前版本还不能上架。

若两个 App 要自然识别为同一 Apple 用户，需要在 Apple Developer 后台将两个 App ID 配置到
同一个 Sign in with Apple primary app / app group；不能用邮箱（包括私密转发邮箱）猜测合并账户。

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
go run ./cmd/account-server  # 默认 :8460；所需变量见 .env.example 的 Tomato Platform 区域
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
固定自己的文本模型；业务表位于独立的 `english` schema，录音、提示词、前端和端口均不与
Menu Agent 共用。本地开发仍可不配 `ENGLISH_DATABASE_URL`，回退到 SQLite。
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
