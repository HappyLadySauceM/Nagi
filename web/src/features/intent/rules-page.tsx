import { useEffect, useMemo, useState } from 'react'
import { Plus, Save, Send, Trash2, Upload } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Input } from '../../components/ui/input'
import { Select } from '../../components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import {
  RULESET_STATUS_LABELS,
  RULE_ACTION_LABELS,
  type RuleEntry,
  type Ruleset,
  type ScopeType,
} from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { useAccountContext } from '../accounts/account-context'
import {
  useCreateDraft,
  usePublishRuleset,
  useRollbackRuleset,
  useRuleset,
  useRulesets,
  useSaveEntries,
  useSimulate,
} from './rules-queries'

const EMPTY_ENTRY: RuleEntry = {
  priority: 0,
  match: {},
  action: { type: 'silent', reasonCode: '' },
}

export function RulesPage() {
  const { selectedBotId } = useAccountContext()
  const [scopeType, setScopeType] = useState<ScopeType>('group')
  const [scopeId, setScopeId] = useState('')
  const scope = useMemo(() => {
    const parsed = Number(scopeId)
    if (!selectedBotId || !Number.isInteger(parsed) || parsed <= 0) return null
    return { botId: selectedBotId, scopeType, scopeId: parsed }
  }, [selectedBotId, scopeType, scopeId])

  const versions = useRulesets(scope)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const detail = useRuleset(selectedId)
  const [entries, setEntries] = useState<RuleEntry[]>([])
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    if (detail.data && !dirty) {
      setEntries(detail.data.entries)
    }
  }, [detail.data, dirty])

  // Hooks must run unconditionally; the placeholder scope keeps them inert
  // until the operator fills in a valid target.
  // Hook 必须无条件调用；占位作用域让它们在填写有效目标前保持惰性。
  const hookScope = scope ?? { botId: 0, scopeType, scopeId: 0 }
  const createDraft = useCreateDraft(hookScope)
  const save = useSaveEntries()
  const publish = usePublishRuleset()
  const rollback = useRollbackRuleset(hookScope)

  const current: Ruleset | undefined = detail.data
  const isDraft = current?.status === 'draft'

  function selectVersion(id: number) {
    setDirty(false)
    setSelectedId(id)
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="意图规则"
        description="按作用域维护版本化规则：草稿可编辑，发布后不可改，可回滚到历史版本；模拟只做求值，不会发送。"
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
            <Input
              value={scopeId}
              onChange={(e) => setScopeId(e.target.value)}
              placeholder="群号或 QQ 号"
              inputMode="numeric"
            />
          </label>
          <div className="flex items-end">
            <Button
              variant="outline"
              disabled={!scope || createDraft.isPending}
              onClick={() =>
                scope &&
                createDraft.mutate(
                  { note: '', cloneActive: true },
                  {
                    onSuccess: (set) => {
                      toast.success(`已创建 v${set.version} 草稿`)
                      selectVersion(set.id)
                    },
                    onError: (error) => toast.error(describeError(error).title),
                  },
                )
              }
            >
              <Plus />
              新建草稿
            </Button>
          </div>
        </div>
      </Card>

      {!scope ? (
        <FeedbackState variant="empty" title="先选择目标" description="填写群号或 QQ 号后即可查看该作用域的规则版本。" />
      ) : versions.isError ? (
        <FeedbackState variant="error" title="读取规则版本失败" error={versions.error} onRetry={() => void versions.refetch()} />
      ) : versions.data && versions.data.length === 0 ? (
        <FeedbackState
          variant="empty"
          title="还没有规则版本"
          description="新建草稿后可添加规则；未发布前仍沿用内置规则。"
        />
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>版本</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>备注</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {versions.data?.map((set) => (
                <TableRow key={set.id} className="cursor-pointer" onClick={() => selectVersion(set.id)}>
                  <TableCell>v{set.version}</TableCell>
                  <TableCell>
                    <Badge variant={set.status === 'published' ? 'success' : 'secondary'}>
                      {RULESET_STATUS_LABELS[set.status]}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-meta text-muted-foreground">{set.note || '—'}</TableCell>
                  <TableCell className="text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={rollback.isPending || set.status === 'published'}
                      onClick={(event) => {
                        event.stopPropagation()
                        rollback.mutate(set.version, {
                          onSuccess: () => toast.success(`已回滚到 v${set.version}`),
                          onError: (error) => toast.error(describeError(error).title),
                        })
                      }}
                    >
                      回滚到此版本
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      {current ? (
        <Card className="space-y-3 p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="text-sm font-medium">
              v{current.version} 规则条目
              {isDraft ? <span className="ml-2 text-meta text-muted-foreground">草稿可编辑</span> : null}
            </div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => { setEntries([...entries, { ...EMPTY_ENTRY, priority: entries.length }]); setDirty(true) }} disabled={!isDraft}>
                <Plus />
                添加条目
              </Button>
              <Button
                size="sm"
                disabled={!isDraft || !dirty || save.isPending}
                onClick={() =>
                  save.mutate({ id: current.id, entries }, {
                    onSuccess: () => { setDirty(false); toast.success('草稿已保存') },
                    onError: (error) => toast.error(describeError(error).title),
                  })
                }
              >
                <Save />
                保存
              </Button>
              <Button
                size="sm"
                disabled={!isDraft || publish.isPending}
                onClick={() =>
                  publish.mutate(current.id, {
                    onSuccess: () => toast.success(`已发布 v${current.version}`),
                    onError: (error) => toast.error(describeError(error).title),
                  })
                }
              >
                <Upload />
                发布
              </Button>
            </div>
          </div>

          {entries.length === 0 ? (
            <p className="text-meta text-muted-foreground">尚未添加条目；命中优先级最小的规则先生效。</p>
          ) : (
            <div className="space-y-2">
              {entries.map((entry, index) => (
                <div key={index} className="grid gap-2 rounded-md border border-border/60 p-2 sm:grid-cols-6">
                  <Input
                    value={String(entry.priority)}
                    disabled={!isDraft}
                    onChange={(e) => updateEntry(index, { priority: Number(e.target.value) || 0 })}
                    aria-label="优先级"
                  />
                  <Input
                    value={entry.match.textContains ?? ''}
                    disabled={!isDraft}
                    placeholder="包含文本"
                    onChange={(e) => updateEntry(index, { match: { ...entry.match, textContains: e.target.value || undefined } })}
                  />
                  <Select
                    value={entry.match.kind ?? ''}
                    disabled={!isDraft}
                    onChange={(e) => updateEntry(index, { match: { ...entry.match, kind: e.target.value || undefined } })}
                  >
                    <option value="">任意会话</option>
                    <option value="group">仅群聊</option>
                    <option value="private">仅私聊</option>
                  </Select>
                  <Select
                    value={entry.action.type}
                    disabled={!isDraft}
                    onChange={(e) => updateEntry(index, { action: { ...entry.action, type: e.target.value as RuleEntry['action']['type'] } })}
                  >
                    {Object.entries(RULE_ACTION_LABELS).map(([value, label]) => (
                      <option key={value} value={value}>{label}</option>
                    ))}
                  </Select>
                  <Input
                    value={entry.action.reasonCode}
                    disabled={!isDraft}
                    placeholder="原因码"
                    onChange={(e) => updateEntry(index, { action: { ...entry.action, reasonCode: e.target.value } })}
                  />
                  <Button
                    size="icon"
                    variant="ghost"
                    disabled={!isDraft}
                    onClick={() => { setEntries(entries.filter((_, i) => i !== index)); setDirty(true) }}
                    aria-label="删除条目"
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </Card>
      ) : null}

      {scope ? <SimulateCard scope={scope} /> : null}
    </div>
  )

  function updateEntry(index: number, patch: Partial<RuleEntry>) {
    setEntries((prev) => prev.map((entry, i) => (i === index ? { ...entry, ...patch } : entry)))
    setDirty(true)
  }
}

function SimulateCard({ scope }: { scope: { botId: number; scopeType: ScopeType; scopeId: number } }) {
  const simulate = useSimulate(scope)
  const [text, setText] = useState('')
  const [explicit, setExplicit] = useState(false)
  return (
    <Card className="space-y-3 p-4">
      <div className="text-sm font-medium">只读模拟</div>
      <div className="grid gap-2 sm:grid-cols-4">
        <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="输入一条消息" className="sm:col-span-2" />
        <Select value={explicit ? 'explicit' : 'plain'} onChange={(e) => setExplicit(e.target.value === 'explicit')}>
          <option value="plain">未被指向</option>
          <option value="explicit">明确指向机器人</option>
        </Select>
        <Button
          disabled={!text || simulate.isPending}
          onClick={() =>
            simulate.mutate(
              { kind: scope.scopeType === 'group' ? 'group' : 'private', replyMode: 'intent_based', explicit, text },
              { onError: (error) => toast.error(describeError(error).title) },
            )
          }
        >
          <Send />
          模拟
        </Button>
      </div>
      {simulate.data ? (
        <div className="rounded-md bg-muted/40 p-3 text-meta">
          <div>结果：{simulate.data.blocked ? '被拦截' : simulate.data.forceReply ? '强制回复' : simulate.data.needSemantic ? '需要语义判定' : '走内置策略'}</div>
          <div>原因码：{simulate.data.reasonCode || '—'}</div>
          <div>规则版本：{simulate.data.rulesetVersion || '内置规则'}</div>
        </div>
      ) : null}
    </Card>
  )
}
