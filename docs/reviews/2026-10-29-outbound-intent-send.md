# 投递台账 / 意图决策 / 发送工具：审查记录

覆盖 D（`bfb928c`）、P4（`087cc79`）、E（`b8ef0d5`）三个已合并单元。

## 审查方式与偏差

仓库的 review 技能要求独立 subagent 审查。本次共启动 6 个审查 subagent，全部在平台层失败：
4 次长时间无输出后取消，1 次遗留临时测试文件（已删除），1 次任务记录直接消失（`No such task`）。
因此改用替代流程，并把证据固定在此文件：

1. 逐条核对关键不变量（下方表格，含文件与行号）；
2. 对每个不变量做**变异实验**：临时破坏实现，确认对应测试失败，然后还原；
3. 数据库集成测试在独立临时库（`nagi_lead_test`）上以 `-race` 运行。

这是流程偏差，不是等价替代：缺少的是「第三方视角发现我们没想到的问题」。

## 不变量与证据

| # | 不变量 | 位置 | 变异实验 |
|---|---|---|---|
| 1 | `Reserve` 单事务内先 `FOR UPDATE` 锁任务行，再读预算/分序号/插入/累加 | `internal/outbound/infrastructure/pg.go` | 预算校验短路后 `TestReserveRejectsWithoutInserting` 失败 ✅ |
| 2 | 预算拒绝不插入、不累加，返回 `*BudgetError` | 同上 + `internal/outbound/domain/outbound.go` | 同上 |
| 3 | `FailJob` 在已有 `sent/unknown/sending` 时不重排，记 `failed`+`partial` | `internal/conversation/infrastructure/pg.go` | 去掉 `has_delivered` 分支后 `TestFailJobDoesNotRetryInFlightSend` 失败 ✅ |
| 4 | `RecoverStaleJobs` 先把 `sending` 置 `unknown`，只影响 `processing` | 同上 | 由 `TestRecoverStaleJobsPartitionsByLedger` 覆盖 |
| 5 | 群聊 `always_reply` 不依赖 Jev | `internal/intent/application/rules.go` | 关闭该分支后 `TestGroupAlwaysReply*` 失败 ✅ |
| 6 | 私聊三模式分别产出 `not_addressed`/`always_reply`/语义（Jev 不可用降级） | 同上 | `TestPrivateReplyModesDiverge` 覆盖 |
| 7 | 发送工具复用 `SanitizeReply`、目标固定当前会话、预算拒绝不发送 | `internal/agent/infrastructure/replytool/provider.go` | 工具内用例覆盖（链接被拒、预算拒绝不发送） |
| 8 | 三重门禁：`allow-send-current` + `allow-side-effects` + 权限 | `cmd/app/assembly/intent.go` | 关闭门禁后 `TestReplyToolRequiresDedicatedSwitch` 失败 ✅ |

## 运行记录

- `go test -race ./internal/outbound/... ./pkg/db/... ./internal/conversation/... ./internal/agent/...`（`NAGI_TEST_DSN` 指向临时库）：全部通过，覆盖迁移 up/down 与并发 Reserve。
- 变异实验 4 组，全部被现有用例捕获，无「永远通过」的空转测试。

## 第二轮：独立审查（Codex CLI）

subagent 连续失败后，改用本机 `codex` CLI（OpenAI Codex 0.165.0，只读沙箱、不同厂商模型）对同样三个提交做独立审查，输出：

* 严重：无
* 中等：1 条 —— `RecoverStaleJobs` 把**所有** `sending` 记录改为 `unknown`，而不仅是 `processing` 任务下的记录（`internal/conversation/infrastructure/pg.go:300-305`）
* 轻微：无
* A/C/D（Reserve 事务顺序与预算拒绝、always_reply/私聊分流、send_current 三重门禁）核验通过

### 对该发现的判断：不修改，调整规格表述

`sending` 是一个只能存在于进程存活期间的中间态：正常路径由 `Settle` 落定终态，异常路径（发送后 `Settle` 失败 → `FailJob`）会让任务变 `failed` 而记录停在 `sending`。恢复时若只处理 `processing` 任务，这条记录会永久停留在 `sending`，既不是已送达也不是失败，`HasDelivered` 也不会把它算作已投递——比误改更糟。

因此实现保持"把全部 `sending` 视为结果不明"是正确的；上一节表格里"只影响 processing"那句话是规格表述不准确，现修正为：**`sending` 记录一律落定为 `unknown`；只有 `processing` 任务会被改判为 `failed/partial`，其余任务状态不变。**

## 残余风险

- 缺少第三方审查，可能遗漏我们共同的盲点（例如并发边界、错误分类）。
- 若后续恢复 subagent 审查，应优先补审本文件覆盖的三个提交。
