import { Gauge } from 'lucide-react'
import { NumberField } from './fields'
import { SettingsCard } from './settings-card'
import type { SectionProps } from './types'

/**
 * Per-run budgets. The scope policy can still override the sending budget, so
 * these values are the fallback for conversations without a stored policy.
 * 单次运行的预算。作用域策略仍可覆盖发送预算，
 * 因此这里的取值是没有存量策略的会话所使用的兜底。
 */
export function LimitsSection({ draft, errors, updateAgent, updateSending }: SectionProps) {
  return (
    <SettingsCard
      id="limits"
      title="发送与限额"
      description="限制单次运行的步数、时长与出站消息；会话策略存在时以策略为准。"
      icon={Gauge}
      tag="兜底预算"
    >
      <div className="grid gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
        <NumberField
          id="agent-max-steps"
          label="最大推理步数"
          value={draft.agent.maxSteps}
          onChange={(maxSteps) => updateAgent({ maxSteps })}
          unit="步"
          hint="ReAct 循环上限，防止工具调用反复自问自答。"
          error={errors['Agent.MaxSteps']}
        />
        <NumberField
          id="agent-max-tool-calls"
          label="最大工具调用"
          value={draft.agent.maxToolCalls}
          onChange={(maxToolCalls) => updateAgent({ maxToolCalls })}
          unit="次"
          hint="单次运行内所有工具共享的调用预算。"
          error={errors['Agent.MaxToolCalls']}
        />
        <NumberField
          id="agent-max-run-duration"
          label="单次运行时长"
          value={draft.agent.maxRunDurationSeconds}
          onChange={(maxRunDurationSeconds) => updateAgent({ maxRunDurationSeconds })}
          unit="秒"
          hint="包含模型与工具调用的总时长上限。"
          error={errors['Agent.MaxRunDurationSeconds']}
        />
      </div>
      <div className="mt-1 rounded-lg border border-border/70 bg-surface/40 p-3">
        <p className="mb-1 text-caption text-muted-foreground">
          出站预算：模型可连续发送多条气泡，超出后本回合停止发送且不报错。
        </p>
        <div className="grid gap-x-6 sm:grid-cols-3">
          <NumberField
            id="sending-max-messages"
            label="每轮最多消息"
            value={draft.agent.sending.maxMessagesPerRun}
            onChange={(maxMessagesPerRun) => updateSending({ maxMessagesPerRun })}
            unit="条"
            error={errors['Agent.Sending.MaxMessagesPerRun']}
          />
          <NumberField
            id="sending-max-chars"
            label="单条字数上限"
            value={draft.agent.sending.maxCharsPerMessage}
            onChange={(maxCharsPerMessage) => updateSending({ maxCharsPerMessage })}
            unit="字"
            error={errors['Agent.Sending.MaxCharsPerMessage']}
          />
          <NumberField
            id="sending-max-total"
            label="整轮字数上限"
            value={draft.agent.sending.maxTotalCharsPerRun}
            onChange={(maxTotalCharsPerRun) => updateSending({ maxTotalCharsPerRun })}
            unit="字"
            error={errors['Agent.Sending.MaxTotalCharsPerRun']}
          />
        </div>
      </div>
    </SettingsCard>
  )
}
