# QQBot

接入 [SnowLuma](https://github.com/SnowLuma/SnowLuma)（QQ → OneBot v11 桥接运行时）的 Go 智能 QQ 助手。
硬性规则决定能否回复，Jev 语义模型判断是否该参与，LLM 负责生成，PostgreSQL 负责会话与记忆。

- 协议层：OneBot v11 正向 WebSocket，38 个标准动作，CQ 码/段数组双形态
- 业务层：目录同步、作用域授权（默认全关）、会话持久化、有界工作器、意图决策、LLM 回复、长期记忆
- 运维层：管理 HTTP API、Bearer 鉴权与审计、聚合指标、可选 Langfuse 追踪

架构细节与分层约定见 [`docs/architecture.md`](docs/architecture.md)，阶段任务与缺口见
[`docs/QQBot_Implementation_Plan_v1.0.md`](docs/QQBot_Implementation_Plan_v1.0.md)。

## 前置条件

| 依赖 | 说明 |
| --- | --- |
| Go 1.22+ | `go.mod` 声明 1.22 |
| SnowLuma | 已登录目标 QQ 账号，并在 WebUI 中配置 OneBot v11 **正向 WS 服务端** |
| Docker + Compose | `docker compose up -d` 启动 PostgreSQL 16(pgvector) 与 Langfuse 全栈 |
| PostgreSQL 16（可选） | 未配置 DSN 时机器人仍可运行，但只有 echo 与状态接口 |

## 快速开始

```bash
# 1. 启动基础设施：PostgreSQL(pgvector) + Langfuse 全栈
#    首次拉取较慢；db-init 会幂等创建 langfuse 与 qqbot_test 库
docker compose up -d

# 2. 生成本地配置（已 gitignore）
just init-config

# 3. 填写 configs/config.yaml
#    onebot.access-token：SnowLuma WebUI「网络配置」里 WS 服务端的 access token
#    database.dsn：postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot?sslmode=disable
#    server.admin-token：本机可留空；非回环监听必须填写

# 4. 注入密钥（不落盘）
export QQBOT_LLM_API_KEY=sk-...
export TYPESAFE_API_KEY=...   # 可选，仅 intent_based 群聊插话需要

# 5. 启动；进程启动时自动应用数据库迁移
just run
```

`configs/config.yaml` 缺失时可用 `configs/config-example.yaml` 作为模板；`just run` 会自动执行
`init-config` 再启动。

### 获取 OneBot access token

SnowLuma 的 WS 服务端强制校验 token，不带或填错会被拒绝握手（日志为 `websocket: bad handshake`
或 `401 Unauthorized`）。两种获取方式：

- WebUI（默认 <http://127.0.0.1:5099>）→ 网络 → WebSocket 服务端 → Access Token
- 容器内配置文件：`/app/data/config/onebot_*.json` 的 `wsServers[].accessToken`

## 管理 API

默认监听 `127.0.0.1:5080`，响应统一为 `{code,message,data}`。配置 `server.admin-token` 后，
除 `/livez` `/readyz` 外的接口都需要 `Authorization: Bearer <token>`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/livez` | 进程存活 |
| GET | `/readyz` | OneBot 已连接返回 200，否则 503 |
| GET | `/api/v1/status` | 版本、连接状态、self_id、处理器列表 |
| POST | `/api/v1/directory/sync` | 同步好友/群（要求已连接） |
| GET | `/api/v1/accounts` `/api/v1/contacts` `/api/v1/groups` | 账号与目录查询 |
| GET | `/api/v1/groups/{id}/members?refresh=true` | 群成员，可强制刷新 |
| GET/PUT | `/api/v1/scopes` `/api/v1/scopes/{type}/{id}` | 作用域策略 |
| GET/POST/PATCH/DELETE | `/api/v1/memories` `/api/v1/memories/{id}` | 长期记忆 |
| GET | `/api/v1/metrics` | 消息、任务、决策、记忆聚合计数 |

## 打开一个作用域的对话

作用域默认全部关闭（`enabled=false`），必须先显式开启。写入前要求 self_id 已知，否则返回 409。

```bash
TOKEN=...   # server.admin-token，未配置则省略该 header
BOT=127.0.0.1:5080

# 同步目录
curl -s -X POST http://$BOT/api/v1/directory/sync -H "Authorization: Bearer $TOKEN"

# 开启某个群：仅响应 @ 与引用，持久化消息并允许读取历史
curl -s -X PUT http://$BOT/api/v1/scopes/group/123456789 \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"reply_mode":"mention_only","store_messages":true,
       "allow_history_read":true,"allow_memory_write":true,
       "min_reply_interval_ms":10000,"max_replies_per_hour":20,"retention_days":30}'
