import { ConfirmDialog } from '../../components/confirm-dialog'
import { Sheet, SheetContent, SheetTitle, SheetDescription } from '../../components/ui/sheet'
import { useEditGuard } from './edit-guard'
import { useEffect, useMemo, useState } from 'react'
import { Plus, Save, Send, Trash2, Upload } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Input } from '../../components/ui/input'
import { Switch } from '../../components/ui/switch'
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
import { ApiError } from '../../lib/api/client'
import { useContacts, useGroups } from '../directory/queries'
import { useAccountContext } from '../accounts/account-context'
import {
  useCreateDraft,
  useDeleteRuleset,
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

export function RulesPage({ embedded = false }: { embedded?: boolean }) {
  const { selectedBotId } = useAccountContext()
  return <RulesEditor key={selectedBotId ?? 'none'} embedded={embedded} />
}

function RulesEditor({ embedded }: { embedded: boolean }) {
  const { selectedBotId } = useAccountContext()
  const [scopeType, setScopeType] = useState<ScopeType>(() => new URLSearchParams(location.search).get('scope_type') === 'private' ? 'private' : 'group')
  const [scopeId, setScopeId] = useState(() => new URLSearchParams(location.search).get('scope_id') ?? '0')
  useEffect(() => {
    if (embedded) return
    const url = new URL(window.location.href)
    url.searchParams.set('scope_type', scopeType)
    if (scopeId) url.searchParams.set('scope_id', scopeId)
    else url.searchParams.delete('scope_id')
    window.history.replaceState(window.history.state, '', url)
  }, [embedded, scopeType, scopeId])
  const scope = useMemo(() => {
    const parsed = Number(scopeId)
    if (!selectedBotId || !Number.isInteger(parsed) || parsed < 0) return null
    return { botId: selectedBotId, scopeType, scopeId: parsed }
  }, [selectedBotId, scopeType, scopeId])

  const versions = useRulesets(selectedBotId ? { botId: selectedBotId, scopeType: 'group', scopeId: -1 } : null)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const detail = useRuleset(selectedId)
  const [entries, setEntries] = useState<RuleEntry[]>([])
  const [dirty, setDirty] = useState(false)
  const [builtinRules, setBuiltinRules] = useState<Record<string, boolean>>({})
  const [targetText, setTargetText] = useState('')
  const [allTargets, setAllTargets] = useState(true)
  const [targetSearch,setTargetSearch]=useState('')
  const [targetKind,setTargetKind]=useState<ScopeType>('private')

  useEffect(() => {
    if (detail.data && !dirty) {
      setEntries(detail.data.entries)
      setBuiltinRules(detail.data.builtinRules ?? {})
      setAllTargets(!detail.data.targets?.length)
      setTargetText((detail.data.targets ?? []).map(target => `${target.kind}:${target.id}`).join(', '))
    }
  }, [detail.data, dirty])

  // Hooks must run unconditionally; the placeholder scope keeps them inert
  // until the operator fills in a valid target.
  // Hook 必须无条件调用；占位作用域让它们在填写有效目标前保持惰性。
  const current: Ruleset | undefined = detail.data && detail.data.botId === selectedBotId ? detail.data : undefined
  const hookScope = (current ? {botId:current.botId,scopeType:current.scopeType,scopeId:current.scopeId} : scope) ?? { botId: 0, scopeType, scopeId: 0 }
  const remove = useDeleteRuleset()
  const [deleting, setDeleting] = useState<Ruleset | null>(null)
  const [editorView, setEditorView] = useState('edit')
  const createDraft = useCreateDraft(hookScope)
  const save = useSaveEntries()
  const publish = usePublishRuleset()
  const rollback = useRollbackRuleset()

  const globalBuiltinRules = versions.data?.find(set => set.status === 'published' && !set.targets?.length)?.builtinRules ?? {}
  const isDraft = current?.status === 'draft'
  const matchingFriends=useContacts(selectedBotId,{search:targetSearch,page:1,limit:20},!allTargets && isDraft && targetKind==='private')
  const matchingGroups=useGroups(selectedBotId,{search:targetSearch,page:1,limit:20},!allTargets && isDraft && targetKind==='group')
  const targetQuery=targetKind==='private'?matchingFriends:matchingGroups
  const choices=targetKind==='private'?(matchingFriends.data?.items ?? []).map(row=>({id:row.userId,name:row.remark||row.nickname})):(matchingGroups.data?.items ?? []).map(row=>({id:row.groupId,name:row.name}))

  function selectVersion(id: number) {
    if (save.isPending || publish.isPending || rollback.isPending || createDraft.isPending || remove.isPending || (dirty && !window.confirm('放弃未保存的修改并切换版本？'))) return
    setDirty(false)
    setSelectedId(id)
    setEditorView('edit')
  }

  const busy = remove.isPending || save.isPending || publish.isPending || rollback.isPending || createDraft.isPending
  useEditGuard(dirty, busy)
  function changeTarget(change: () => void) {
    if (busy || (dirty && !window.confirm('放弃未保存的修改并切换目标？'))) return
    setDirty(false)
    setSelectedId(null); setEntries([]);
    change()
  }

  return (
    <div className="space-y-5">
      {!embedded ? <PageHeader
        title="规则管理"
        description="指定适用会话并维护版本化规则：草稿可编辑，发布后不可改，可回滚到历史版本；模拟只做求值，不会发送。"
        className="xl:col-span-2"
      /> : null}

      <Card className="p-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <label className="space-y-1 text-sm">
            <span className="text-muted-foreground">会话类型</span>
            <Select value={scopeType} disabled={busy} onChange={(e) => changeTarget(() => setScopeType(e.target.value as ScopeType))}>
              <option value="group">群聊</option>
              <option value="private">私聊</option>
            </Select>
          </label>
          <label className="space-y-1 text-sm">
            <span className="text-muted-foreground">新建规则的初始目标 · 0 为所有会话</span>
            <Input
              value={scopeId}
              disabled={busy} onChange={(e) => changeTarget(() => setScopeId(e.target.value))}
              placeholder="0 表示所有会话"
              inputMode="numeric"
            />
          </label>
          <div className="flex items-end">
            <Button
              variant="outline"
              disabled={!scope || busy}
              onClick={() =>
                scope && !busy && (!dirty || window.confirm('放弃未保存的修改并新建草稿？')) &&
                createDraft.mutate(
                  { note: '', cloneActive: true, cloneFromId: current?.id },
                  {
                    onSuccess: (set) => {
                      toast.success(`已创建 v${set.version} 草稿`)
                      setDirty(false)
                      setSelectedId(set.id)
                      setEditorView('edit')
                    },
                    onError: (error) => toast.error(describeError(error).title),
                  },
                )
              }
            >
              <Plus />
              {current ? '从此版本创建草稿' : '新建规则草稿'}
            </Button>
          </div>
        </div>
      </Card>

      {!selectedBotId ? (
        <FeedbackState variant="empty" title="先选择机器人账号" description="规则属于当前账号。" />
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
                <TableHead>适用范围／备注</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {versions.data?.map((set) => (
                <TableRow key={set.id} className="cursor-pointer" onClick={() => selectVersion(set.id)}>
                  <TableCell><button type="button" className="font-medium hover:underline" onClick={event => { event.stopPropagation(); selectVersion(set.id) }}>v{set.version}</button></TableCell>
                  <TableCell>
                    <Badge variant={set.status === 'published' ? 'success' : 'secondary'}>
                      {RULESET_STATUS_LABELS[set.status]}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-meta text-muted-foreground">{set.targets?.length ? set.targets.map(target => `${target.kind === 'private' ? '好友' : '群聊'} ${target.id}`).join('、') : '所有会话'} · {set.note || '—'}</TableCell>
                  <TableCell className="text-right">
                    <Button variant="ghost" disabled={busy} onClick={event => { event.stopPropagation(); setDeleting(set) }} aria-label={`删除规则 v${set.version}`}><Trash2 /></Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={busy || dirty || set.status !== 'archived'}
                      onClick={(event) => {
                        event.stopPropagation()
                        rollback.mutate({ version: set.version, scope: { botId: set.botId, scopeType: set.scopeType, scopeId: set.scopeId } }, {
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

      <Sheet open={selectedId !== null} onOpenChange={open => {
        if (open || busy || (dirty && !window.confirm('放弃未保存的修改并关闭？'))) return
        setDirty(false)
        setSelectedId(null)
        setEditorView('edit')
      }}>
        <SheetContent side="right" className="h-dvh w-full gap-0 overflow-hidden bg-background p-0 lg:w-[calc(100%-2rem)] lg:max-w-[1600px]">
          <header className="shrink-0 border-b border-border px-6 py-5 pr-14">
            <SheetTitle className="text-lg font-semibold">规则编辑{current ? ` · v${current.version}` : ''}</SheetTitle>
            <SheetDescription className="mt-2">草稿可编辑；发布及历史版本只读，可从当前版本创建草稿。</SheetDescription>
          </header>
          <div className="flex shrink-0 gap-2 border-b p-3 lg:hidden"><Button variant={editorView === 'edit' ? 'secondary' : 'ghost'} aria-current={editorView === 'edit' ? 'page' : undefined} onClick={() => setEditorView('edit')}>规则编辑</Button><Button variant={editorView === 'test' ? 'secondary' : 'ghost'} aria-current={editorView === 'test' ? 'page' : undefined} onClick={() => setEditorView('test')}>编辑测试</Button></div>
          <div className="grid min-h-0 flex-1 lg:grid-cols-[3fr_2fr]">
          <div className={`min-h-0 overflow-y-auto p-5 sm:p-6 ${editorView === 'edit' ? '' : 'hidden lg:block'}`}>


      {detail.isLoading && selectedId ? <p role="status">正在读取规则详情…</p> : null}
      {detail.isError ? <FeedbackState variant="error" title="读取规则详情失败" error={detail.error} onRetry={() => void detail.refetch()} /> : null}
      {current ? (
        <div className="min-w-0 space-y-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="text-sm font-medium">
              v{current.version} 规则条目
              {isDraft ? <span className="ml-2 text-meta text-muted-foreground">草稿可编辑</span> : null}
            </div>

          </div>

          <div className="space-y-3 rounded-lg border border-border bg-muted/30 p-4">
            <label className="flex items-center justify-between gap-3"><span>适用于所有会话</span><Switch checked={allTargets} disabled={!isDraft || busy} onCheckedChange={value => { setAllTargets(value); setDirty(true) }} /></label>
            {!allTargets ? <label className="block space-y-1 text-sm"><span>指定会话（逗号分隔，例如 private:1552089234, group:123456）</span><Input aria-label="规则适用会话" value={targetText} disabled={!isDraft || busy} onChange={event => { setTargetText(event.target.value); setDirty(true) }} /></label> : null}
            {!allTargets && isDraft ? <div className="space-y-2">
              <div className="flex gap-2"><Select aria-label="搜索目标类型" value={targetKind} onChange={event=>setTargetKind(event.target.value as ScopeType)}><option value="private">好友</option><option value="group">群聊</option></Select><Input aria-label="搜索规则目标" placeholder="搜索昵称、备注或号码" value={targetSearch} onChange={event=>setTargetSearch(event.target.value)}/></div>
              {targetQuery.isError?<FeedbackState variant="error" error={targetQuery.error} onRetry={()=>void targetQuery.refetch()} compact/>:targetQuery.isFetching?<p className="text-xs">搜索中…</p>:<div className="flex max-h-32 flex-wrap gap-2 overflow-y-auto">{choices.map(row=><Button type="button" size="sm" variant="outline" key={row.id} onClick={()=>{const value=`${targetKind}:${row.id}`;setTargetText(old=>[...new Set([...old.split(/[,，\s]+/).filter(Boolean),value])].join(', '));setDirty(true)}}>{row.name || '未命名'} · {row.id}</Button>)}</div>}
            </div>:null}
            <p className="text-xs text-muted-foreground">指定会话规则优先；未命中再执行全局规则。发布时检查目标冲突。</p>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            {Object.entries({ closing: '告别时保持安静', continuation: '延续机器人话题', directed_to_bot: '识别对机器人发言', addressed_to_other: '避免打断其他人', interjection: '判断是否适合插话' }).map(([key,label]) => <label key={key} className="flex items-center justify-between gap-3 rounded-lg border border-border p-3"><span className="text-sm">{label}{builtinRules[key] === undefined ? <span className="ml-2 text-xs text-muted-foreground">{allTargets ? '内置默认' : '继承全局'}</span> : null}</span><Switch aria-label={label} checked={builtinRules[key] ?? (!allTargets ? globalBuiltinRules[key] : undefined) ?? true} disabled={!isDraft || busy} onCheckedChange={value => { setBuiltinRules(old => ({...old,[key]:value})); setDirty(true) }} /></label>)}
          </div>
          <p className="text-xs text-muted-foreground">参与授权、观察模式、回复模式、冷却和小时额度始终生效。</p>

          {entries.length === 0 ? (
            <p className="text-meta text-muted-foreground">尚未添加条目；命中优先级最小的规则先生效。</p>
          ) : (
            <div className="space-y-2">
              {entries.map((entry, index) => (
                <div key={index} className="grid gap-2 rounded-md border border-border/60 p-3 sm:grid-cols-2 2xl:grid-cols-3">
                  <label className="flex items-center gap-2 text-sm"><Switch aria-label="启用规则条目" checked={entry.enabled ?? true} disabled={!isDraft || busy} onCheckedChange={enabled => updateEntry(index, { enabled })} />启用</label>
                  <Input
                    value={String(entry.priority)}
                    disabled={!isDraft || busy}
                    onChange={(e) => updateEntry(index, { priority: Number(e.target.value) || 0 })}
                    aria-label="优先级"
                  />
                  <Input
                    value={entry.match.textContains ?? ''}
                    disabled={!isDraft || busy}
                    aria-label="包含文本"
                    placeholder="包含文本"
                    onChange={(e) => updateEntry(index, { match: { ...entry.match, textContains: e.target.value || undefined } })}
                  />
                  <Select
                    aria-label="规则会话类型"
                    value={entry.match.kind ?? ''}
                    disabled={!isDraft || busy}
                    onChange={(e) => updateEntry(index, { match: { ...entry.match, kind: e.target.value || undefined } })}
                  >
                    <option value="">任意会话</option>
                    <option value="group">仅群聊</option>
                    <option value="private">仅私聊</option>
                  </Select>
                  <Select
                    aria-label="规则动作"
                    value={entry.action.type}
                    disabled={!isDraft || busy}
                    onChange={(e) => updateEntry(index, { action: { ...entry.action, type: e.target.value as RuleEntry['action']['type'] } })}
                  >
                    {Object.entries(RULE_ACTION_LABELS).map(([value, label]) => (
                      <option key={value} value={value}>{label}</option>
                    ))}
                  </Select>
                  <Input
                    aria-label="规则原因码"
                    value={entry.action.reasonCode}
                    disabled={!isDraft || busy}
                    placeholder="原因码"
                    onChange={(e) => updateEntry(index, { action: { ...entry.action, reasonCode: e.target.value } })}
                  />
                  <Button
                    size="icon"
                    variant="ghost"
                    disabled={!isDraft || busy}
                    onClick={() => { setEntries(entries.filter((_, i) => i !== index)); setDirty(true) }}
                    aria-label="删除条目"
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      ) : null}
          </div>
          <aside className={`min-h-0 overflow-y-auto p-5 lg:border-l lg:border-border ${editorView === 'test' ? '' : 'hidden lg:block'}`}>
          {current ? <SimulateCard layout="stacked" key={current.id} scope={{ botId: current.botId, scopeType: current.targets?.[0]?.kind ?? current.scopeType, scopeId: current.targets?.[0]?.id ?? 0 }} entries={entries} builtinRules={builtinRules} previewGlobal={allTargets} previewTargetsText={targetText} /> : null}
          </aside></div>
          <footer className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border bg-card px-6 py-4">
            {current ? <Button variant="ghost" className="text-destructive" disabled={busy} onClick={() => setDeleting(current)}><Trash2 />删除版本</Button> : null}
            {current ? <Button variant="outline" disabled={busy} onClick={() => current && (!dirty || window.confirm('放弃未保存的修改并新建草稿？')) && createDraft.mutate({ note: '', cloneActive: true, cloneFromId: current.id }, { onSuccess: set => { setDirty(false); setSelectedId(set.id); setEditorView('edit'); toast.success(`已创建 v${set.version} 草稿`) }, onError: error => toast.error(describeError(error).title) })}>从此版本创建草稿</Button> : null}
{current ? (            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => { setEntries([...entries, { ...EMPTY_ENTRY, priority: Math.max(-1, ...entries.map(entry => entry.priority)) + 1 }]); setDirty(true) }} disabled={!isDraft || busy}>
                <Plus />
                添加条目
              </Button>
              <Button
                size="sm"
                disabled={!isDraft || !dirty || busy || (!allTargets && (!targetText.trim() || targetText.split(/[,，\s]+/).filter(Boolean).some(value => !/^(private|group):[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value.split(':')[1]))))) || entries.some(entry => !Number.isSafeInteger(entry.priority) || entry.priority < 0 || !entry.action.reasonCode.trim()) || new Set(entries.map(entry => entry.priority)).size !== entries.length}
                onClick={() =>
                  save.mutate({ id: current.id, entries, builtinRules, targets: allTargets ? [] : targetText.split(/[,，\s]+/).filter(Boolean).map(value => { const [kind,id] = value.split(':'); return {kind: kind as ScopeType, id:Number(id)} }) }, {
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
                disabled={!isDraft || dirty || busy}
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
            </div>) : null}
          </footer>
        </SheetContent>
      </Sheet>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={open => { if (!open && !remove.isPending) setDeleting(null) }}
        title="删除规则版本？"
        description={deleting ? `v${deleting.version} · ${RULESET_STATUS_LABELS[deleting.status]} · ${deleting.targets?.length ? deleting.targets.map(target => `${target.kind}:${target.id}`).join('、') : '所有会话'}。删除生效版本后将回到全局或内置规则，不自动恢复旧版本。` : ''}
        destructive
        pending={remove.isPending}
        confirmLabel="确认删除"
        onConfirm={() => {
          if (!deleting || !selectedBotId) return
          const target = deleting
          const closed = () => {
            if (selectedId === target.id) { setSelectedId(null); setDirty(false); setEditorView('edit') }
            setDeleting(null)
          }
          remove.mutate({ id: target.id, botId: selectedBotId }, {
            onSuccess: () => { closed(); toast.success('规则版本已删除') },
            onError: error => {
              // 版本已被其它端删除时按已删除处理，并刷新列表让界面回到真实状态。
              if (error instanceof ApiError && error.kind === 'not-found' && error.detail === 'missing') {
                closed()
                void versions.refetch()
                toast.success('该规则版本已不存在')
                return
              }
              toast.error(describeError(error).title)
            },
          })
        }}
      />
    </div>
  )

  function updateEntry(index: number, patch: Partial<RuleEntry>) {
    setEntries((prev) => prev.map((entry, i) => (i === index ? { ...entry, ...patch } : entry)))
    setDirty(true)
  }
}

export function SimulateCard({ scope, entries, builtinRules, previewGlobal, previewTargetsText, layout = 'columns' }: { previewTargetsText?: string; layout?: 'stacked' | 'columns'; scope: { botId: number; scopeType: ScopeType; scopeId: number }; entries?: RuleEntry[]; builtinRules?: Record<string, boolean>; previewGlobal?: boolean }) {
  const [simulationKind,setSimulationKind]=useState<ScopeType>(scope.scopeType)
  const [simulationTarget,setSimulationTarget]=useState(scope.scopeId>0?String(scope.scopeId):'')
  const actualScope={botId:scope.botId,scopeType:simulationKind,scopeId:Number(simulationTarget)}
  const simulate = useSimulate(actualScope)
  const [mode, setMode] = useState<'rules' | 'semantic'>('rules')
  const [text, setText] = useState('')
  const [explicit, setExplicit] = useState(false)
  const [replyMode, setReplyMode] = useState('intent_based')
  const [seconds, setSeconds] = useState('60')
  const targets = (previewTargetsText ?? '').split(/[,，\s]+/).filter(Boolean)
  const targetAllowed = entries === undefined || previewGlobal || (targets.length > 0 && targets.every(value => /^(private|group):[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value.split(':')[1]))) && targets.includes(`${simulationKind}:${simulationTarget}`))
  const snapshot = JSON.stringify({ actualScope, entries, builtinRules, previewGlobal, previewTargetsText, mode, text, explicit, replyMode, seconds })
  const [submitted, setSubmitted] = useState('')

  return (
    <Card className={`grid min-w-0 gap-6 p-5 ${layout === 'columns' ? 'lg:grid-cols-2' : ''}`}>

      <div className="min-w-0 space-y-4">
      <div className="grid gap-2 sm:grid-cols-2"><Select aria-label="模拟实际会话类型" value={simulationKind} onChange={event=>{setSimulationKind(event.target.value as ScopeType);simulate.reset()}}><option value="private">好友私聊</option><option value="group">群聊</option></Select><Input aria-label="模拟实际目标号码" placeholder="输入用于判断范围的 QQ 号或群号" value={simulationTarget} onChange={event=>{setSimulationTarget(event.target.value);simulate.reset()}}/></div>
      <div className="text-xs text-muted-foreground">实际模拟对象：{simulationKind==='private'?'好友':'群聊'} {simulationTarget || '未选择'}</div>
      {!targetAllowed ? <p role="alert" className="text-warning">测试对象不在当前编辑的适用范围内，请调整测试对象或规则范围。</p> : null}
      <div className="text-sm font-medium">只读规则模拟 · {entries ? '当前编辑内容' : '当前生效版本'}</div>
      <Select aria-label="验证方式" value={mode} onChange={e => { setMode(e.target.value as 'rules' | 'semantic'); simulate.reset() }}><option value="rules">规则验证 · 不调用模型</option><option value="semantic">完整语义验证 · 使用真实模型</option></Select>
      <p className="text-meta text-muted-foreground">{mode === 'semantic' ? '使用当前参与权限、真实会话活动与获准读取的历史，以及服务 Provider 和严格阈值；按下方测试回复模式判断，不采用手工秒数，不保存决策或发送 QQ 消息。' : '只验证规则闸门，不调用语义模型，也不发送 QQ 消息。'}</p>
      <div className="flex gap-2"><Select aria-label="模拟回复模式" value={replyMode} onChange={e => setReplyMode(e.target.value)}><option value="intent_based">智能判断</option><option value="always_reply">始终回复</option><option value="mention_only">仅明确指向</option><option value="observe_only">仅观察</option></Select><Input aria-label="距上次回复秒数" disabled={mode==='semantic'} value={seconds} onChange={e => setSeconds(e.target.value)} inputMode="numeric" /></div>
      <div className={`grid gap-2 ${layout === 'stacked' ? 'grid-cols-[minmax(0,1fr)_auto]' : 'sm:grid-cols-4'}`}>
        <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="输入一条消息" className={layout === 'stacked' ? 'col-span-2' : 'sm:col-span-2'} />
        <Select value={explicit ? 'explicit' : 'plain'} onChange={(e) => setExplicit(e.target.value === 'explicit')}>
          <option value="plain">未被指向</option>
          <option value="explicit">明确指向机器人</option>
        </Select>
        <Button
          disabled={!targetAllowed || !Number.isSafeInteger(actualScope.scopeId) || actualScope.scopeId<=0 || !text || simulate.isPending || !/^\d+$/.test(seconds) || !Number.isSafeInteger(Number(seconds))}
          onClick={() => {
            setSubmitted(snapshot)
            simulate.mutate(
              { mode, builtinRules, previewGlobal, kind: actualScope.scopeType === 'group' ? 'group' : 'private', replyMode, explicit, text, entries, secondsSinceReply: Number(seconds) },
              { onError: (error) => toast.error(describeError(error).title) },
            )
          }}
        >
          <Send />
          模拟
        </Button>
      </div>
      </div>
      <section aria-label="模拟结果" className={`min-w-0 border-t border-border pt-5 ${layout === 'columns' ? 'lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0' : ''}`}>
      <h3 className="mb-4 text-sm font-medium">验证结果</h3>
      {simulate.isPending ? <p role="status" className="text-muted-foreground">正在验证…</p> : null}
      {simulate.data && submitted !== snapshot ? <p role="status">内容已变化，请重新测试</p> : null}
      {simulate.data && !simulate.isPending && submitted === snapshot ? (
        <div className="rounded-md bg-muted/40 p-3 text-meta">
          <div>最终判断：{simulate.data.decision ?? '规则验证尚未进行语义判断'}</div>
          <div>语义类别：{simulate.data.semanticCategory || '未调用或不可用'}</div>
          <div>模型：{simulate.data.provider || '未调用'} / {simulate.data.model || '—'} · {simulate.data.latencyMs ?? 0} ms</div>
          {simulate.data.semanticAvailable === false ? <p role="alert">语义服务不可用：{simulate.data.failureCode || '已降级'}</p> : null}
          {Object.entries(simulate.data.scores ?? {}).map(([key,value]) => <div key={key}>{key}：{value.toFixed(3)}</div>)}
          <div>规则结果：{simulate.data.blocked ? '被拦截' : simulate.data.forceReply ? '强制回复' : simulate.data.needSemantic ? '需要语义判定' : '走内置策略'}</div>
          <div>原因码：{simulate.data.reasonCode || '—'}</div>
          <div>规则版本：{simulate.data.rulesetVersion || '内置规则'}</div>
        </div>
      ) : !simulate.isPending ? <p className="text-sm text-muted-foreground">填写测试消息并运行模拟后，在这里查看判断、语义分数与规则结果。</p> : null}
      </section>
    </Card>
  )
}

export function SimulationPanel() {
 const { selectedBotId } = useAccountContext()
 return selectedBotId ? <SimulateCard scope={{ botId: selectedBotId, scopeType: 'private', scopeId: 0 }} /> : <FeedbackState variant="empty" title="先选择机器人账号" />
}
