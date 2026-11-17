import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { History, SlidersHorizontal, Workflow } from 'lucide-react'
import { toast } from 'sonner'
import { useAccountContext } from '../accounts/account-context'
import { useSystemSettings } from '../settings/queries'
import { SettingsCard } from '../settings/components/settings-card'
import { NumberField, SwitchField } from '../settings/components/fields'
import { Button } from '../../components/ui/button'
import { FeedbackState } from '../../components/feedback-state'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { apiRequest } from '../../lib/api/client'
import { toSettingsView } from '../../lib/api/adapters/settings'
import { queryKeys } from '../../lib/query-keys'
import type {
  SettingsContext,
  SettingsIntent,
  SystemSettings,
} from '../../lib/api/contracts'
import { useEditGuard } from './edit-guard'
import { useEffectiveRules } from './rules-queries'

interface ConfigDraft {
  context: SettingsContext
  intent: SettingsIntent
}

function pickConfig(settings: SystemSettings): ConfigDraft {
  return {
    context: { ...settings.context },
    intent: { ...settings.intent },
  }
}

function serialize(draft: ConfigDraft | null): string {
  return draft ? JSON.stringify(draft) : ''
}

function IntentConfigForm() {
  const query = useSystemSettings()
  const { selectedBotId } = useAccountContext()
  const effective = useEffectiveRules(
    selectedBotId !== undefined
      ? { botId: selectedBotId, scopeType: 'group', scopeId: 0 }
      : null,
  )
  const client = useQueryClient()
  const [draft, setDraft] = useState<ConfigDraft | null>(null)
  const [version, setVersion] = useState(0)
  const baseline = useRef<ConfigDraft | null>(null)

  useEffect(() => {
    if (!query.data) return
    const next = pickConfig(query.data.settings)
    if (!draft || serialize(draft) === serialize(baseline.current)) {
      baseline.current = next
      setDraft(next)
      setVersion(query.data.version)
    }
    // Keep an edited draft on its original revision so concurrent changes produce a conflict.
    // 编辑中的草稿保留原版本，使并发修改产生明确冲突。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query.data])

  const change = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error('配置尚未加载')
      return apiRequest('/api/v1/settings', {
        method: 'PATCH',
        body: {
          Version: version,
          Sections: {
            Context: { HistoryLimit: draft.context.historyLimit },
            Intent: {
              Enabled: draft.intent.enabled,
              ContinuationThreshold: draft.intent.continuationThreshold,
              InterjectionThreshold: draft.intent.interjectionThreshold,
              AddressedOtherMax: draft.intent.addressedOtherMax,
            },
          },
        },
      }).then(toSettingsView)
    },
    onSuccess: (view) => {
      const next = pickConfig(view.settings)
      baseline.current = next
      setDraft(next)
      setVersion(view.version)
      client.setQueryData(queryKeys.settings, view)
      toast.success('意图配置已保存')
    },
  })

  const rules = useMemo(
    () => new Map((effective.data?.builtinRules ?? []).map((rule) => [rule.ruleId, rule])),
    [effective.data],
  )
  const isRuleOff = (ruleId: string) => {
    const rule = rules.get(ruleId)
    return !!rule && (rule.status === 'not_applicable' || !rule.applicable)
  }
  const reasonFor = (ruleIds: string[]) =>
    ruleIds
      .filter(isRuleOff)
      .map((ruleId) => `「${rules.get(ruleId)?.title ?? ruleId}」`)
      .join('、')

  const continuationOff = isRuleOff('continuation') && isRuleOff('directed_to_bot')
  const interjectionOff = isRuleOff('interjection')
  const addressedOtherOff = isRuleOff('addressed_to_other')
  const configErrors = useMemo(() => {
    const errors: Record<string, string> = {}
    if (!draft) return errors
    if (!Number.isInteger(draft.context.historyLimit) || draft.context.historyLimit < 1 || draft.context.historyLimit > 200) {
      errors['Context.HistoryLimit'] = '请填写 1–200 的整数'
    }
    for (const [field, value] of [
      ['Intent.ContinuationThreshold', draft.intent.continuationThreshold],
      ['Intent.InterjectionThreshold', draft.intent.interjectionThreshold],
      ['Intent.AddressedOtherMax', draft.intent.addressedOtherMax],
    ] as const) {
      if (!Number.isFinite(value) || value <= 0 || value > 1) {
        errors[field] = '阈值必须在 0.001–1 之间'
      }
    }
    return errors
  }, [draft])
  const dirty = !!draft && serialize(draft) !== serialize(baseline.current)
  const hasConfigErrors = Object.keys(configErrors).length > 0
  useEditGuard(dirty, change.isPending)

  if (query.isError) {
    return (
      <FeedbackState
        variant="error"
        error={query.error}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!draft) return <p role="status">正在加载意图配置…</p>

  const updateIntent = (patch: Partial<SettingsIntent>) =>
    setDraft({ ...draft, intent: { ...draft.intent, ...patch } })

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (!dirty || change.isPending || hasConfigErrors) return
        change.mutate()
      }}
    >
      <div className="grid gap-4 lg:grid-cols-2">
      <SettingsCard
        id="intent-pipeline"
        title="回复决策流水线"
        description="关闭后只记录消息，不进行意图判断，也不会自动回复。"
        icon={Workflow}
        tag="全局"
      >
        <SwitchField
          id="intent-pipeline-enabled"
          label="回复决策流水线"
          description="控制消息是否进入回复判断。关闭后不会自动回复，也不会触发模型生成；消息仍按会话策略入库。"
          checked={draft.intent.enabled}
          onChange={(enabled) => updateIntent({ enabled })}
        />
      </SettingsCard>

      <SettingsCard
        id="intent-context"
        title="会话上下文"
        description="意图判断与生成使用同一份会话历史预算。"
        icon={History}
        tag="共享"
      >
        <NumberField
          id="intent-history-limit"
          label="历史条数上限"
          value={draft.context.historyLimit}
          onChange={(historyLimit) =>
            setDraft({ ...draft, context: { ...draft.context, historyLimit } })
          }
          unit="条"
          hint="默认 30，范围 1–200。按条数读取；只有会话策略允许读取历史时才会读取。"
          error={configErrors['Context.HistoryLimit']}
        />
      </SettingsCard>
      </div>

      <SettingsCard
        id="intent-group-thresholds"
        title="群聊高级设置"
        description="以下阈值只作用于群聊参与判断；私聊正常回复不受它们控制。"
        icon={SlidersHorizontal}
        tag="仅群聊"
        warning="0.80 / 0.95 / 0.60 是默认起点，不是准确率承诺。"
      >
        <div className="grid gap-x-6 sm:grid-cols-3">
          <ThresholdField
            id="intent-continuation-threshold"
            label="接续话题阈值"
            value={draft.intent.continuationThreshold}
            onChange={(continuationThreshold) => updateIntent({ continuationThreshold })}
            hint="同时用于「延续机器人话题」和「指向机器人」。升高会减少这两类回复；取值范围 0.001–1。"
            unavailableReason={
              continuationOff
                ? `全局默认规则${reasonFor(['continuation', 'directed_to_bot'])}已关闭；具体群聊覆盖时仍使用该阈值。`
                : undefined
            }
            error={configErrors['Intent.ContinuationThreshold']}
          />
          <ThresholdField
            id="intent-interjection-threshold"
            label="插话阈值"
            value={draft.intent.interjectionThreshold}
            onChange={(interjectionThreshold) => updateIntent({ interjectionThreshold })}
            hint="用于「自然插话」。升高会减少主动插话；取值范围 0.001–1。"
            unavailableReason={
              interjectionOff
                ? `全局默认规则${reasonFor(['interjection'])}已关闭；具体群聊覆盖时仍使用该阈值。`
                : undefined
            }
            error={configErrors['Intent.InterjectionThreshold']}
          />
          <ThresholdField
            id="intent-addressed-other-max"
            label="他人指向上限"
            value={draft.intent.addressedOtherMax}
            onChange={(addressedOtherMax) => updateIntent({ addressedOtherMax })}
            hint="用于「对他人说话优先抑制」。降低上限会更容易判定为对他人说话并抑制回复；取值范围 0.001–1。"
            unavailableReason={
              addressedOtherOff
                ? `全局默认规则${reasonFor(['addressed_to_other'])}已关闭；具体群聊覆盖时仍使用该阈值。`
                : undefined
            }
            error={configErrors['Intent.AddressedOtherMax']}
          />
        </div>
        <p className="mt-1 text-meta leading-relaxed text-muted-foreground">
          私聊正常回复按会话策略处理，不受以上三个群聊门槛控制。
        </p>
        {selectedBotId === undefined ? (
          <p className="mt-1 text-meta text-muted-foreground">
            选择机器人账号后，会显示全局默认规则开关；具体群聊覆盖时阈值仍可编辑。
          </p>
        ) : effective.isLoading ? (
          <p role="status" className="mt-1 text-meta text-muted-foreground">
            正在读取内置规则开关…
          </p>
        ) : effective.isError ? (
          <p role="alert" className="mt-1 text-meta text-destructive">
            读取内置规则开关失败，阈值暂时按可编辑展示。
          </p>
        ) : null}
      </SettingsCard>

      {change.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {change.error.message}
        </p>
      ) : null}

      <div className="sticky bottom-3 z-20 rounded-xl border border-border/80 bg-card/92 p-3 shadow-lg shadow-black/5 backdrop-blur">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0 text-meta text-muted-foreground" aria-live="polite">
            {hasConfigErrors ? '有配置项需要修正' : dirty ? '有未保存的修改' : '配置已保存'}
          </div>
          <Button type="submit" disabled={!dirty || change.isPending || hasConfigErrors}>
            {change.isPending ? '保存中…' : '保存配置'}
          </Button>
        </div>
      </div>
    </form>
  )
}

