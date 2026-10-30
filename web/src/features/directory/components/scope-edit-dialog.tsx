import { AnimatedNavIcon } from '../../../components/animated-nav-icon'
import { useEffect, useRef, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useEditGuard } from '../../intent/edit-guard'
import { Save, Trash2, MessageCircle, ShieldCheck, Send, Database } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from '../../../components/ui/sheet'
import { ConfirmDialog } from '../../../components/confirm-dialog'
import { FeedbackState } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import {
  DEFAULT_SCOPE_FORM,
  scopeDangerFlags,
  toScopeForm,
} from '../../../lib/api/adapters/scope'
import { SCOPE_TYPE_LABELS, type ScopePolicy, type ScopeType } from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { ScopeFormFields } from '../../scopes/components/scope-form'
import {
  useDeleteScope,
  useSaveScope,
  useScope,
  useToolsCatalog,
} from '../../scopes/queries'
import {
  fromScopeFormValues,
  scopeFormSchema,
  toScopeFormValues,
  type ScopeFormValues,
} from '../../scopes/schema'

interface PendingSave {
  values: ScopeFormValues
  flags: string[]
}

interface ScopeEditDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  botId: number
  scopeType: ScopeType
  scopeId: number
  /** Display name of the target contact/group for the dialog title. */
  targetLabel: string
  /** Whether a stored policy row exists (decides delete button visibility). */
  savedPolicy?: ScopePolicy
}

/**
 * Policy editing workspace for the contacts page. Reuses the same form fields
 * and confirmations as the former standalone edit page: risky capability
 * switches need an explicit confirmation, and unsaved edits prompt before the
 * dialog closes.
 * 联系人页内嵌的策略编辑工作区。复用原独立编辑页的表单与确认流程：
 * 敏感开关需二次确认，未保存的修改在关闭对话框前会先询问。
 */
