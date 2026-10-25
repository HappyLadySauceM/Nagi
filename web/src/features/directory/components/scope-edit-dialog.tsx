import { useEffect, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { Save, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '../../../components/ui/dialog'
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
 * In-dialog policy editor for the contacts page. Reuses the same form fields
 * and confirmations as the former standalone edit page: risky capability
 * switches need an explicit confirmation, and unsaved edits prompt before the
 * dialog closes.
 * 联系人页内嵌的策略编辑对话框。复用原独立编辑页的表单与确认流程：
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
  const tools = useToolsCatalog()
  const save = useSaveScope()
  const remove = useDeleteScope()

  const [baseline, setBaseline] = useState(DEFAULT_SCOPE_FORM)
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
  const isBusy = save.isPending || remove.isPending

  useEffect(() => {
    if (!open || !scope.data) {
      return
    }
    const next = toScopeForm(scope.data)
    setBaseline(next)
    reset(toScopeFormValues(next))
  }, [open, scope.data, reset])

  const requestClose = (next: boolean) => {
    if (next || !isDirty) {
      onOpenChange(next)
      return
    }
    setConfirmDiscard(true)
  }

  const submitSave = (values: ScopeFormValues) => {
    save.mutate(
      { botId, scopeType, scopeId, form: fromScopeFormValues(values) },
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
  })

  const confirmDelete = () => {
    remove.mutate(
      { botId, scopeType, scopeId },
      {
        onSuccess: () => {
          setPendingDelete(false)
          toast.success('策略已删除', {
            description: `${SCOPE_TYPE_LABELS[scopeType]} ${scopeId} 已回到默认拒绝。`,
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
      <Dialog open={open} onOpenChange={requestClose}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {savedPolicy ? '编辑策略' : '配置策略'} · {targetLabel}
            </DialogTitle>
            <DialogDescription>
              {SCOPE_TYPE_LABELS[scopeType]} {scopeId} ·{' '}
              {savedPolicy ? '保存后立即生效，可随时调整。' : '未保存过，当前按默认拒绝处理。'}
            </DialogDescription>
          </DialogHeader>

          <div className="flex items-center justify-between gap-3 rounded-lg border border-border/70 bg-secondary/40 px-3 py-2">
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

          {scope.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-24 w-full" />
              <Skeleton className="h-40 w-full" />
            </div>
          ) : scope.isError ? null : (
            <form id="scope-edit-dialog-form" onSubmit={onSubmit} className="min-w-0">
              <ScopeFormFields form={form} disabled={isBusy} catalog={tools.data} />
            </form>
          )}

          <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
            {savedPolicy ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={isBusy}
                onClick={() => setPendingDelete(true)}
                className="text-destructive hover:text-destructive"
              >
                <Trash2 aria-hidden />
                删除策略
              </Button>
            ) : (
              <span />
            )}
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={!isDirty || isBusy}
                onClick={() => reset(toScopeFormValues(baseline))}
              >
                放弃修改
              </Button>
              <Button
                type="submit"
                form="scope-edit-dialog-form"
                size="sm"
                disabled={isBusy || !isDirty || scope.isLoading || scope.isError}
              >
                <Save aria-hidden />
                {save.isPending ? '保存中…' : '保存策略'}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

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
          reset(toScopeFormValues(baseline))
          onOpenChange(false)
        }}
      />

      <ConfirmDialog
        open={pendingDelete}
        onOpenChange={setPendingDelete}
        title="删除该作用域策略？"
        description={`${SCOPE_TYPE_LABELS[scopeType]} ${scopeId} 将立即回到默认拒绝：不回复、不保存消息、不写记忆。`}
        details={['删除后该会话重新需要显式授权才能参与对话', '历史消息与记忆数据不会被删除']}
        confirmLabel="删除策略"
        destructive
        pending={remove.isPending}
        onConfirm={confirmDelete}
      />
    </>
  )
}
