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
| Go 1.26+ | `go.mod` 声明最低 1.26.0，本地可用更高补丁版（当前用 1.27.x 构建） |
| SnowLuma | 已登录目标 QQ 账号，并在 WebUI 中配置 OneBot v11 **正向 WS 服务端** |
| Docker + Compose | `docker compose up -d` 启动 PostgreSQL 17 业务库、保留的 PostgreSQL 16 与 Langfuse 全栈 |
| PostgreSQL 17（业务库，可选） | 未配置 DSN 时机器人仍可运行，但只有 echo 与状态接口 |

## 快速开始

```bash
# 1. 启动基础设施：PostgreSQL(pgvector) + Langfuse 全栈
#    首次拉取较慢；db-init 创建 langfuse，db17-init 创建隔离的 nagi_ent_test 库
docker compose up -d

# 2. 生成本地配置（已 gitignore）
just init-config

# 3. 填写 configs/config.yaml（所有值都直接写在这个文件里）
#    onebot.endpoints[]：每项一个 SnowLuma OneBot 实例，含 access-token；账号身份启动后自动识别
#    database.dsn：postgres://nagi:nagi-local@127.0.0.1:5434/nagi_ent?sslmode=disable
#    ai.api-key：LLM 密钥（ai.enabled=true 时必填）
#    intent.api-key：Jev 密钥（留空则降级为仅响应 @/引用）
#    observability.{base-url,public-key,secret-key}：Langfuse 地址与项目凭据
#    server.admin-token：本机可留空；非回环监听必须填写
#    configs/config.yaml 已被 gitignore，明文密钥不会入库

# 4. 先按下文“数据库迁移”初始化新库，再启动；应用只校验版本
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
| GET/POST/PATCH/DELETE | `/api/v1/intent/rulesets(/{id})` | 规则版本、草稿、发布与删除（删除后回到全局或内置规则，版本号不复用） |
| POST | `/api/v1/intent/rulesets/{id}/publish` `/rollback` | 发布与回滚规则版本 |
| POST | `/api/v1/intent/simulate` | 只读模拟：不写库、不发送 |
| GET | `/api/v1/agent/outbound?job_id=` | 单次运行的投递记录 |
| GET/PUT | `/api/v1/agent/sending-policy` | 按作用域的发送授权与预算 |
| GET/PUT | `/api/v1/settings` | 读取/保存系统配置（乐观锁，冲突返回 409） |
| POST | `/api/v1/settings/reset` | 恢复配置文件基线 |

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

# 开启某个群：仅响应 @ 与引用，允许读取历史
# 消息存储是产品基线，无需也不能在界面关闭；0 表示不限。
curl -s -X PUT "http://$BOT/api/v1/scopes/group/123456789?bot_id=10001" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"reply_mode":"mention_only",
       "allow_history_read":true,"allow_memory_write":true,
       "min_reply_interval_ms":10000,"max_replies_per_hour":0,
       "max_messages_per_run":0,"max_chars_per_message":4500,"max_total_chars_per_run":0}'
```

`reply_mode` 可选 `mention_only`（只回 @/引用，最可控）、`intent_based`（Jev 判定是否插话）、
`always_reply`、`observe_only`（只记录不回复）。消息留存改为 `messages.retention-days`
（系统配置 → 消息与留存），不再按会话配置。

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
| 消息不回复 | 依次检查：作用域是否 `enabled`、`reply_mode` 是否匹配（群聊非 @ 需 `intent_based`）、是否获得 `qq.reply.current`（默认授予，可能被按会话撤销）、`ai.api-key` 是否填写、`message_jobs` 是否停在 `pending` |
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
  sending:                # 出站预算，0 表示不限
    max-messages-per-run: 0
    max-chars-per-message: 4500   # QQ 单条文本上限
    max-total-chars-per-run: 0

messages:
  retention-days: 30      # 全局消息留存，0 表示不清理

