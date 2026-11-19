import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { History, Workflow } from 'lucide-react'
import { toast } from 'sonner'
import { useSystemSettings } from '../settings/queries'
import { SettingsCard } from '../settings/components/settings-card'
import { NumberField, SwitchField } from '../settings/components/fields'
import { Button } from '../../components/ui/button'
import { FeedbackState } from '../../components/feedback-state'
import { apiRequest } from '../../lib/api/client'
import { toSettingsView } from '../../lib/api/adapters/settings'
import { queryKeys } from '../../lib/query-keys'
import type {
  SettingsContext,
  SettingsIntent,
  SystemSettings,
} from '../../lib/api/contracts'
import { useEditGuard } from './edit-guard'

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
              TopicContinuationEnabled: draft.intent.topicContinuationEnabled,
              TopicIdleSeconds: draft.intent.topicIdleSeconds,
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

  const configErrors = useMemo(() => {
    const errors: Record<string, string> = {}
    if (!draft) return errors
    if (!Number.isInteger(draft.context.historyLimit) || draft.context.historyLimit < 1 || draft.context.historyLimit > 200) {
      errors['Context.HistoryLimit'] = '请填写 1–200 的整数'
    }
    if (!Number.isInteger(draft.intent.topicIdleSeconds) || draft.intent.topicIdleSeconds < 30 || draft.intent.topicIdleSeconds > 3600) {
      errors['Intent.TopicIdleSeconds'] = '请填写 30–3600 的整数'
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
      <SettingsCard
        id="intent-group-topics"
        title="群聊话题续接"
        description="让多个群友在同一个话题中自然追问或补充。"
        icon={Workflow}
        tag="群聊"
      >
        <SwitchField
          id="intent-group-topics-enabled"
          label="群聊话题续接"
          description="适用于允许读取历史、按意图回复的群。"
          checked={draft.intent.topicContinuationEnabled}
          onChange={(topicContinuationEnabled) => updateIntent({ topicContinuationEnabled })}
        />
        <NumberField
          id="intent-topic-idle-seconds"
          label="话题空闲到期"
          value={draft.intent.topicIdleSeconds}
          onChange={(topicIdleSeconds) => updateIntent({ topicIdleSeconds })}
          unit="秒"
          hint="默认 300 秒，范围 30–3600。有效追问或补充会续期。"
          error={configErrors['Intent.TopicIdleSeconds']}
        />
      </SettingsCard>
      </div>

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

export function IntentConfig() {
  return <IntentConfigForm />
}