export function ScopeEditDialog({
  open,
  onOpenChange,
  botId,
  scopeType,
  scopeId,
  targetLabel,
  savedPolicy,
}: ScopeEditDialogProps) {
  const scope = useScope(botId, scopeType, scopeId)
  const template = useScope(botId, scopeType, 0)
  const [loadTemplate, setLoadTemplate] = useState(false)
  const [templateVersion, setTemplateVersion] = useState<number | undefined>(undefined)
  const tools = useToolsCatalog(scopeType)
  const save = useSaveScope()
  const remove = useDeleteScope()

  const [baseline, setBaseline] = useState(DEFAULT_SCOPE_FORM)
  const [baselineTemplateVersion,setBaselineTemplateVersion]=useState<number | undefined>(undefined)
  const loadedIdentity=useRef<string | null>(null)
  const [pendingSave, setPendingSave] = useState<PendingSave | null>(null)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const [pendingDelete, setPendingDelete] = useState(false)

  const form = useForm<ScopeFormValues>({
    resolver: zodResolver(scopeFormSchema),
    defaultValues: toScopeFormValues(DEFAULT_SCOPE_FORM),
    mode: 'onBlur',
  })
  const { reset, formState, handleSubmit } = form
  const isDirty = formState.isDirty
  const [forceIndependent,setForceIndependent]=useState(false)
  const isBusy = save.isPending || remove.isPending
  useEditGuard(open && (isDirty || forceIndependent), open && isBusy)
  const [section,setSection]=useState('participation')

  useEffect(() => {
    const identity=`${botId}:${scopeType}:${scopeId}`
    if(!open){loadedIdentity.current=null;return}
    if (!scope.data || loadedIdentity.current===identity) return
    loadedIdentity.current=identity
    const next = toScopeForm(scope.data)
    setBaseline(next)
    const expected=scopeId === 0 ? scope.data.version : scope.data.source === 'default' ? scope.data.templateVersion : undefined
    setTemplateVersion(expected)
    setBaselineTemplateVersion(expected)
    reset(toScopeFormValues(next))
  }, [open, scope.data, reset, scopeId, botId, scopeType])

  const requestClose = (next: boolean) => {
    if (isBusy) return
    if (next || (!isDirty && !forceIndependent)) {
      onOpenChange(next)
      return
    }
    setConfirmDiscard(true)
  }

  const submitSave = (values: ScopeFormValues) => {
    save.mutate(
      { botId, scopeType, scopeId, form: fromScopeFormValues(values), expectedTemplateVersion: templateVersion },
      {
        onSuccess: (policy) => {
          const stored = toScopeForm(policy)
          setBaseline(stored)
          reset(toScopeFormValues(stored))
          setPendingSave(null)
          toast.success('策略已保存', {
            description: policy.enabled ? '该作用域现在会参与对话。' : '策略已保存，但当前未启用。',
          })
          onOpenChange(false)
        },
        onError: (error) => {
          setPendingSave(null)
          toast.error('保存失败', { description: describeError(error).description })
        },
      },
    )
  }

  const onSubmit = handleSubmit((values) => {
    const flags = scopeDangerFlags(fromScopeFormValues(values), baseline)
    if (flags.length > 0) {
      setPendingSave({ values, flags })
      return
    }
    submitSave(values)
  }, (errors) => {
    const key=Object.keys(errors)[0]
    setSection(['maxMessagesPerRun','maxCharsPerMessage','maxTotalCharsPerRun','messageIntervalMs','minReplyIntervalMs','maxRepliesPerHour'].includes(key) ? 'sending' : key==='toolPermissions' ? 'capabilities' : ['allowHistoryRead','allowMemoryWrite'].includes(key) ? 'data' : 'participation')
    toast.error('请检查表单中的字段')
  })

  const restoreChanges: string[] = []
  if (scope.data && template.data && tools.data) {
    for (const [key, label] of [['allowHistoryRead', '读取聊天历史'], ['allowMemoryWrite', '写入长期记忆']] as const) {
      if (scope.data[key] !== template.data[key]) restoreChanges.push(`${label}：${scope.data[key] ? '允许' : '关闭'} → ${template.data[key] ? '允许' : '关闭'}`)
    }
    for (const permission of tools.data.permissions.filter(permission => tools.data!.tools.some(tool => tool.applicable && permission.tools.includes(tool.id)))) {
      const before = scope.data.toolPermissions[permission.id] ?? permission.defaultGranted
      const after = template.data.toolPermissions[permission.id] ?? permission.defaultGranted
      if (before !== after) restoreChanges.push(`${permission.id}：${before ? '授权' : '关闭'} → ${after ? '授权' : '关闭'}`)
    }
  }
  const restoreReady = !!template.data && !template.isError && !template.isFetching && !!tools.data && !tools.isError && !tools.isFetching
  const confirmDelete = () => {
    if (!restoreReady) return
    remove.mutate(
      { botId, scopeType, scopeId },
      {
        onSuccess: () => {
          setPendingDelete(false)
          toast.success('策略已删除', {
            description: `${SCOPE_TYPE_LABELS[scopeType]} ${scopeId} 已恢复跟随默认模板。`,
          })
          onOpenChange(false)
        },
        onError: (error) => {
          setPendingDelete(false)
          toast.error('删除失败', { description: describeError(error).description })
        },
      },
    )
  }

  const enabled = form.watch('enabled')

  return (
    <>
      <Sheet open={open} onOpenChange={requestClose}>
        <SheetContent side="right" className="h-dvh w-full gap-0 overflow-hidden bg-background p-0 sm:max-w-[960px]">
          <header className="shrink-0 border-b border-border px-5 py-5 pr-14 sm:px-6 sm:pr-14">
            <SheetTitle className="text-lg font-semibold">
              {scopeId === 0 ? '编辑默认策略' : '编辑策略'} · {targetLabel}
            </SheetTitle>
            <SheetDescription className="mt-2 leading-relaxed">
              {scopeId === 0 ? (scopeType === 'private' ? '默认好友策略' : '默认群聊策略') : `${SCOPE_TYPE_LABELS[scopeType]} ${scopeId}`} ·{' '}
              {scopeId === 0 ? '保存后用于未独立配置的新旧会话。' : scope.data?.source === 'default' ? '当前跟随默认模板，保存后成为独立策略。' : '独立策略，保存后立即生效。'}
            </SheetDescription>
          </header>

          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-5 py-3 sm:px-6">
            <span className="text-meta text-muted-foreground">生效状态</span>
            {scope.isLoading ? (
              <Skeleton className="h-5 w-16" />
            ) : scope.isError ? (
              <FeedbackState variant="error" error={scope.error} onRetry={() => void scope.refetch()} compact />
            ) : (
              <Badge variant={enabled ? 'success' : savedPolicy ? 'warning' : 'outline'}>
                {enabled ? '已启用' : savedPolicy ? '已保存未启用' : '默认关闭'}
              </Badge>
            )}
          </div>

          <div className="flex min-h-0 flex-1 flex-col md:flex-row">
            <nav aria-label="策略设置分区" className="flex shrink-0 gap-1 overflow-x-auto border-b border-border bg-card p-3 md:w-44 md:flex-col md:border-b-0 md:border-r md:p-4">
              {([['participation', '参与控制', MessageCircle], ['capabilities', '能力授权', ShieldCheck], ['sending', '发送设置', Send], ['data', '数据权限', Database]] as const).map(([value, label, Icon]) => (
                <Button type="button" size="sm" key={value} aria-current={section === value ? 'page' : undefined}
                  className={`shrink-0 justify-start ${section === value ? 'bg-secondary text-foreground' : 'text-muted-foreground'}`}
                  variant="ghost" onClick={() => setSection(value)}><AnimatedNavIcon icon={Icon} className="text-cyan-600 dark:text-cyan-400" />{label}</Button>
              ))}
            </nav>
            <div className="min-h-0 min-w-0 flex-1 overflow-y-auto p-5 sm:p-6">
          {section==='capabilities' && tools.isError ? <FeedbackState variant="error" error={tools.error} onRetry={()=>void tools.refetch()} compact /> : null}
          {scope.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-24 w-full" />
              <Skeleton className="h-40 w-full" />
            </div>
          ) : scope.isError ? null : (
            <form id="scope-edit-dialog-form" onSubmit={onSubmit} className="min-h-0 min-w-0 overflow-y-auto">
              <ScopeFormFields form={form} disabled={isBusy} catalog={tools.data} section={section} />
            </form>
          )}

            </div>
          </div>
          <footer className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border bg-card px-5 py-4 sm:px-6">
            {scopeId !== 0 && savedPolicy?.source !== 'default' && savedPolicy ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={!restoreReady || isBusy} onClick={() => setPendingDelete(true)}
                className="text-destructive hover:text-destructive"
              >
                <Trash2 aria-hidden />
                恢复跟随默认
              </Button>
            ) : (
              <span />
            )}
            <div className="flex flex-wrap items-center gap-2">
              {scopeId!==0 ? <Button type="button" variant="outline" size="sm" disabled={isBusy || scope.isLoading || scope.isError} onClick={()=>{setForceIndependent(true);form.setValue('enabled',false,{shouldDirty:true});setSection('participation')}}>关闭此会话</Button> : null}
              {scopeId !== 0 ? <Button type="button" variant="outline" size="sm" disabled={isBusy || !template.data} onClick={() => setLoadTemplate(true)}>载入默认模板</Button> : null}
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={(!isDirty && !forceIndependent) || isBusy}
                onClick={() => {setForceIndependent(false);setTemplateVersion(baselineTemplateVersion);reset(toScopeFormValues(baseline))}}
              >
                放弃修改
              </Button>
              <Button
                type="submit"
                form="scope-edit-dialog-form"
                size="sm"
                disabled={isBusy || (!isDirty && !forceIndependent && !(scopeId>0 && scope.data?.source==='default')) || scope.isLoading || scope.isError || tools.isLoading || tools.isError}
              >
                <Save aria-hidden />
                {save.isPending ? '保存中…' : '保存策略'}
              </Button>
            </div>
          </footer>
        </SheetContent>
      </Sheet>

      <ConfirmDialog
        open={pendingSave !== null}
        onOpenChange={(next) => {
          if (!next) {
            setPendingSave(null)
          }
        }}
        title="确认开启敏感能力？"
        description="保存后立即生效。以下能力会扩大机器人可访问的数据范围。"
        details={pendingSave?.flags}
        confirmLabel="确认并保存"
        pending={save.isPending}
        onConfirm={() => {
          if (pendingSave) {
            submitSave(pendingSave.values)
          }
        }}
      />

      <ConfirmDialog
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title="放弃未保存的修改？"
        description="关闭后本次修改不会保存，表单会恢复为服务端当前生效的策略。"
        confirmLabel="放弃并关闭"
        destructive
        onConfirm={() => {
          setConfirmDiscard(false)
          setForceIndependent(false)
          reset(toScopeFormValues(baseline))
          onOpenChange(false)
        }}
      />

      <ConfirmDialog
        open={pendingDelete}
        onOpenChange={setPendingDelete}
        title="删除该作用域策略？"
        description={`恢复后将跟随${scopeType === 'private' ? '好友' : '群聊'}默认策略，参与状态：${template.data?.enabled ? '启用' : '关闭'}。`}
        details={[...(restoreChanges.length ? restoreChanges : ['数据权限与能力授权没有变化']), '后续默认模板修改会同步影响此会话', '历史消息与记忆数据不会被删除']}
        confirmLabel="恢复跟随默认"
        destructive
        pending={remove.isPending || !restoreReady}
        onConfirm={confirmDelete}
      />
      <ConfirmDialog open={loadTemplate} onOpenChange={setLoadTemplate} title="载入默认模板？" description="当前编辑内容将替换为对应默认模板，保存后才生效。" confirmLabel="载入模板" onConfirm={() => {
        if (template.data) {
          reset(toScopeFormValues(toScopeForm(template.data)), { keepDefaultValues: true })
          setTemplateVersion(template.data.version)
        }
        setLoadTemplate(false)
      }} />
    </>
  )
}
