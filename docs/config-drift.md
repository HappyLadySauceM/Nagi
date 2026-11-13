# 默认配置与固定流程漂移检测

本文件说明仓库如何捕获默认配置、参与判断阈值和固定流程版本的意外变化。

## 覆盖范围

| 快照/检查 | 文件 | 固定的内容 |
| --- | --- | --- |
| 内置默认值 | `pkg/config/testdata/defaults.golden.json` | `config.Load` 无文件、无环境变量时的全部内置默认配置 |
| 示例配置文件 | `pkg/config/testdata/config-example.golden.json` | `configs/config-example.yaml` 装载后的配置（含占位符） |
| 固定流程契约 | `internal/intent/domain/testdata/flow-contract.golden.json` | `FlowVersion`、`SnapshotVersion`、全部固定节点与边 |
| 默认阈值 | `internal/intent/application/default_config_threshold_test.go` | `intent.continuation-threshold`、`intent.interjection-threshold`、`intent.addressed-other-max` 与参与判断基线一致 |

`just verify` 会通过 `go test ./...` 执行以上检查；定向复跑命令：

```bash
go test ./pkg/config -run 'TestConfig.*Snapshot' -count=1
go test ./internal/intent/domain -run TestFixedFlowContractSnapshot -count=1
go test ./internal/intent/application -run TestDefaultIntentThresholdsMatchParticipationBaseline -count=1
```

## 变更处理流程

1. **确认需求**：默认配置项、阈值、`FlowVersion` 或 `SnapshotVersion` 的变化必须有产品确认，PR 要写明对应需求单/确认依据。不能为了通过测试直接更新快照。
2. **更新实现**：按需求修改 `pkg/config`、`configs/config-example.yaml` 或 `internal/intent/domain` 中的值。
3. **更新快照**：仅确认变化符合预期后，执行：
   ```bash
   go test ./pkg/config -run 'TestConfig.*Snapshot' -update
   go test ./internal/intent/domain -run TestFixedFlowContractSnapshot -update
   ```
   逐行审查 golden diff，确认没有顺带改变无关配置项。
4. **阈值变化**：默认阈值与参与判断基线（`0.80 / 0.95 / 0.60`）绑定。确认调整时，同步更新 `pkg/config` 快照、`internal/intent/application/participation_baseline_test.go` 中的 `baselineThresholds` 和 `docs/reviews/2026-11-10-participation-baseline.md` 的报告，并重新评审基线样本的预期结果；不得只改其中一处。
5. **流程版本变化**：`FlowVersion` 或节点/边结构变化要同步前端契约；`SnapshotVersion` 变化要评估历史决策快照的兼容与回放策略，不能只改常量。
6. **验证**：执行 `just verify`；涉及前端展示时按仓库 review 技能做页面联调。

## 测试失败时

- 失败信息会列出 `want` 与 `got`，并提示对应快照文件或阈值基线。
- 如果变化不是本次需求有意引入的，请还原代码或配置，不要更新快照来掩盖漂移。
- 如果确认是有意变更，按上面的流程更新快照、报告与回归，并在 PR 中记录产品确认依据。
