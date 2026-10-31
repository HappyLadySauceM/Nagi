import { Wrench } from 'lucide-react'
import { Label } from '../../../components/ui/label'
import { NumberField } from './fields'
import { SettingsCard } from './settings-card'
import type { SectionProps } from './types'

/**
 * Agent runtime limits and tool call limits. Providers carry no enable list:
 * everything registered at startup is available, and the conversation's grant
 * decides whether the model ever sees a given tool.
 * 运行限制与工具调用限制。提供方没有启用列表：
 * 启动时注册的全部可用，会话的授权决定模型是否能看到某个工具。
 */
export function ToolsSection({
  draft,
  errors,
  updateAgent,
  updateTools,
  providers,
}: SectionProps) {
  return (
    <div className="space-y-4">
      <SettingsCard
        id="tools"
        title="工具与能力"
        description="提供方随进程启动全部注册；会话是否可使用由联系人页的「能力授权」决定。"
        icon={Wrench}
        tag="全部可用"
      >
        <div className="py-2">
          <Label className="text-sm">已注册的提供方</Label>
          <div className="mt-2 flex flex-wrap gap-2">
            {providers.length === 0 ? (
              <p className="text-meta text-muted-foreground">当前进程没有注册任何工具提供方。</p>
            ) : (
              providers.map((provider) => (
                <span
                  key={provider}
                  className="rounded-full border border-border bg-secondary px-3 py-1.5 font-mono text-meta text-muted-foreground"
                >
                  {provider}
                </span>
              ))
            )}
          </div>
          <p className="mt-2 text-meta leading-relaxed text-muted-foreground">
            这里没有启用开关：进程注册多少提供方就全部可用，模型能否调用只看对应会话是否授权。
          </p>
        </div>
        <div className="grid gap-x-6 sm:grid-cols-3">
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
        <div className="mt-1 grid gap-x-6 sm:grid-cols-2">
          <NumberField
            id="tools-timeout"
            label="单次调用超时"
            value={draft.tools.timeoutSeconds}
            onChange={(timeoutSeconds) => updateTools({ timeoutSeconds })}
            unit="秒"
            hint="超时的调用按失败返回，不会阻塞整个回合。"
            error={errors['Tools.TimeoutSeconds']}
          />
          <NumberField
            id="tools-max-result"
            label="结果字节上限"
            value={draft.tools.maxResultBytes}
            onChange={(maxResultBytes) => updateTools({ maxResultBytes })}
            unit="字节"
            hint="超出即拒绝，避免超长结果挤占上下文。"
            error={errors['Tools.MaxResultBytes']}
          />
        </div>
      </SettingsCard>
    </div>
  )
}
