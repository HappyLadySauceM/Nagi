import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useSystemSettings } from '../settings/queries'
import { apiRequest } from '../../lib/api/client'
import { toSettingsView } from '../../lib/api/adapters/settings'
import { queryKeys } from '../../lib/query-keys'
import type { SettingsIntent } from '../../lib/api/contracts'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { FeedbackState } from '../../components/feedback-state'
import { useEditGuard } from './edit-guard'

export function IntentConfig() {
  const query = useSystemSettings()
  const client = useQueryClient()
  const [draft, setDraft] = useState<SettingsIntent | null>(null)
  const [version, setVersion] = useState(0)
  const baseline = useRef<SettingsIntent | null>(null)
  useEffect(() => {
    if (
      query.data &&
      (!draft || JSON.stringify(draft) === JSON.stringify(baseline.current))
    ) {
      baseline.current = { ...query.data.settings.intent }
      setDraft({ ...query.data.settings.intent })
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
            Intent: {
              Enabled: draft.enabled,
              Provider: draft.provider,
              Endpoint: draft.endpoint,
              Model: draft.model,
              BotName: draft.botName,
              TimeoutSeconds: draft.timeoutSeconds,
              HistoryLimit: draft.historyLimit,
              ContinuationThreshold: draft.continuationThreshold,
              InterjectionThreshold: draft.interjectionThreshold,
              AddressedOtherMax: draft.addressedOtherMax,
            },
          },
        },
      }).then(toSettingsView)
    },
    onSuccess: (view) => {
      baseline.current = { ...view.settings.intent }
      setDraft({ ...view.settings.intent })
      setVersion(view.version)
      client.setQueryData(queryKeys.settings, view)
    },
  })
  const dirty =
    !!draft && JSON.stringify(draft) !== JSON.stringify(baseline.current)
  useEditGuard(dirty, change.isPending)
  if (query.isError)
    return (
      <FeedbackState
        variant="error"
        error={query.error}
        onRetry={() => void query.refetch()}
      />
    )
  if (!draft) return <p role="status">正在加载意图配置…</p>
  const patch = (p: Partial<SettingsIntent>) => setDraft({ ...draft, ...p })
  return (
    <form
      className="max-w-2xl space-y-5"
      onSubmit={(e) => {
        e.preventDefault()
        change.mutate()
      }}
    >
      <h2 className="font-medium">意图识别配置</h2>
      <p className="text-sm text-muted-foreground">
        全局生效。模型接口和凭证在系统配置的“模型配置”中管理。
      </p>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={draft.enabled}
          onChange={(e) => patch({ enabled: e.target.checked })}
        />
        启用意图识别
      </label>
      <label className="block space-y-1 text-sm">
        机器人自称
        <Input
          value={draft.botName}
          onChange={(e) => patch({ botName: e.target.value })}
        />
      </label>
      <label className="block space-y-1 text-sm">
        上下文条数
        <Input
          type="number"
          min={1}
          max={200}
          required
          value={draft.historyLimit}
          onChange={(e) => patch({ historyLimit: Number(e.target.value) })}
        />
      </label>
      {(
        [
          ['continuationThreshold', '接续话题阈值'],
          ['interjectionThreshold', '插话阈值'],
          ['addressedOtherMax', '他人指向上限'],
        ] as const
      ).map(([key, label]) => (
        <label key={key} className="block space-y-1 text-sm">
          {label}
          <Input
            type="number"
            required
            min={0.001}
            max={1}
            step="any"
            value={draft[key]}
            onChange={(e) => patch({ [key]: Number(e.target.value) })}
          />
        </label>
      ))}
      {change.isError ? (
        <p role="alert" className="text-destructive">
          {change.error.message}
        </p>
      ) : null}
      <Button disabled={!dirty || change.isPending}>
        {change.isPending ? '保存中…' : '保存意图配置'}
      </Button>
    </form>
  )
}
