# 测试约束

## 决定是否改测试

仅在以下情况新增或调整测试：用户明确要求；修改改变已有测试覆盖的行为；修复
核心逻辑 Bug 且小范围回归明显必要；仓库明确要求该类修改具有测试。

普通小修改可运行现有测试，不机械增加用例。不要为了完整性引入框架、fixture、
mock 基础设施、测试依赖，或仅为便于测试而重构生产代码。

## 测试职责与有效性

- 每个用例应明确主要被测对象、输入或触发条件、可观察结果，以及什么错误会使它
  失败。验证行为，不只是调用实现或复制常量。
- 组件测试与集成测试可共存，相似断言可验证不同调用层；解释各自独有的失败场景，
  不按测试名称或相同配置键判断重复。
- 避免替代机制掩盖目标行为：验证帧解复用时区分 echo 响应与事件；验证重连时确保
  拨号次数可观察；验证 panic 隔离时，确保目标回调具有可观察行为。
- 异步测试应有明确完成条件、必要同步和有界等待，不单靠固定休眠证明事件发生；
  项目内使用 waitFor(cond) 有界轮询模式。
- 同包共享 helper 可以跨文件使用；仅因共享、文件较长或职责名称不够漂亮，不新增
  公共辅助文件或机械拆分测试。
- 测试替身（fakeTransport、fakeSender 等）如被多 goroutine 访问，必须加锁或提供
  快照读取；并发断言直接读字段会被 `go test -race` 判定竞争。

## 审查与删除

删除或合并用例前，说明它原来验证的行为、是否具有独有覆盖，以及由哪个保留用例
替代；如果相关需求明确取消，说明覆盖对象为何不再需要。

同一生产分支、相似代码、暂未接入的生产模块、helper 都有引用或测试全绿，都不足以
单独证明测试冗余或有效。无法确认时报告不确定性，不凭猜测删除。

必要且在任务授权范围内时，可用隔离的临时替换验证保留测试能否发现目标回归。
实验不得污染最终差异，也不要求为每个测试引入变异测试工具。

## 测试金字塔（Nagi 版）

| 层级 | 主要对象 | 手段 | 命令 |
| --- | --- | --- | --- |
| 单元 | Domain、Application 编排 | 表驱动、`internal/testsupport` Fake | `just verify` |
| API | Handler、middleware | `httptest` | `just verify` |
| Repository | `internal/*/infrastructure` 持久化 | **真 PostgreSQL**（`dbtest.Open`、迁移） | **`just verify-repo`** |
| Live | FFmpeg、Ark、真实模型 | opt-in 环境变量 | `just verify-integration`（非 PR 必需） |

Application 单测 **不替代** Repository 真库测：Fake 只验证编排与错误传播；SQL、JSONB 往返、
`SKIP LOCKED`、双连接池等必须在 integration 文件中用真库验证。

## 目录与替身

- 单测与生产代码同包，文件 `*_test.go`；跨包 Fake 仅 `internal/testsupport`。
- 真库用例：`//go:build integration`，或文件名 `*_pg_test.go` / `*_integration_test.go`
  （文件头注释标明 Repository integration）。改 `infrastructure` 仓储实现时优先改/补此类文件。
- `testdata/` 放在使用它的包下；不建顶层 `test/mocks` 树。
- **Stub**：函数字段返回预设值（HTTP、OneBot）。
- **Fake（testsupport）**：Application 层内存仓储。
- **Real PG**：Repository 层默认且唯一；**不用** sqlmock 代替仓储。
- **窄 sqlmock**：仅极少数直连 SQL 切片（如 media `storeTask` 单条 `UPDATE`），不扩展为通用 Repository 策略。

## PR 检查清单（摘要）

改仓储或 SQL 时核对：是否有真库 integration 覆盖；`NAGI_TEST_DSN` 是否仅指向可销毁的
`*_test` 库；Application 断言是否与 infrastructure 重复且 **不测 SQL**（可删重复）。
改并发路径时考虑 `just verify-race` 相关包。不把 integration 全 Skip 当作合并通过。

## 功能交付验证

