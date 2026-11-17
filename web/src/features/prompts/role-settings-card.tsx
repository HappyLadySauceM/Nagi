import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { UserRound } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { apiRequest } from '../../lib/api/client'
import { toSettingsView } from '../../lib/api/adapters/settings'
import { describeError } from '../../lib/errors'
import { queryKeys } from '../../lib/query-keys'
import { TextField } from '../settings/components/fields'
import { SettingsCard } from '../settings/components/settings-card'
import { useSystemSettings } from '../settings/queries'

/**
 * Global bot name shared by chat replies, intent judgment and memory extraction.
 * 全局机器人自称，供聊天回复、意图判断和记忆抽取统一渲染 {{bot_name}}。
 */
export function RoleSettingsCard() {
  const query = useSystemSettings()
  const client = useQueryClient()
  const [botName, setBotName] = useState('')
  const [baseline, setBaseline] = useState('')

  useEffect(() => {
    if (!query.data) return
    setBotName(query.data.settings.role.botName)
    setBaseline(query.data.settings.role.botName)
  }, [query.data])

  const save = useMutation({
    mutationFn: () => {
      if (!query.data) {
        return Promise.reject(new Error('角色设置尚未加载'))
      }
      return apiRequest('/api/v1/settings', {
        method: 'PATCH',
        body: {
          Version: query.data.version,
          Sections: { Role: { BotName: botName.trim() } },
        },
      }).then(toSettingsView)
    },
    onSuccess: (view) => {
      client.setQueryData(queryKeys.settings, view)
      const value = view.settings.role.botName
      setBaseline(value)
      setBotName(value)
      toast.success('角色设置已保存')
    },
  })

  const dirty = botName !== baseline

  return (
    <SettingsCard
      id="prompt-role-settings"
      title="角色设置"
      description="机器人自称会统一用于聊天回复、意图判断和记忆抽取。"
      icon={UserRound}
      tag="全局"
    >
      {query.isLoading ? (
        <p role="status" className="py-2 text-sm text-muted-foreground">
          正在读取角色设置…
        </p>
      ) : query.isError ? (
        <p role="alert" className="py-2 text-sm text-destructive">
          读取角色设置失败；{describeError(query.error).description}
        </p>
      ) : (
        <div className="space-y-2">
          <TextField
            id="prompt-bot-name"
            label="机器人自称"
            value={botName}
            onChange={setBotName}
            placeholder="例如 Nagi"
            hint="渲染 {{bot_name}} 时统一使用该值。"
          />
          <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
            <span className="text-meta text-muted-foreground" aria-live="polite">
              {dirty ? '有未保存的修改' : '已保存'}
            </span>
            <Button
              type="button"
              size="sm"
              disabled={!dirty || save.isPending}
              onClick={() => save.mutate()}
            >
              {save.isPending ? '保存中…' : '保存角色设置'}
            </Button>
          </div>
          {save.isError ? (
            <p role="alert" className="text-sm text-destructive">
              {describeError(save.error).description}
            </p>
          ) : null}
        </div>
      )}
    </SettingsCard>
  )
}
