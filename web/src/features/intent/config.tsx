import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Cpu, History, SlidersHorizontal, UserRound, Workflow } from 'lucide-react'
import { toast } from 'sonner'
import { useAccountContext } from '../accounts/account-context'
import { useModels, modelReady } from '../settings/models'
import { useSystemSettings } from '../settings/queries'
import { SettingsCard } from '../settings/components/settings-card'
import { NumberField, TextField, SwitchField } from '../settings/components/fields'
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
  SettingsRole,
  SystemSettings,
} from '../../lib/api/contracts'
import { useEditGuard } from './edit-guard'
import { useEffectiveRules } from './rules-queries'

interface ConfigDraft {
  role: SettingsRole
  context: SettingsContext
  intent: SettingsIntent
}

function pickConfig(settings: SystemSettings): ConfigDraft {
  return {
    role: { ...settings.role },
    context: { ...settings.context },
    intent: { ...settings.intent },
  }
}

function serialize(draft: ConfigDraft | null): string {
  return draft ? JSON.stringify(draft) : ''
}

function IntentConfigForm() {
  const query = useSystemSettings()
  const models = useModels()
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
            Role: { BotName: draft.role.botName },
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
  const intentModel = models.data?.Catalog.Profiles.find(
    (profile) => profile.ID === models.data?.Catalog.Defaults.intent,
  )
  const dirty = !!draft && serialize(draft) !== serialize(baseline.current)
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
      className="max-w-3xl space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (!dirty || change.isPending) return
        change.mutate()
      }}
    >
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
        id="intent-role"
        title="角色设置"
        description="机器人身份会同时用于意图判断与回复生成。"
        icon={UserRound}
        tag="共享"
      >
        <TextField
          id="intent-bot-name"
          label="机器人自称"
          value={draft.role.botName}
          onChange={(botName) => setDraft({ ...draft, role: { botName } })}
          placeholder="例如 Nagi"
          hint="同时用于意图判断（消息是否指向机器人）和回复生成（回复中的自称）。"
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
        />
      </SettingsCard>

      <SettingsCard
        id="intent-model"
        title="意图模型"
        description="模型连接、地址、超时与凭证由模型目录统一维护。"
        icon={Cpu}
        tag="模型目录"
      >
        {models.isLoading ? (
          <p role="status" className="py-2 text-sm text-muted-foreground">
            正在读取模型目录…
          </p>
        ) : models.isError ? (
          <p role="alert" className="py-2 text-sm text-destructive">
            读取模型目录失败，请到系统配置检查。
          </p>
        ) : (
          <div className="rounded-lg border border-border bg-secondary/20 p-3">
            <p className="text-sm font-medium">
              {intentModel ? intentModel.Name : '未选择默认意图模型'}
            </p>
            <p className="mt-1 text-meta text-muted-foreground">
              {intentModel
                ? modelReady(models.data?.Catalog, 'intent')
                  ? '已配置并测试通过。'
                  : '模型已选择，但尚未通过连接测试。'
                : '语义判断将降级为规则判断。'}
            </p>
            <Link className="mt-2 inline-block text-sm underline" to="/settings">
              前往模型配置
            </Link>
          </div>
        )}
        <p className="mt-2 text-meta leading-relaxed text-muted-foreground">
          本页不再重复维护接口地址、模型名或请求超时；这些字段只以模型目录为唯一来源。
        </p>
      </SettingsCard>

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
                ? `内置规则${reasonFor(['continuation', 'directed_to_bot'])}已关闭`
                : undefined
            }
          />
          <ThresholdField
            id="intent-interjection-threshold"
            label="插话阈值"
            value={draft.intent.interjectionThreshold}
            onChange={(interjectionThreshold) => updateIntent({ interjectionThreshold })}
            hint="用于「自然插话」。升高会减少主动插话；取值范围 0.001–1。"
            unavailableReason={
              interjectionOff ? `内置规则${reasonFor(['interjection'])}已关闭` : undefined
            }
          />
          <ThresholdField
            id="intent-addressed-other-max"
            label="他人指向上限"
            value={draft.intent.addressedOtherMax}
            onChange={(addressedOtherMax) => updateIntent({ addressedOtherMax })}
            hint="用于「对他人说话优先抑制」。降低上限会更容易判定为对他人说话并抑制回复；取值范围 0.001–1。"
            unavailableReason={
              addressedOtherOff
                ? `内置规则${reasonFor(['addressed_to_other'])}已关闭`
                : undefined
            }
          />
        </div>
        <p className="mt-1 text-meta leading-relaxed text-muted-foreground">
          私聊正常回复按会话策略处理，不受以上三个群聊门槛控制。
        </p>
        {selectedBotId === undefined ? (
          <p className="mt-1 text-meta text-muted-foreground">
            选择机器人账号后，会根据该账号的内置规则开关显示「不适用」。
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
            {dirty ? '有未保存的修改' : '配置已保存'}
          </div>
          <Button type="submit" disabled={!dirty || change.isPending}>
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
}: {
  id: string
  label: string
  value: number
  onChange: (value: number) => void
  hint: string
  unavailableReason?: string
}) {
  return (
    <div className="py-2.5" data-field={id}>
      <Label htmlFor={`${id}-input`} className="text-sm">
        {label}
      </Label>
      {unavailableReason ? (
        <div
          id={`${id}-input`}
          role="status"
          className="mt-1.5 rounded-md border border-dashed border-border bg-secondary/30 px-3 py-2 text-sm text-muted-foreground"
        >
          <span className="font-medium text-foreground">不适用</span>
          <span className="ml-2">{unavailableReason}</span>
        </div>
      ) : (
        <Input
          id={`${id}-input`}
          type="number"
          inputMode="decimal"
          required
          min={0.001}
          max={1}
          step="any"
          value={Number.isFinite(value) ? value : ''}
          className="mt-1.5 font-mono tabular-nums"
          onChange={(event) => onChange(event.target.valueAsNumber)}
        />
      )}
      <p className="mt-1 text-meta leading-relaxed text-muted-foreground">{hint}</p>
    </div>
  )
}

export function IntentConfig() {
  return <IntentConfigForm />
}
