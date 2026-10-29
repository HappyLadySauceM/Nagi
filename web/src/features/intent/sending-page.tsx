import { useEffect, useMemo, useState } from 'react'
import { Save } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Input } from '../../components/ui/input'
import { Select } from '../../components/ui/select'
import { Switch } from '../../components/ui/switch'
import type { ScopeType } from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { useAccountContext } from '../accounts/account-context'
import { useSaveSendingPolicy, useSendingPolicy } from './sending-queries'

export function SendingPage() {
  const { selectedBotId } = useAccountContext()
  const [scopeType, setScopeType] = useState<ScopeType>('group')
  const [scopeId, setScopeId] = useState('')
  const scope = useMemo(() => {
    const parsed = Number(scopeId)
    if (!selectedBotId || !Number.isInteger(parsed) || parsed <= 0) return null
    return { botId: selectedBotId, scopeType, scopeId: parsed }
  }, [selectedBotId, scopeType, scopeId])

  const policy = useSendingPolicy(scope)
  const save = useSaveSendingPolicy()

  const [allow, setAllow] = useState(false)
  const [maxMessages, setMaxMessages] = useState('3')
  const [maxCharsEach, setMaxCharsEach] = useState('500')
  const [maxCharsTotal, setMaxCharsTotal] = useState('1500')
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    if (policy.data && !dirty) {
      setAllow(policy.data.allowSendCurrent)
      setMaxMessages(String(policy.data.maxMessagesPerRun))
      setMaxCharsEach(String(policy.data.maxCharsPerMessage))
      setMaxCharsTotal(String(policy.data.maxTotalCharsPerRun))
    }
  }, [policy.data, dirty])

  const numbersValid =
    Number(maxMessages) > 0 && Number(maxCharsEach) > 0 && Number(maxCharsTotal) > 0 &&
    Number(maxCharsEach) <= Number(maxCharsTotal)

  return (
    <div className="space-y-4">
      <PageHeader
        title="发送策略"
        description="按作用域灰度自主发送：只有开启后，模型才看得到 qq.send_current；三条上限决定一次运行最多发几条、每条与整轮的字数。"
      />

      <Card className="p-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <label className="space-y-1 text-meta">
            <span className="text-muted-foreground">会话类型</span>
            <Select value={scopeType} onChange={(e) => setScopeType(e.target.value as ScopeType)}>
              <option value="group">群聊</option>
              <option value="private">私聊</option>
            </Select>
          </label>
          <label className="space-y-1 text-meta">
            <span className="text-muted-foreground">目标号码</span>
            <Input value={scopeId} onChange={(e) => setScopeId(e.target.value)} placeholder="群号或 QQ 号" inputMode="numeric" />
          </label>
        </div>
      </Card>

      {!scope ? (
        <FeedbackState variant="empty" title="先选择目标" description="填写群号或 QQ 号后即可查看并调整该作用域的发送策略。" />
      ) : policy.isError ? (
        <FeedbackState variant="error" title="读取发送策略失败" error={policy.error} onRetry={() => void policy.refetch()} />
      ) : (
        <Card className="space-y-4 p-4">
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm font-medium">允许自主发送</div>
              <p className="mt-1 text-meta text-muted-foreground">
                {policy.data?.exists ? '该作用域已有独立策略。' : '尚未保存策略，当前沿用全局默认预算且不允许自主发送。'}
              </p>
            </div>
            <Switch checked={allow} onCheckedChange={(next) => { setAllow(next); setDirty(true) }} aria-label="允许自主发送" />
          </div>

          <div className="grid gap-3 sm:grid-cols-3">
            <label className="space-y-1 text-meta">
              <span className="text-muted-foreground">单轮最多条数</span>
              <Input value={maxMessages} onChange={(e) => { setMaxMessages(e.target.value); setDirty(true) }} inputMode="numeric" />
            </label>
            <label className="space-y-1 text-meta">
              <span className="text-muted-foreground">单条字数上限</span>
              <Input value={maxCharsEach} onChange={(e) => { setMaxCharsEach(e.target.value); setDirty(true) }} inputMode="numeric" />
            </label>
            <label className="space-y-1 text-meta">
              <span className="text-muted-foreground">整轮字数上限</span>
              <Input value={maxCharsTotal} onChange={(e) => { setMaxCharsTotal(e.target.value); setDirty(true) }} inputMode="numeric" />
            </label>
          </div>
          {!numbersValid ? (
            <p className="text-meta text-warning">三个上限都必须为正数，且单条上限不能大于整轮上限。</p>
          ) : null}

          <div className="flex justify-end">
            <Button
              disabled={!numbersValid || save.isPending || !dirty}
              onClick={() =>
                scope &&
                save.mutate(
                  {
                    botId: scope.botId,
                    scopeType: scope.scopeType,
                    scopeId: scope.scopeId,
                    allowSendCurrent: allow,
                    maxMessagesPerRun: Number(maxMessages),
                    maxCharsPerMessage: Number(maxCharsEach),
                    maxTotalCharsPerRun: Number(maxCharsTotal),
                  },
                  {
                    onSuccess: () => { setDirty(false); toast.success('发送策略已保存') },
                    onError: (error) => toast.error(describeError(error).title),
                  },
                )
              }
            >
              <Save />
              保存
            </Button>
          </div>
        </Card>
      )}
    </div>
  )
}