tools:
  permission-defaults:    # 首次启动写入数据库的初始权限基线
    qq.reply.current: true
  timeout: 5s             # 首次启动写入数据库的初始调用超时
  max-result-bytes: 8192  # 首次启动写入数据库的初始结果上限
```

工具提供方没有启用列表：启动时注册多少就全部可用，模型能否调用某个工具只取决于
该会话的「能力授权」。

`ai`、`intent`、`agent`、`messages` 与 `tools` 这几个分区在首次启动时写入数据库，此后由控制台的
「系统配置」页面维护，保存即生效、无需重启；`tools.permission-defaults` 作为内部基线不再由界面编辑，
`onebot`、`server`、`database`、`observability` 的基础设施凭证仍由配置文件或环境变量维护。

模型在「系统配置 → 模型配置」中逐项打开右侧抽屉，管理生成、意图、语音识别和语音合成模型，
并选择各用途的全局默认值。OpenAI 兼容配置填写基础地址（例如 `https://example.com/v1`），
SystemOne 配置填写完整判定地址。Token 留空保留，填入新值则替换；所有读取接口只显示凭证状态。
提交按钮为“测试保存模型”，真实调用成功后才持久化，可能产生少量模型费用；旧模型需要重新测试。
高级配置支持多组自定义 HTTP 请求头，值加密保存且不回显。
已有 `ai.api-key`、`intent.api-key` 和模型连接配置仅在模型目录初始化时导入一次，之后以数据库为准。
「恢复默认」保留模型目录、会话选模与凭证，不会重新导入已清除的 Token。

Token 与 B 站 Cookie 使用 AES-GCM 加密，密钥首次使用自动保存在系统配置目录的
`nagi/credentials.key`（Linux 通常为 `~/.config/nagi/credentials.key`，权限 `0600`）。
备份、迁移时必须同时保留数据库和此文件；共享数据库的多实例必须部署同一份密钥。

联系人策略抽屉中的生成模型选择独立保存，按「会话 → 该账号的好友/群默认模板 → 全局默认」生效；
恢复跟随默认请将该选择设为跟随模板。模型被默认值或会话引用时不能删除。
意图启用、自称、上下文条数和判定阈值在「意图识别 → 识别配置」中调整。

图片自动传给支持图片的生成模型。语音识别需选择已验证的全局默认 ASR 并授予会话“语音识别”；
OpenAI 兼容 ASR 在意图判断前经 `/audio/transcriptions` 转写；
已启用会话的当前输入由对应模型处理，历史媒体遵守历史读取授权。
OpenAI 兼容合成语音使用 `/audio/speech`，需要选择已验证的全局默认 TTS，并同时授予「合成语音回复」「当前会话回复」「发送媒体」。
语音发送复用投递台账及字符预算，结果未知时不重发，也不重复发送最终文字回复。
ASR／TTS 还支持“火山 Agent Plan 语音协议”：填写专属 API Key 和完整接口地址，
ASR 通过 WebSocket 上传服务器持有的音频，TTS 通过 HTTP 合成，音色可从官方 2.0 列表选择。

视频需要服务器安装 `ffmpeg`、`ffprobe`。`dsh-video-vision` 提供 `media.video.inspect`，
接受 B 站视频链接、BV 号、短链、分 P，以及 `attachment:current`（当前消息）或 `attachment:消息ID` 形式的 QQ 视频附件；
不接受任意本地路径或其他视频网站。授予「查看视频」后可使用；「使用B站登录凭证」
额外允许工具使用在模型配置页保存的 Cookie，未授予时匿名访问。
工具支持 `start`、`end`（秒）和 `frames`：默认 6 帧、最多 12 帧；字幕/转写每次覆盖最多
180 秒，需继续查看时指定下一时间窗口。下载最多 512 MiB，媒体工具超时 120 秒，
整条消息处理（含转写、意图判断和生成）的硬上限为 180 秒；管理员设置更短的 Agent 运行时限时优先遵守该值。缓存保留 24 小时。画面与平台字幕或转写均标注时间范围。

