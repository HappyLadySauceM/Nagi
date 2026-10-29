# Nagi

接入 [SnowLuma](https://github.com/SnowLuma/SnowLuma)（QQ → OneBot v11 桥接运行时）的 Go 智能 QQ 助手。
硬性规则决定能否回复，Jev 语义模型判断是否该参与，LLM 负责生成，PostgreSQL 负责会话与记忆。

- 协议层：OneBot v11 正向 WebSocket，38 个标准动作，CQ 码/段数组双形态
- 账号层：多个 SnowLuma 端点并行连接，启动后用 `get_login_info` 自动识别 QQ 号并按账号隔离数据
- 业务层：目录同步、作用域授权（默认全关）、会话持久化、有界工作器、意图决策、Eino Agent 回复与工具调用、长期记忆
- 运维层：管理 HTTP API、Bearer 鉴权与审计、聚合指标、可选 Langfuse 追踪

架构细节与分层约定见 [`docs/architecture.md`](docs/architecture.md)，
优化路线见 `docs/Nagi_Implementation_Plan_v2.0.md`。

## 前置条件

| 依赖 | 说明 |
| --- | --- |
| Go 1.26+ | `go.mod` 声明最低 1.26.0，本地可用更高补丁版（当前用 1.27.2 构建） |
| SnowLuma | 已登录目标 QQ 账号，并在 WebUI 中配置 OneBot v11 **正向 WS 服务端** |
| Docker + Compose | `docker compose up -d` 启动 PostgreSQL 16(pgvector) 与 Langfuse 全栈 |
| PostgreSQL 16（可选） | 未配置 DSN 时机器人仍可运行，但只有 echo 与状态接口 |

## 快速开始

```bash
# 1. 启动基础设施：PostgreSQL(pgvector) + Langfuse 全栈
#    首次拉取较慢；db-init 会幂等创建 langfuse 与 nagi_test 库
docker compose up -d

# 2. 生成本地配置（已 gitignore）
just init-config

# 3. 填写 configs/config.yaml（所有值都直接写在这个文件里）
#    onebot.endpoints[]：每项一个 SnowLuma OneBot 实例，含 access-token；账号身份启动后自动识别
#    database.dsn：postgres://nagi:nagi-local@127.0.0.1:5433/nagi?sslmode=disable
#    ai.api-key：LLM 密钥（ai.enabled=true 时必填）
#    intent.api-key：Jev 密钥（留空则降级为仅响应 @/引用）
#    observability.{base-url,public-key,secret-key}：Langfuse 地址与项目凭据
#    server.admin-token：本机可留空；非回环监听必须填写
#    configs/config.yaml 已被 gitignore，明文密钥不会入库

# 4. 启动；进程启动时自动应用数据库迁移
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
| GET | `/readyz` | 任一账号已连接返回 200，否则 503 |
| GET | `/api/v1/status` | 版本、运行时长、处理器列表与事件计数 |
| GET | `/api/v1/accounts` `/api/v1/accounts/{bot_id}` | 全部账号及连接状态 |
| POST | `/api/v1/accounts/{bot_id}/sync` | 同步该账号的好友/群（要求已连接） |
| GET | `/api/v1/contacts?bot_id=` `/api/v1/groups?bot_id=` | 账号级目录查询 |
| GET | `/api/v1/groups/{id}/members?bot_id=&refresh=true` | 群成员，可强制刷新 |
| GET/PUT | `/api/v1/scopes` `/api/v1/scopes/{type}/{id}` | 作用域策略 |
| GET/POST/PATCH/DELETE | `/api/v1/memories` `/api/v1/memories/{id}` | 长期记忆 |
| GET | `/api/v1/metrics` | 消息、任务、决策、记忆聚合计数 |
| GET | `/api/v1/tools` | 已注册工具、权限项与默认授予状态 |
| GET | `/api/v1/intent/decisions(/{id})` | 决策记录与可解释步骤 |
| GET/POST/PATCH | `/api/v1/intent/rulesets(/{id})` | 规则版本、草稿与发布 |
| POST | `/api/v1/intent/rulesets/{id}/publish` `/rollback` | 发布与回滚规则版本 |
| POST | `/api/v1/intent/simulate` | 只读模拟：不写库、不发送 |
| GET | `/api/v1/agent/outbound?job_id=` | 单次运行的投递记录 |
| GET/PUT | `/api/v1/agent/sending-policy` | 按作用域的发送开关与预算 |

`GET /api/v1/tools` 支持可选参数 `scope_kind=private|group`，按该会话类型返回每个工具的
`Category`、`ScopeKinds`、`RiskLevel`、`Applicable` 与 `UnavailableReason`；不带参数时
不做过滤，所有工具都按适用返回。

## 打开一个作用域的对话

作用域默认全部关闭（`enabled=false`），必须先显式开启。多账号时每个请求都要带 `bot_id`；
只配置一个账号时可省略，后端会使用唯一账号。账号身份未知时写入返回 409。

```bash
TOKEN=...   # server.admin-token，未配置则省略该 header
BOT=127.0.0.1:5080

# 查看已识别账号
curl -s http://$BOT/api/v1/accounts -H "Authorization: Bearer $TOKEN"

# 同步某个账号的目录
curl -s -X POST http://$BOT/api/v1/accounts/10001/sync -H "Authorization: Bearer $TOKEN"

# 开启某个群：仅响应 @ 与引用，持久化消息并允许读取历史
curl -s -X PUT "http://$BOT/api/v1/scopes/group/123456789?bot_id=10001" \
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
- 多账号用 `onebot.endpoints[]`（`url`、`access-token`、可选 `expect-qq`）。
  `expect-qq` 不一致时该端点会被拒绝并记录原因，事件不会进入业务流水线。
- 全部地址、凭据与密钥都直接写在 `configs/config.yaml`（已 gitignore）；提交仓库的
  `configs/config-example.yaml` 只含占位符，不要把真实密钥写进示例文件。
- 通用运行时覆盖（可选）：`NAGI_SERVER_LISTEN`、`NAGI_LOG_LEVEL`，以及 `--listen`、`--log-level` Flags。
- 无 `configs/config.yaml` 时以默认值 + 上述覆盖启动（默认端点 `ws://127.0.0.1:3001`、无数据库）。

## MVP 验收清单

1. `curl -s localhost:5080/livez` → 200；`/readyz` → 200。
2. `/api/v1/accounts` 中每个端点都有 `bot_id`，且 `connected=true`。
3. `POST /api/v1/accounts/{bot_id}/sync` 后 `/api/v1/contacts?bot_id=`、`/api/v1/groups?bot_id=` 有数据。
4. 对目标群 PUT 作用域后，在群里 @机器人 发一条消息。
5. 日志依次出现 `intent decided`（`decision=reply`、`reason=explicit_mention`）、`nagi.agent.reply`、
   `nagi.onebot.send`，QQ 侧收到回复。
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
| 消息不回复 | 依次检查：作用域是否 `enabled` + `store_messages`、`reply_mode` 是否匹配（群聊非 @ 需 `intent_based`）、`ai.api-key` 是否填写、`message_jobs` 是否停在 `pending` |
| 日志里连 `ai ingress` 都没有 | 多半是作用域被重置为默认关闭（例如跑过集成测试清空了表）：`GET /api/v1/scopes/group/{id}` 确认 `enabled`，必要时重新 PUT |
| 启动报 `ai.enabled 必须同时设置 intent.enabled=true` | 两者需同时开启 |
| `GET /groups/{id}/members?refresh=true` 返回 409 | 该账号还没同步过目录，先 `POST /api/v1/accounts/{bot_id}/sync` 再重试 |
| 账号列表出现 `rejected` | 端点返回的 QQ 号与 `expect-qq` 不一致，检查 SnowLuma 中登录的账号或修正配置 |
| 多账号下接口返回 400 `bot_id is required` | 控制台会发送 `bot_id`；手工调用时需显式带上当前账号 |
| 开启 AI 后仍收到 echo 复读 | 关闭 `echo.enabled` |
| 数据库未配置 | 依赖 DB 的路由不会注册，`ai_ingress` 也不生效，机器人退化为 echo 模式 |

## Langfuse 追踪

`docker compose up -d` 会在 <http://127.0.0.1:3010> 拉起 Langfuse，并自动创建组织、项目与 API Key
（默认 `pk-lf-nagi-local` / `sk-lf-nagi-local`；登录账号 `admin@nagi.local`，密码
`nagi-local-2026`，可用 `LANGFUSE_INIT_*` 覆盖）。开启导出只需在 `configs/config.yaml` 填写：

```yaml
observability:
  enabled: true
  base-url: "http://127.0.0.1:3010"
  public-key: "pk-lf-nagi-local"
  secret-key: "sk-lf-nagi-local"
  ingestion-version: "4"
  environment: "local"        # deployment.environment 资源属性，留空不写
  export-raw-content: true    # 默认导出 prompt、回复与工具结果；false 时正文打码
  user-id-secret: ""          # openssl rand -hex 32；至少 32 字符，留空则无用户维度
  raw-identifiers: false      # true 时直接导出原始 QQ 号，仅建议本机调试
```

`export-raw-content=true`（默认）时消息正文与工具参数/结果都会进入 Langfuse；设为 false 时 span 结构、
耗时与 token 用量照常导出，正文替换为 `{"redacted":true}`，详情页不会出现空白。
用户维度二选一：`user-id-secret` 生成稳定 HMAC 假名（占位符或少于 32 字符会被拒绝，启动日志给出原因并关闭该维度）；
`raw-identifiers=true` 则直接写入发送者 QQ、机器人 QQ 与群号/私聊对象，启动时会额外告警，需自行限制 Langfuse 访问。
凭据缺失或导出失败只影响追踪，不影响聊天链路。

一条消息的每个业务节点都会写入 `langfuse.observation.input/output`：入站判定、作用域结果、规则结论、
Jev 分数、最终决策与原因、Agent 提示与回复、输出净化、发送状态、任务终态，因此详情页可以直接阅读，
不需要展开 Attributes。Eino 内部 `Init`/`Lambda`/`ReAct` 包装已收敛，trace 保持 `agent → 模型/工具` 的结构。

## Agent 运行时与工具

回复生成由 Eino ADK 的 `ChatModelAgent` + `Runner` 完成，业务侧仍由 `ReplyExecutor` 负责作用域复核、
出站净化、发送与状态回写。相关配置：

```yaml
agent:
  max-steps: 6            # ReAct 循环上限（含工具轮次）
  max-tool-calls: 5       # 单次运行的累计工具调用上限
  max-run-duration: 30s   # 单次运行总超时（含模型与工具）

tools:
  enabled: [builtin.time, builtin.qq]   # 为空时不暴露任何工具；写错 ID 启动失败
  permission-defaults: {} # 按权限覆盖默认基线（未列出的只读允许、其余拒绝）
  allow-side-effects: false  # true 才可能暴露非只读工具
  timeout: 5s             # 单次工具调用超时
  max-result-bytes: 8192  # 结果过大即拒绝
```

内置工具分三类：只读的 `system.current_time`、`qq.get_group_info`（`qq.group.read`）与
`qq.get_group_member_info`（`qq.group.member.read`）；回合内发送 `qq.send_current`
（`qq.reply.current`，需 `tools.allow-send-current`）；群管理的禁言/名片/移出
（`qq.group.admin.mute`、`qq.group.admin.card`、`qq.group.admin.kick`）。只读工具直接可用，
其余默认不暴露。模型无法传入任意群号或跨会话目标，工具调用在服务端做权限、账号与目标绑定校验，
结果按不可信数据处理；Eino 的 Agent/Model/Tool span 通过共享 TracerProvider 进入同一条 Langfuse trace。

### 工具权限模型

实际可用的工具由四层共同决定：

1. **进程层**：`tools.enabled` 决定哪些提供方被载入，未启用的提供方不注册、也不出现在清单中。
2. **会话类型层**：描述符的 `ScopeKinds` 限定工具适用的会话类型；`qq.get_group_info` 仅群聊可用，
   私聊运行的工具列表中不会出现它。省略 `ScopeKinds` 表示所有会话类型可用。
3. **默认基线**：`tools.permission-defaults` 覆盖描述符兜底；未配置时只读工具默认允许，写操作/群管理默认拒绝。
   非只读工具还需要 `allow-side-effects: true` 才会暴露。
4. **作用域层**：`scope_policies.tool_permissions` 保存每个群/私聊的显式覆盖（如 `{"qq.group.read": false}`），
   优先级最高；被撤销的工具不会出现在模型工具列表中，执行前 Guard 还会再校验一次。

`GET /api/v1/tools` 返回真正注册成功的工具与权限项（含默认授予状态），控制台的作用域弹窗据此渲染
「能力授权」区块；提交未知权限会被拒绝，避免存入无效策略。

## 本地开发环境

`scripts/dev-env.sh` 封装本地 compose 的初始化、状态与清理（也可用 `just` 调用）：

| 命令 | 作用 |
| --- | --- |
| `just dev-init` | `docker compose up -d` 并等待 PostgreSQL 就绪；`configs/config.yaml` 缺失时从示例生成，写入随机 `user-id-secret`、`environment: local`，并开启正文与原始 QQ 导出（仅限本地） |
| `just run` | 后台启动后端与前端（compose 未启动时先拉起）；PID 与日志写入 `.run/` |
| `just exit` | 停止 `just run` 启动的后端与前端（也可用 `scripts/dev-env.sh stop`） |
| `just dev-status` | 查看业务表行数、任务状态、ClickHouse 追踪计数与运行中的进程 |
| `just dev-clean` | 清理开发数据；默认 dry-run，只打印计划 |

`just dev-clean --yes` 会清空 `messages`、`message_jobs`、`conversations`、`intent_decisions`、`memories`、
`admin_audit_logs`，以及 ClickHouse 的 `events_core/events_full/observations*/traces/scores/analytics_*`
和 Redis 中的 OTel ingestion 队列；默认保留 `scope_policies`、`bot_accounts`、目录缓存、`schema_migrations`
与 Langfuse 的登录/API Key。`--with-scopes` 额外清空作用域与目录缓存（回到默认拒绝）；
`--reset-volumes` 执行 `docker compose down -v`，连 Langfuse 登录与 API Key 一起重建。
加 `--force` 时先停止后端与前端，清理完成后自动重新启动；不加时若服务仍在运行则拒绝并提示先 `just exit`。
前端只管理 `web/` 的 `pnpm dev`（默认 127.0.0.1:5173），不安装依赖、不构建产物。

## 验证

```bash
just verify   # go mod tidy + vet + test + fmt

# 数据库集成测试必须指向独立测试库（compose 已创建 nagi_test）
NAGI_TEST_DSN="postgres://nagi:nagi-local@127.0.0.1:5433/nagi_test?sslmode=disable" go test ./...
```

> 迁移往返测试会 `drop` 所有表。绝不把 `NAGI_TEST_DSN` 指向 `nagi` 库——跑一次就会清空会话、
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

## 构建

- 基础镜像默认走集群内 Zot（`zot.infra-registry.svc.cluster.local:5000/infrastructure/{alpine,nginx,node}`），
  由 `gitops-infra` 的 `sync-base-images` 工作流同步；本地构建可用
  `docker build --build-arg BASE_IMAGE=alpine:3.22 .` 覆盖。
- CI 里前端依赖走 `registry.npmmirror.com`，Go 模块与 pnpm store 用 `actions/cache` 复用，
  cosign 二进制同样缓存；artifacts 只保留 1 天。
- 镜像 tag 是 7 位短 SHA，由 gitops 仓库的 Image Updater 自动开 PR；本仓库不再推送 digest 事件。
- 旧镜像由 Zot 保留策略（每仓库最近 10 个）与 ghcr 保留策略（每包最近 10 个）清理；
  ghcr 侧是 `.github/workflows/ghcr-retention.yml`（显示名
  `chore(cleanup): Update from GHCR cleanup automation`），只调 GitHub API 删除、不产生提交。
