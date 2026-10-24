import { useEffect, useMemo, useState } from 'react'
import { Link, useBlocker, useParams } from '@tanstack/react-router'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { ArrowLeft, Save } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../../components/ui/card'
import { ConfirmDialog } from '../../components/confirm-dialog'
import { FeedbackState } from '../../components/feedback-state'
import { InfoRow } from '../../components/info-row'
import { PageHeader } from '../../components/page-header'
import { Skeleton } from '../../components/ui/skeleton'
import {
  DEFAULT_SCOPE_FORM,
  isScopeType,
  scopeDangerFlags,
  toScopeForm,
} from '../../lib/api/adapters/scope'
import { REPLY_MODE_LABELS, SCOPE_TYPE_LABELS, type ScopeForm } from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { cn } from '../../lib/utils'
import { ScopeFormFields } from './components/scope-form'
import { useSaveScope, useScope } from './queries'
import {
  fromScopeFormValues,
  scopeFormSchema,
  toScopeFormValues,
  type ScopeFormValues,
} from './schema'

interface PendingSave {
  form: ScopeForm
  flags: string[]
}

export function ScopeEditPage() {
  const { type, id } = useParams({ from: '/scopes/$type/$id' })
  const scopeType = isScopeType(type) ? type : undefined
  const scopeId = Number(id)
  const validScope = scopeType !== undefined && Number.isInteger(scopeId) && scopeId > 0

  const scope = useScope(scopeType ?? 'group', scopeId)
  const save = useSaveScope()

  const [baseline, setBaseline] = useState<ScopeForm>(DEFAULT_SCOPE_FORM)
  const [pendingSave, setPendingSave] = useState<PendingSave | null>(null)
  const [saveError, setSaveError] = useState<unknown>(null)

  const form = useForm<ScopeFormValues>({
    resolver: zodResolver(scopeFormSchema),
    defaultValues: toScopeFormValues(DEFAULT_SCOPE_FORM),
    mode: 'onBlur',
  })
  const { reset, formState, handleSubmit } = form
  const isDirty = formState.isDirty
  const isSaving = save.isPending

  useEffect(() => {
    if (!scope.data) {
      return
    }
    const next = toScopeForm(scope.data)
    setBaseline(next)
    reset(toScopeFormValues(next))
  }, [scope.data, reset])

  // Unsaved edits block in-app navigation and trigger the browser prompt on
  // close; the page also shows a persistent banner, so nothing is dropped
  // silently.
  // 未保存时拦截应用内导航，并在关闭页面时触发浏览器提示；
  // 页面另有常驻横幅，避免静默丢弃修改。
  const blocker = useBlocker({
    shouldBlockFn: () => isDirty,
    withResolver: true,
    enableBeforeUnload: () => isDirty,
  })

  const summary = useMemo(() => {
    if (!scope.data) {
      return null
    }
    return {
      enabled: scope.data.enabled,
      replyMode: scope.data.replyMode,
      storeMessages: scope.data.storeMessages,
    }
  }, [scope.data])

  const submitSave = (next: ScopeForm) => {
    if (!scopeType) {
      return
    }
    save.mutate(
      { scopeType, scopeId, form: next },
      {
        onSuccess: (policy) => {
          const stored = toScopeForm(policy)
          setBaseline(stored)
          reset(toScopeFormValues(stored))
          setSaveError(null)
          setPendingSave(null)
          toast.success('策略已保存', {
            description: policy.enabled ? '该作用域现在会参与对话。' : '策略已保存，但当前未启用。',
          })
        },
        onError: (error) => {
          setSaveError(error)
          setPendingSave(null)
          toast.error('保存失败', { description: describeError(error).description })
        },
      },
    )
  }

  const onSubmit = handleSubmit((values) => {
    const next = fromScopeFormValues(values)
    const flags = scopeDangerFlags(next, baseline)
    if (flags.length > 0) {
      setPendingSave({ form: next, flags })
      return
    }
    submitSave(next)
  })

  if (!validScope) {
    return (
      <div className="space-y-4">
        <PageHeader title="作用域策略" description="地址中的类型或 ID 无效。" />
        <Card>
          <FeedbackState
            variant="error"
            title="作用域参数无效"
            description="作用域类型只能是 group 或 private，ID 必须是正整数。"
            action={
              <Button asChild variant="outline" size="sm">
                <Link to="/scopes">返回策略列表</Link>
              </Button>
            }
          />
        </Card>
      </div>
    )
  }

  const typeLabel = SCOPE_TYPE_LABELS[scopeType]

  return (
    <div className="space-y-4">
      <PageHeader
        title={`${typeLabel} ${id} 的策略`}
        description="PUT 会整份覆盖策略：保存时提交页面上的全部字段，保存后自动回读服务端结果。"
        actions={
          <div className="flex items-center gap-2">
            <Button asChild variant="ghost" size="sm">
              <Link to="/scopes">
                <ArrowLeft aria-hidden />
                策略列表
              </Link>
            </Button>
            <Button type="submit" form="scope-form" size="sm" disabled={isSaving || !isDirty}>
              <Save aria-hidden />
              {isSaving ? '保存中…' : '保存策略'}
            </Button>
          </div>
        }
      />

      {saveError ? (
        <div
          className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-[11px] leading-relaxed text-destructive"
          role="alert"
        >
          {describeError(saveError).title}：{describeError(saveError).description}
        </div>
      ) : null}

      {isDirty ? (
        <div className="flex items-center gap-2 rounded-lg border border-warning/25 bg-warning/10 px-3 py-2 text-[11px] text-warning">
          <span className="size-1.5 rounded-full bg-warning" aria-hidden />
          有未保存的修改，离开页面会丢失；浏览器关闭前也会提示。
        </div>
      ) : null}

      {scope.isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : scope.isError ? (
        <Card>
          <FeedbackState
            variant="error"
            error={scope.error}
            onRetry={() => void scope.refetch()}
          />
        </Card>
      ) : (
        <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_300px]">
          <form id="scope-form" onSubmit={onSubmit} className="min-w-0">
            <ScopeFormFields form={form} disabled={isSaving} />
            <div className="sticky bottom-0 mt-3 flex items-center justify-between gap-3 rounded-xl border border-border bg-card/95 px-4 py-3 backdrop-blur">
              <span className="text-[11px] text-muted-foreground">
                {isDirty ? '尚未保存的修改不会生效' : '当前与服务端一致'}
              </span>
              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={!isDirty || isSaving}
                  onClick={() => reset(toScopeFormValues(baseline))}
                >
                  放弃修改
                </Button>
                <Button type="submit" size="sm" disabled={isSaving || !isDirty}>
                  {isSaving ? '保存中…' : '保存策略'}
                </Button>
              </div>
            </div>
          </form>

          <div className="space-y-3">
            <Card className="lg:sticky lg:top-[76px]">
              <CardHeader>
                <CardTitle>当前目标</CardTitle>
                <CardDescription className="mt-1">
                  未保存过的目标会解析为默认拒绝策略。
                </CardDescription>
              </CardHeader>
              <CardContent className="pt-1">
                <InfoRow label="类型" value={`${typeLabel}（${scopeType}）`} />
                <InfoRow label="ID" value={id} mono />
                <InfoRow label="回复模式" value={summary ? REPLY_MODE_LABELS[summary.replyMode] : '不可获取'} />
                <div className="flex items-center justify-between gap-3 py-2">
                  <span className="text-xs text-muted-foreground">生效状态</span>
                  <Badge variant={summary?.enabled ? 'success' : 'warning'}>
                    {summary?.enabled ? '已启用' : '未启用'}
                  </Badge>
                </div>
                <p className="mt-2 rounded-lg border border-border/70 bg-secondary/40 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
                  {summary?.enabled
                    ? '该目标已参与对话。关闭「启用该作用域」并保存即可随时停用。'
                    : '即使其它字段已开启，未启用状态下机器人也不会回复该会话。'}
                </p>
                {summary?.storeMessages ? (
                  <p className="mt-2 text-[11px] leading-relaxed text-warning">
                    正在保存消息内容，请确认留存天数与群内告知义务。
                  </p>
                ) : null}
              </CardContent>
            </Card>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={pendingSave !== null}
        onOpenChange={(open) => {
          if (!open) {
            setPendingSave(null)
          }
        }}
        title="确认开启敏感能力？"
        description="保存后立即生效。以下能力会扩大机器人可访问的数据范围。"
        details={pendingSave?.flags}
        confirmLabel="确认并保存"
        pending={isSaving}
        onConfirm={() => {
          if (pendingSave) {
            submitSave(pendingSave.form)
          }
        }}
      />

      <ConfirmDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(open) => {
          if (!open) {
            blocker.reset?.()
          }
        }}
        title="放弃未保存的修改？"
        description="离开后本次修改不会保存，表单会恢复为服务端当前生效的策略。"
        confirmLabel="放弃并离开"
        destructive
        onConfirm={() => blocker.proceed?.()}
      />

      <p className={cn('text-[11px] text-muted-foreground', !isDirty && 'opacity-70')}>
        保存成功后会重新读取该作用域的生效策略，避免只在前端乐观展示。
      </p>
    </div>
  )
}