内置工具分三类：只读的 `system.current_time`、`qq.get_group_info`（`qq.group.read`）与
`qq.get_group_member_info`（`qq.group.member.read`）；回合内发送 `qq.send_current`
（`qq.reply.current`）；群管理的禁言/名片/移出（`qq.group.admin.mute`、`qq.group.admin.card`、
`qq.group.admin.kick`）。只读工具与连续回复（`qq.reply.current`）默认允许，其余默认拒绝，
全部由会话的「能力授权」决定。
模型无法传入任意群号或跨会话目标，工具调用在服务端做权限、账号与目标绑定校验，
结果按不可信数据处理；Eino 的 Agent/Model/Tool span 通过共享 TracerProvider 进入同一条 Langfuse trace。

### 工具权限模型

实际可用的工具由三层共同决定，没有任何进程级开关，也没有提供方启用列表：

1. **会话类型层**：描述符的 `ScopeKinds` 限定工具适用的会话类型；`qq.get_group_info` 仅群聊可用，
   私聊运行的工具列表中不会出现它。省略 `ScopeKinds` 表示所有会话类型可用。
2. **权限基线层**：`system_settings` 的 `tools.permission-defaults` 覆盖描述符兜底；
   内置基线授予 `qq.reply.current`，其余只读工具默认允许，写操作/群管理默认拒绝。
3. **作用域授权层**：`scope_policies.tool_permissions` 保存每个群/私聊的授权（如撤销 `qq.reply.current`），
   优先级最高；被撤销的工具不会出现在模型工具列表中，执行前 Guard 还会再校验一次。

控制台的「系统配置 → 工具与能力」维护运行限制、调用超时与结果上限，并只读展示
当前进程注册的提供方；「消息与留存」维护全局留存天数；联系人页的「能力授权」维护单个会话的授权。

`GET /api/v1/tools` 返回真正注册成功的全部工具与权限项（含默认授予状态），控制台的作用域弹窗据此渲染
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