- Go 改动执行 **`just verify`**；改 Repository 或 migration 时追加 **`just verify-repo`**
  （PR / CI 必需）。前端改动在 `web/` 执行 `pnpm typecheck`、`pnpm lint`、`pnpm test`，
  影响构建、路由或产物时执行 `pnpm build`。数据库与并发改动追加相应的集成测试或竞态检查。
- 通过实际业务入口检查本次改动的输入、输出和状态变化，按影响核对持久化和失败路径。
  使用适用的隔离环境，测试结束后只停止本次启动的服务。
- 涉及页面展示或交互时，启动适用的前后端服务，通过真实浏览器从正常入口执行核心操作，核对结果反馈和
  刷新后的状态，并检查相关空态、失败提示及控制台与网络异常。接口成功和构建成功不能替代页面试用。
- 修复本次改动导致的失败后，重跑受影响的检查和功能流程。缺少浏览器、服务或授权时，注明未验证的部分。

## 外部依赖分类

`just verify`（Fast Tier）不连 PostgreSQL、不读 `configs/config.yaml` 的 `database.dsn`。
`just verify-repo`（PR 门禁）**必须**提供 `NAGI_TEST_USE_DB=1` 与 `NAGI_TEST_DSN`（`dbtest`
派生 `*_test` 库，见 [database.md](database.md)）。

- **本地**：未设 `NAGI_TEST_DSN` 时 `just verify-repo` 会执行 `just test-infra-up`（`docker compose`
  PostgreSQL **18.6**、Redis、MinIO），默认
  `postgres://nagi:nagi-local@127.0.0.1:5434/nagi_test?sslmode=disable`。业务库为 `nagi_v2`（同端口）。
- **CI**：workflow 不设 `services.postgres`；注入 secret **`NAGI_CI_TEST_DSN`** 为
  `NAGI_TEST_DSN`，连接 CNPG 读写端 `postgresql-rw.dev-cnpg.svc:5432` 上已存在的 **`nagi_test`**
  库（勿在 CI 再起 PG 实例）。

| 类型 | 代表 | Fast（verify） | Repository / Live |
| --- | --- | --- | --- |
| Application | `scope/application` 等 | `testsupport` Fake | — |
| Repository | `*/infrastructure/*_pg_test.go`、`dbtest.Open` | 不编译 / Skip | **`verify-repo` 真库** |
| 本机 CLI | models ASR/TTS | 注入 PCM/MP3 | `verify-integration` + ffmpeg |
| Live API | Ark、真实模型提取 | 不编译 | `verify-integration` + 凭据 |
| 对象存储 | inbound_archive | 内存 S3 + 窄 sqlmock | 全链路 `inbound_archive_test.go`（真库） |
| SnowLuma | `ws_test` | `httptest` | — |
| HTTP API | routes、middleware | `httptest` | — |

合并与发布前：**`just verify` + `just verify-repo`** 均应通过；CI **`verify` workflow** 另跑
**`just verify-security`**。FFmpeg/Ark 等见 `just verify-integration`。**`main` push** 在同一
`verify` workflow 内串联 `verify` → `compile` → `release`；手动重打镜像用 **`build-images`**
（仅 `workflow_dispatch`，不跑校验）。PR 与非 `main` 分支不构建镜像。

## 验证与撤销

- 按修改影响选择验证，优先已有相关测试；跨模块改动需要覆盖受影响的调用链。
- 并发逻辑变化时执行相关包的 `go test -race`；不对每次任务无条件重复全量检查。
  适用检查通过后，只有新修改、失败或未解决疑点才扩大或重复验证。
- 明确报告实际运行的检查、失败项和跳过原因。在 **`verify-repo` 已启用真库** 时，
  因未设 `NAGI_TEST_USE_DB` 而 Skip 的 `dbtest` 用例视为门禁失败。Live 子集（Ark/FFmpeg）
  在 PR 中允许 Skip。只修改技能等指令文件时，不为此运行无关业务测试。
- 可选：`just verify-race`、`just verify-cover`（见 Justfile）；`just verify-security` 在 CI `verify` 门禁中执行。
- 撤销操作只恢复指定修改涉及的原有测试，不新增防止再犯的禁止性测试。
- 不通过删除断言、跳过失败用例或改变预期来隐藏本次修改引入的问题。
