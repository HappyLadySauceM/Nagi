# Nagi 入站决策评估

从当前有历史读取和留存授权的会话中选一段连续入站，用消息 ID 闭区间固定样本。先干跑确认数量，再加 `--upload` 发布到配置指定的 Langfuse 项目。

```sh
go run ./cmd/nagi-eval --config configs/config.yaml --dataset nagi-intent-baseline \
  --bot 10001 --conversation 11111111-2222-3333-4444-555555555555 \
  --first 25 --last 77 --split tuning --baseline-release 4802b44 \
  --nagi-url https://nagi.example.org
```

`NAGI_EVAL_READ_DSN` 可指向只读数据源。`NAGI_EVAL_HMAC_KEY` 可提供独立的假名密钥，默认使用配置里的观测假名密钥。重跑同一数据集需沿用原密钥。已有样本会保留原始输入和人工标签。参与人和话题使用稳定假名，正文隐藏常见凭证、链接、CQ 段和长数字，附件内容通过授权的 Nagi 详情获取。

同一会话内整段话题用同一个 `--split`，首批样本用 `tuning`。将其他完整会话导出为 `holdout`，供候选选择后的验收。元数据的 `missing` 表示有未保存的决策输入或无法取回的附件。标注可以查看这些样本，评分时按可用字段计算覆盖率。

## 在 Langfuse 标注

在 Dataset 的每个 Item 中编辑 Expected Output。至少填写 `participation`，确定对象或话题时再填相应字段。可以为人数较多或上下文不充分的场景保留多个可接受动作。

```json
{
  "participation": "must_reply",
  "acceptableActions": ["continue", "open"],
  "acceptableTopicIds": [],
  "acceptableTargetMessageIds": []
}
```

`participation` 可选 `must_reply`（应回）、`optional`（可回可不回）和 `must_be_silent`（应沉默）。动作使用 `continue`、`open`、`close`、`wait` 或 `silent`。对象和话题 ID 取自样本输入中的假名。先保存预期，再查看线上 Trace 的实际行为，方便复核分歧。

## 离线策略对照

`compare` 从标准输入读取合成 JSON 清单，输出双臂逐条结果和汇总。请用合成别名与合成消息。
在仓库根目录运行以下命令。`-buildvcs=true` 为报告保留提交和工作区修改状态。

```sh
eval_dir=$(mktemp -d)
go build -buildvcs=true -o "$eval_dir/nagi-eval" ./cmd/nagi-eval
"$eval_dir/nagi-eval" compare < cmd/nagi-eval/testdata/policy-comparison.json
```

两臂固定作用域策略、规则集问题与阈值，并共享样本的到达时上下文和语义分数。示例中
`[]` 表示明确为空的历史或当前消息附件。附件状态须与当前消息的图片、语音、视频或文件段对应。
`missing` 用于不能重建的证据。模型配置和 Prompt 引用用于
标识冻结输入。运行本身没有任何模型、QQ、数据库或 Langfuse 调用，且不写入记忆或画像。

报告记录清单和可执行文件的 SHA-256。应回召回、误插话和话题命中只使用两臂都有结果的样本，
分母为其中相应标签的样本数。报告同时给出每臂可评估率和缺失项。空分母、回复对象命中和
线上 P95 使用 `null`，模型调用数与 token 为 0。送达与自然度需分别使用实际回执和盲评。