# 数据库集成测试必须指向独立测试库（db17-init 已创建 nagi_ent_test）
NAGI_TEST_DSN="postgres://nagi:nagi-local@127.0.0.1:5434/nagi_ent_test?sslmode=disable" go test ./...
```

> 迁移往返测试会 `drop` 所有表。绝不把 `NAGI_TEST_DSN` 指向 `nagi` 库——跑一次就会清空会话、
> 消息、作用域与记忆数据。

并发相关改动建议追加 `go test -race ./...`。

## 目录结构

```
cmd/            进程入口、生命周期、组合根（assembly）、HTTP 路由
internal/       各业务上下文（onebot / bot / directory / scope / conversation / intent / agent / memory / observability）
pkg/            通用技术组件（config / db / logging / version）
pkg/db/        Ent schema、生成代码、Atlas 配置与版本化 SQL（独立执行）
configs/        config-example.yaml（config.yaml 为本地文件，已 gitignore）
```

## 构建

- 两个镜像各自在所属 Dockerfile 内多阶段构建：api 用 `infrastructure/golang:1.27` 编译，只把二进制放进
  `infrastructure/alpine:3.22`；web 用 `infrastructure/node:24-alpine` 跑 pnpm 构建，只把 `dist`
  放进 `infrastructure/nginx:alpine`。基础镜像默认走集群内 Zot
  （`zot.infra-registry.svc.cluster.local:5000/infrastructure/{alpine,nginx,node,golang}`，
  同一命名空间下还有 CI 用的 `postgres:17` 与 `buildkit`），由 `gitops-infra` 的
  `sync-base-images` 工作流同步。
- 本地复现同一条构建（需要 BuildKit；本机不信任 Zot 自签证书，用 Docker Hub 上的对应镜像覆盖集群内
  基础镜像）：
  ```bash
  sha=$(git rev-parse HEAD | cut -c1-7)
  docker build --build-arg GO_IMAGE=golang:1.27-alpine --build-arg BASE_IMAGE=alpine:3.22 \
    --build-arg VERSION=0.1.0-dev+$sha --build-arg COMMIT=$sha \
    --build-arg BUILD_DATE=$(git log -1 --format=%cI) -t nagi-api .
  docker build --build-arg NODE_IMAGE=node:24-alpine --build-arg BASE_IMAGE=nginx:alpine -t nagi-web web
  ```
- CI 构建时给 api 二进制注入版本信息：`Commit` 是与镜像 tag 一致的 7 位短 SHA，`BuildDate` 是提交时间，
  `Version` 形如 `0.1.0-dev+<短 SHA>`；`nagi -version` 与 `/api/v1/status` 能直接对回镜像 tag。
  镜像 config 里另有 `org.opencontainers.image.revision`（全量 SHA）、`.version`、`.source` 等标签。
  本地按上面的命令构建后可用 `docker run --rm nagi-api -version` 自查。
  注意本地示例用 Docker Hub 的对应镜像替代 Zot 基础镜像（如 `golang:1.27-alpine` 对应
  `infrastructure/golang:1.27`），产物不保证与 CI 逐字节一致。
- 构建作业依赖 runner 的 docker daemon 信任 Zot（自签证书 + 已登录）：基础镜像由 buildkit 拉取，
  构建后的版本校验由 daemon 拉取该镜像完成，runner 换机时需要同步这份配置。
- 检查环境由 `ci/check/Dockerfile` 定义，通过 `.github/workflows/check-environment.yml` 构建、推送并签名到 Zot。
  `verify` 直接使用该镜像内的 Go、Node、pnpm、just 和媒体工具，工具版本调整时重新发布环境镜像。
- 构建依赖层通过 Zot 的 `buildcache/nagi-api:main`、`buildcache/nagi-web:main` 跨运行复用。
  `GOCACHE` 与 pnpm store 的 BuildKit cache mount 不随 registry 缓存导出，常驻 builder 才能持续复用挂载内容。
  检查容器另将本机 `/cache/nagi` 挂到 `/cache`，复用 Go 模块、编译缓存及 pnpm store。
- `verify` 是 `main` 的必需检查：所有 PR（包括文档和 workflow 改动）都执行完整的 Go 与前端校验，
  变更检测失败也会使 `verify` 失败。PR 不构建、签名或推送镜像；main 推送按变更路径构建，
  手动 `workflow_dispatch` 执行全量检查和构建。
- 签名只打在 Zot 的镜像引用上（`cosign sign .../zot...@digest`），ghcr 上的同 digest 副本没有签名，
  验签要从 Zot 拉。
- 镜像 tag 是 7 位短 SHA，由 gitops 仓库的 Image Updater 自动开 PR；本仓库不再推送 digest 事件。
- Zot 的镜像清理由平台仓库管理的 `registry-maintenance` 定时任务执行，每天北京时间 04:20 运行。
  保留最近 10 个及 7 天内的构建版本，额外保护正在部署、ReplicaSet 回滚引用和 GitOps 最近 10 次配置引用；
  保护信息读取失败或 GitOps 配置变化时停止删除。构建缓存由 Zot 回收超过 30 天未推送的内容。
  `.github/workflows/ghcr-retention.yml` 当前只做预览，不删除 GHCR 包版本。

## 数据库迁移

业务库使用 PostgreSQL 17，固定数据库组为 `nagi`。Ent schema 位于 `pkg/db/schema/nagi`，
生成代码与迁移分别位于 `pkg/db/ent`、`pkg/db/migrations/nagi`。应用不执行迁移，只检查所需版本；
默认最多 10 个数据库连接，配置范围为 1–12，Ent 与原子 SQL 共享该连接池。

安装标准发行版 Atlas v1.3.0，使用免费迁移能力即可，无需登录或 Pro 订阅。
社区版不支持此处的 `ent://` schema 集成，因此不要用 community 发行版替换。开发流程：