```

`reply_mode` 可选 `mention_only`（只回 @/引用，最可控）、`intent_based`（Jev 判定是否插话）、
`always_reply`、`observe_only`（只记录不回复）。

## 配置要点

- `ai.enabled=true` 时必须同时 `intent.enabled=true`：回复由意图决策流水线触发，只开生成不会
  发出任何消息，启动阶段会直接报错。
- `echo.enabled` 与 `ai.enabled` 同时为 true 时，同一会话可能被回两次；启动日志会用 WARN 提示。
- 密钥只经环境变量注入：`QQBOT_LLM_API_KEY`、`TYPESAFE_API_KEY`、`LANGFUSE_*`、`QQBOT_DATABASE_DSN`。
- 其余环境变量：`QQBOT_ONEBOT_URL`、`QQBOT_ONEBOT_ACCESS_TOKEN`、`QQBOT_SERVER_LISTEN`、
  `QQBOT_LOG_LEVEL`、`QQBOT_SERVER_ADMIN_TOKEN`。
- 无 `configs/config.yaml` 时以默认值 + 环境变量 + Flags 启动。

## MVP 验收清单

1. `curl -s localhost:5080/livez` → 200；`/readyz` → 200。
2. `/api/v1/status` 中 `onebot.connected=true` 且 `self_id` 非零。
3. `POST /api/v1/directory/sync` 后 `/api/v1/contacts`、`/api/v1/groups` 有数据。
4. 对目标群 PUT 作用域后，在群里 @机器人 发一条消息。
5. 日志依次出现 `intent decided`（`decision=reply`、`reason=explicit_mention`）、`qqbot.agent.reply`、
   `qqbot.onebot.send`，QQ 侧收到回复。
6. 数据库核验：

   ```sql
   select direction, sent_status, left(text_content, 30) from messages order by id desc limit 4;
   select status, last_error_code from message_jobs order by id desc limit 2;
   select decision, reason_code, provider, model from intent_decisions order by job_id desc limit 1;
   ```

7. `/api/v1/metrics` 中 `messages_24h`、`message_jobs.done`、`intent_decisions_24h` 相应增长。

## 常见问题

| 现象 | 原因与处理 |
| --- | --- |
| 日志 `onebot: connect failed: websocket: bad handshake` | access-token 缺失或与 SnowLuma 不一致 |
| `/readyz` 返回 503 | WS 未连上；先确认 SnowLuma 中 QQ 账号已登录、WS 服务端已启用 |
| 消息不回复 | 依次检查：作用域是否 `enabled` + `store_messages`、`reply_mode` 是否匹配（群聊非 @ 需 `intent_based`）、`QQBOT_LLM_API_KEY` 是否存在、`message_jobs` 是否停在 `pending` |
| 日志里连 `ai ingress` 都没有 | 多半是作用域被重置为默认关闭（例如跑过集成测试清空了表）：`GET /api/v1/scopes/group/{id}` 确认 `enabled`，必要时重新 PUT |
| 启动报 `ai.enabled 必须同时设置 intent.enabled=true` | 两者需同时开启 |
| `GET /groups/{id}/members?refresh=true` 返回 500 | 进程重启后内存中的"最近同步账号"会丢失，先 `POST /api/v1/directory/sync` 再重试 |
| 开启 AI 后仍收到 echo 复读 | 关闭 `echo.enabled` |
| 数据库未配置 | 依赖 DB 的路由不会注册，`ai_ingress` 也不生效，机器人退化为 echo 模式 |

## Langfuse 追踪

`docker compose up -d` 会在 <http://127.0.0.1:3010> 拉起 Langfuse，并自动创建组织、项目与 API Key
（默认 `pk-lf-qqbot-local` / `sk-lf-qqbot-local`，可用 `LANGFUSE_INIT_*` 覆盖）。开启导出：

```bash
export LANGFUSE_BASE_URL=http://127.0.0.1:3010
export LANGFUSE_PUBLIC_KEY=pk-lf-qqbot-local
export LANGFUSE_SECRET_KEY=sk-lf-qqbot-local
```

并把 `configs/config.yaml` 的 `observability.enabled` 设为 `true`。凭据缺失或导出失败只影响追踪，
不影响聊天链路。

## 验证

```bash
just verify   # go mod tidy + vet + test + fmt

# 数据库集成测试必须指向独立测试库（compose 已创建 qqbot_test）
QQBOT_TEST_DSN="postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot_test?sslmode=disable" go test ./...
```

> 迁移往返测试会 `drop` 所有表。绝不把 `QQBOT_TEST_DSN` 指向 `qqbot` 库——跑一次就会清空会话、
> 消息、作用域与记忆数据。

并发相关改动建议追加 `go test -race ./...`。

## 目录结构

```
cmd/            进程入口、生命周期、组合根（assembly）、HTTP 路由
internal/       各业务上下文（onebot / bot / directory / scope / conversation / intent / agent / memory / observability）
pkg/            通用技术组件（config / db / logging / version）
migrations/     版本化 SQL（启动时自动应用）
configs/        config-example.yaml（config.yaml 为本地文件，已 gitignore）
```
