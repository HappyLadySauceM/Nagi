# 参与判断基线报告（2026-11-10）

## 定位与范围

本报告是 Nagi 参与判断的**策略回归基线**，不是 Jev 语义模型的真实准确率评估。
所有语义分数均为测试中人工设置的合成输入，用于固定「给定一组语义回答后，当前策略如何决策」；
不证明模型能正确理解多意图、否定、反问或真实聊天语境。

本轮固定对比阈值（不从运行时配置读取，不随默认值漂移）：

- `Continuation = 0.80`
- `Interjection = 0.95`
- `AddressedOtherMax = 0.60`

默认配置漂移测试不在本轮范围；若需验证默认值变化对基线的影响，应另立独立任务。

## 样本与期望

| 样本 ID | 输入情境 | 人工设置的语义分数 | 当前期望（决策/原因） | 实际 | 冲突分类 |
| --- | --- | --- | --- | --- | --- |
| `explicit_at` | 群里明确 @ 机器人 | 规则直接命中，不调用语义 | reply / `explicit_mention` | 一致 | 无 |
| `reply_quote` | 引用机器人此前的消息 | 规则直接命中，不调用语义 | reply / `explicit_mention` | 一致 | 无 |
| `natural_follow_up` | 承接机器人上一轮的自然追问 | ContinuesBotThread=0.90，AddressedToOther=0.10 | reply / `active_thread_continuation` | 一致 | 无 |
| `addressed_to_other` | 明显在对别人说话 | ContinuesBotThread=0.10，AppropriateToJoin=0.10，AddressedToOther=0.90 | silent / `addressed_to_other` | 一致 | 无 |
| `closing` | 结束语 | IntentCategory=`closing`，ContinuesBotThread=0.90 | silent / `closing` | 一致 | 无 |
| `consecutive_short_messages` | 连续多条短消息后的追问 | ContinuesBotThread=0.85，AddressedToOther=0.20 | reply / `active_thread_continuation` | 一致 | 无 |
| `semantic_unavailable_group` | Jev 不可用（群聊） | `Available=false`，FailureCode=`semantic_unavailable` | silent / `jev_unavailable` | 一致 | 服务不可用 |
| `semantic_unavailable_private` | Jev 不可用（私聊） | `Available=false`，FailureCode=`semantic_unavailable` | reply / `private_semantic_unavailable` | 一致 | 服务不可用 |
| `conflict_interjection_addressed_to_other` | 插话分数高但同时在对别人说话 | AppropriateToJoin=0.97，AddressedToOther=0.90 | reply / `interjection` | 一致（当前行为） | **插话 vs 对别人说话：已知策略顺序，未修复** |
| `conflict_continuation_addressed_to_other` | 续聊分数高同时在对别人说话 | ContinuesBotThread=0.90，AddressedToOther=0.90 | silent / `addressed_to_other` | 一致 | 续聊 vs 对别人说话 |
| `conflict_directed_to_bot_addressed_to_other` | 指向机器人分数高同时对别人说话 | DirectedToBot=0.90，AddressedToOther=0.90 | silent / `addressed_to_other` | 一致 | 指向机器人 vs 对别人说话 |
| `interjection_clean` | 没有对别人说话的插话 | AppropriateToJoin=0.96，AddressedToOther=0.30 | reply / `interjection` | 一致 | 无 |
| `multi_intent_mixed` | 一句话含查天气+提醒开会两个意图 | DirectedToBot=0.90，AddressedToOther=0.10，IntentCategory=`chat` | reply / `directed_to_bot` | 一致 | 多意图契约限制：当前只有一个 `IntentCategory` |
| `negation_question` | 否定式提问 | DirectedToBot=0.90，AddressedToOther=0.10，IntentCategory=`question` | reply / `directed_to_bot` | 一致 | 否定契约限制：当前无否定标记 |
| `rhetorical_question` | 反问 | DirectedToBot=0.20，AppropriateToJoin=0.40，AddressedToOther=0.20，IntentCategory=`question` | silent / `ambiguous` | 一致 | 反问契约限制：当前无反问标记 |

## 已知策略事实与能力边界

- `internal/intent/application/rules.go` 的 `interjection` 分支位于 `addressed_to_other` 拒绝之前，且没有检查 `AddressedOtherMax`。`conflict_interjection_addressed_to_other` 固定了当前输出 `reply/interjection`；本轮按产品决定保持现状，不调整主动插话策略或默认阈值。
- `SemanticAnswer` 目前只有单个 `IntentCategory` 和四个浮点分数，没有多意图、否定或反问字段。因此 `multi_intent_mixed`、`negation_question`、`rhetorical_question` 仅验证「给定合成语义分数后的策略映射」，不能用于宣称 Jev 具备这些语言理解能力；能力限制属于契约事实，不是测试遗漏。

## 复跑与结果保存

```bash
cd /home/HappyLadySauce/Code/Nagi/Nagi-worktrees/intent-participation-baseline
bash -lc 'go test ./internal/intent/application -run "TestParticipation" -count=1 -v' \
  | tee /tmp/nagi-participation-baseline.log
bash -lc 'just verify' | tee /tmp/nagi-participation-baseline-verify.log
```

- 结果日志：`/tmp/nagi-participation-baseline.log`、`/tmp/nagi-participation-baseline-verify.log`
- 预期：所有样本 PASS；`TestParticipationDecisionBaseline` 首行会打印本轮固定阈值。

## 验证记录

- `go test ./internal/intent/application -run "TestParticipation" -count=1 -v`：PASS。
- `just verify`：PASS（`go mod tidy`、`go vet ./...`、`go test ./...`、`go fmt ./...`）。
- 未调用真实 Jev、未连接真实 QQ、未使用浏览器；本报告不把这些未执行项当作通过。

## P0 策略修订（2026-11-12）

- 群聊 `addressed_to_other` 现在先于 `interjection` 判断；`conflict_interjection_addressed_to_other` 的期望已改为 `silent / addressed_to_other`。
- 私聊 `intent_based` 正常问候、提问、闲聊统一走 `private_normal` 期待回复路径；`closing` 仍为 `silent / closing`，语义不可用仍回退 `private_semantic_unavailable`。
- 上述改为本轮 P0 代码与回归测试所固定；此前报告只作历史基线，不再代表当前行为。