```bash
go generate ./pkg/db
python3 scripts/database.py check
python3 scripts/database.py generate --name add_example_field
python3 scripts/database.py validate
python3 scripts/database.py lint
python3 scripts/database.py diff
```

生成命令只写 up/down 和校验文件，不连接业务运行库；Atlas dev database 固定为独立的
`docker://postgres/17/nagi_atlas_dev?search_path=public` 临时库。生成后必须评审 SQL，尤其是
DROP、重命名、类型收窄、非空要求及数据修复。`lint` 现在运行仓库内的离线保守检查，
不调用 Atlas Pro：自动接受建表、索引和增量 ADD；删除、重命名、ALTER COLUMN、
新增非空列（包括带默认值）、数据修改及其他无法识别的语句均要求人工评审。它不分析表大小、锁时长、
表达式函数副作用或实际数据兼容性，不能替代数据库回放与人工 SQL 评审。
不得通过扩大 exclude、忽略业务表或全局关闭检查规避。必要的破坏性变化单独成版本并评审。
评审后使用错误信息给出的 `--reviewed-up 文件名:SHA256` 显式指定该版本；文件内容变化后
原标记失效。该参数只记录调用者明确评审的内容，不代表工具验证了评审人身份。
down SQL 是人工回滚材料，参与摘要校验和测试库往返，不作为日常 up 的自动安全放行依据。
修改已生成但尚未交付的 SQL 后，执行 `python3 scripts/database.py hash` 更新校验文件。
`atlas.sum` 校验 up 链，`checksums.json` 补充校验 up/down 全部 SQL。

表、字段、外键、CHECK、常规和可表达的部分索引由 Ent schema 维护。
`pkg/db/sql-only.json` 是 SQL-only 对象清单：当前仅包含 `public.schema_migrations` 及版本写入。
应用独立检查其结构，迁移比较仅精确排除该元数据表。当前没有表达式索引；未来引入时必须声明
SQL 所有者、完整定义、精确排除项和专项校验。数据修复由独立版本 SQL 负责，不能依赖应用启动。

首次初始化本地**空的**新库：

```bash
docker compose up -d postgres17 db17-init
# 先校验、评审初始 SQL；仅对空 nagi_ent 执行一次。
docker exec -i nagi-postgres17 psql -U nagi -d nagi_ent -v ON_ERROR_STOP=1 -1 \
  < pkg/db/migrations/nagi/20261031051405_initial.up.sql
```

日常生产交付包括待执行版本顺序、对应 up SQL、`atlas.sum`、`checksums.json`、人工评审的
回滚材料和验证步骤。平台人员在 Bytebase 中逐版本、按事务执行增量 up；版本记录与结构变更
必须在同一事务提交。生产不运行 `atlas migrate apply`，应用和部署启动路径均不执行 down。
初始 down 会删除业务数据，需明确评审并以备份作为数据恢复来源。

本次使用新库切换，不迁移旧数据。停旧入口与 worker，等待在途发送结束并人工核对 unknown 后，
将新版本 DSN 改为 PG17 的 `nagi_ent`；旧 PG16 数据卷保留，旧 pending 任务不再消费。
新库开始接收数据或发送消息后，回退必须停机核对两库状态，禁止直接启动旧库消费冻结任务。
应用启动日志中的 `migration_version` 是已检查的版本，不表示应用执行了迁移。

队列领取通过会话行锁与 CTE/SKIP LOCKED 原子完成；processing 必须同时持有 claim token
和未过期租约。租约 2 分钟，每 30 秒批量续租；续租失败取消任务并停止新发送。过期恢复取消
prepared 记录并退款，将 sending 改为 unknown；有已发送或不确定结果的任务不自动重发。
单任务条件写回必须影响一行，过期执行者不能覆盖新 claim。