function ThresholdField({
  id,
  label,
  value,
  onChange,
  hint,
  unavailableReason,
  error,
}: {
  id: string
  label: string
  value: number
  onChange: (value: number) => void
  hint: string
  unavailableReason?: string
  error?: string
}) {
  return (
    <div className="py-2.5" data-field={id}>
      <div className="flex flex-wrap items-center gap-2">
        <Label htmlFor={`${id}-input`} className="text-sm">
          {label}
        </Label>
        {unavailableReason ? (
          <span className="rounded-full border border-warning/30 bg-warning/10 px-2 py-0.5 text-caption text-warning">
            不适用
          </span>
        ) : null}
      </div>
      <Input
        id={`${id}-input`}
        type="number"
        inputMode="decimal"
        required
        min={0.001}
        max={1}
        step="any"
        value={Number.isFinite(value) ? value : ''}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
        className="mt-1.5 font-mono tabular-nums"
        onChange={(event) => onChange(event.target.valueAsNumber)}
      />
      {unavailableReason ? (
        <p role="status" className="mt-1 text-meta leading-relaxed text-warning">
          {unavailableReason}
        </p>
      ) : null}
      {error ? (
        <p id={`${id}-error`} role="alert" className="mt-1 text-meta text-destructive">
          {error}
        </p>
      ) : (
        <p className="mt-1 text-meta leading-relaxed text-muted-foreground">{hint}</p>
      )}
    </div>
  )
}

export function IntentConfig() {
  return <IntentConfigForm />
}
