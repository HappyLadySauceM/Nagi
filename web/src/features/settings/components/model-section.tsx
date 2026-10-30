import { BrainCircuit, Sparkles } from 'lucide-react'
import { NumberField, SelectField, SwitchField, TextField } from './fields'
import { SettingsCard } from './settings-card'
import type { SectionProps } from './types'

const PROVIDER_OPTIONS = [
  { value: 'jev', label: 'jev · SystemOne' },
  { value: 'openai', label: 'openai · 兼容端点' },
]

/**
 * Generation and decision settings. Both sections are live: saving applies to
 * the next run, and the credential badges explain why a switch may be locked.
 * 生成与决策配置。两个分区都会即时生效：保存后作用于下一次运行，
 * 凭据徽标说明了开关为何可能被锁定。
 */
export function ModelSection({
  draft,
  errors,
  credentials,
  updateAI,
  updateIntent,
}: SectionProps) {
  return (
    <div className="space-y-4">
      <SettingsCard
        id="model"
        title="生成模型"
        description="模型负责把已授权的上下文写成一条自然回复；密钥只从配置文件读取。"
        icon={Sparkles}
        tag="保存即生效"
        warning={
          credentials.ai
            ? undefined
            : '配置文件中没有 ai.api-key，启用开关会被拒绝；请先在 configs/config.yaml 或环境变量中补充密钥。'
        }
      >
        <SwitchField
          id="ai-enabled"
          label="启用生成模型"
          description="关闭后只做意图决策与记录，不再生成任何回复正文。"
          checked={draft.ai.enabled}
          disabled={!credentials.ai}
          onChange={(enabled) => updateAI({ enabled })}
          error={errors['AI.Enabled']}
          badge={credentials.ai ? '密钥已配置' : '缺少密钥'}
        />
        <div className="grid gap-x-6 sm:grid-cols-2">
          <TextField
            id="ai-base-url"
            label="接口地址"
            value={draft.ai.baseURL}
            onChange={(baseURL) => updateAI({ baseURL })}
            placeholder="https://api.openai.com/v1"
            hint="OpenAI 兼容的 /chat/completions 端点。"
            error={errors['AI.BaseURL']}
            disabled={!draft.ai.enabled}
            mono
          />
          <TextField
            id="ai-model"
            label="模型名"
            value={draft.ai.model}
            onChange={(model) => updateAI({ model })}
            placeholder="gpt-4o-mini"
            hint="由服务端原样透传，不做能力探测。"
            error={errors['AI.Model']}
            disabled={!draft.ai.enabled}
            mono
          />
          <NumberField
            id="ai-max-output-tokens"
            label="单次输出上限"
            value={draft.ai.maxOutputTokens}
            onChange={(maxOutputTokens) => updateAI({ maxOutputTokens })}
            unit="token"
            hint="与单轮字数预算共同限制一条回复的长度。"
            error={errors['AI.MaxOutputTokens']}
            disabled={!draft.ai.enabled}
          />
          <NumberField
            id="ai-generation-timeout"
            label="生成超时"
            value={draft.ai.generationTimeoutSeconds}
            onChange={(generationTimeoutSeconds) => updateAI({ generationTimeoutSeconds })}
            unit="秒"
            hint="单次 HTTP 生成调用的上限，超时按失败处理。"
            error={errors['AI.GenerationTimeoutSeconds']}
            disabled={!draft.ai.enabled}
          />
        </div>
      </SettingsCard>

      <SettingsCard
        id="intent"
        title="意图流水线"
        description="决定是否插话，并把通过判定的消息交给生成模型；关闭时消息仍入库但不回复。"
        icon={BrainCircuit}
        tag="规则 + 语义"
      >
        <SwitchField
          id="intent-enabled"
          label="启用意图流水线"
          description="关闭后工作器仍消费任务，只是跳过决策，任务不会积压。"
          checked={draft.intent.enabled}
          onChange={(enabled) => updateIntent({ enabled })}
          error={errors['Intent.Enabled']}
        />
        <div className="grid gap-x-6 sm:grid-cols-2">
          <SelectField
            id="intent-provider"
            label="语义提供方"
            value={draft.intent.provider || 'jev'}
            options={PROVIDER_OPTIONS}
            onChange={(provider) => updateIntent({ provider: provider as 'jev' | 'openai' })}
            hint="jev 使用 SystemOne 接口，openai 使用兼容对话接口。"
            error={errors['Intent.Provider']}
            disabled={!draft.intent.enabled}
          />
          <TextField
            id="intent-endpoint"
            label="语义接口地址"
            value={draft.intent.endpoint}
            onChange={(endpoint) => updateIntent({ endpoint })}
            placeholder="https://api.typesafe.ai/v1/systemone"
            error={errors['Intent.Endpoint']}
            disabled={!draft.intent.enabled}
            mono
          />
          <TextField
            id="intent-model"
            label="语义模型名"
            value={draft.intent.model}
            onChange={(model) => updateIntent({ model })}
            error={errors['Intent.Model']}
            disabled={!draft.intent.enabled}
            mono
          />
          <TextField
            id="intent-bot-name"
            label="机器人自称"
            value={draft.intent.botName}
            onChange={(botName) => updateIntent({ botName })}
            hint="写入系统提示与语义请求，留空时使用“助手”。"
            disabled={!draft.intent.enabled}
          />
          <NumberField
            id="intent-timeout"
            label="语义超时"
            value={draft.intent.timeoutSeconds}
            onChange={(timeoutSeconds) => updateIntent({ timeoutSeconds })}
            unit="秒"
            hint="超时降级为仅规则决策，不会臆测结果。"
            error={errors['Intent.TimeoutSeconds']}
            disabled={!draft.intent.enabled}
          />
          <NumberField
            id="intent-history-limit"
            label="上下文条数"
            value={draft.intent.historyLimit}
            onChange={(historyLimit) => updateIntent({ historyLimit })}
            unit="条"
            hint="读取历史的条数上限，受会话级历史授权约束。"
            error={errors['Intent.HistoryLimit']}
            disabled={!draft.intent.enabled}
          />
          <NumberField
            id="intent-continuation"
            label="接续话题阈值"
            value={draft.intent.continuationThreshold}
            onChange={(continuationThreshold) => updateIntent({ continuationThreshold })}
            step={0.05}
            hint="高于该分数才认为在延续机器人上一条发言。"
            error={errors['Intent.ContinuationThreshold']}
            disabled={!draft.intent.enabled}
          />
          <NumberField
            id="intent-interjection"
            label="插话阈值"
            value={draft.intent.interjectionThreshold}
            onChange={(interjectionThreshold) => updateIntent({ interjectionThreshold })}
            step={0.05}
            hint="高于该分数才允许主动插话，越高越克制。"
            error={errors['Intent.InterjectionThreshold']}
            disabled={!draft.intent.enabled}
          />
          <NumberField
            id="intent-addressed-other"
            label="他人指向上限"
            value={draft.intent.addressedOtherMax}
            onChange={(addressedOtherMax) => updateIntent({ addressedOtherMax })}
            step={0.05}
            hint="超过该分数视为在跟别人说话，机器人不插嘴。"
            error={errors['Intent.AddressedOtherMax']}
            disabled={!draft.intent.enabled}
          />
        </div>
      </SettingsCard>
    </div>
  )
}
