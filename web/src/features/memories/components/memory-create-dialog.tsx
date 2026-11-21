import { useEffect } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { Button } from '../../../components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../../components/ui/dialog'
import { FormField } from '../../../components/form-field'
import { Input } from '../../../components/ui/input'
import { Label } from '../../../components/ui/label'
import { Select } from '../../../components/ui/select'
import { Switch } from '../../../components/ui/switch'
import { Textarea } from '../../../components/ui/textarea'
import {
  MEMORY_SUBJECT_LABELS,
  MEMORY_VISIBILITY_HINTS,
  MEMORY_VISIBILITY_LABELS,
  type MemorySubjectType,
} from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { useCreateMemory } from '../queries'
import { memoryCreateSchema, memoryWriteVisibilities, parseConfidence, type MemoryCreateValues } from '../schema'

const SUBJECT_OPTIONS: MemorySubjectType[] = ['unknown', 'user', 'group']

const SUBJECT_HINTS: Record<MemorySubjectType, string> = {
  unknown: '未记录主体；该候选不能用于替代旧事实。',
  user: '主体是单个用户，需填写其稳定 ID。',
  group: '主体是群聊，需填写群号。',
}

const DEFAULT_VALUES: MemoryCreateValues = {
  visibility: 'group',
  ownerKey: '',
  subjectType: 'unknown',
  subjectId: '',
  content: '',
  source: '',
  confidence: '0.8',
  approve: false,
}

interface MemoryCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  botId?: number
}

export function MemoryCreateDialog({ open, onOpenChange, botId }: MemoryCreateDialogProps) {
  const create = useCreateMemory()
  const form = useForm<MemoryCreateValues>({
    resolver: zodResolver(memoryCreateSchema),
    defaultValues: DEFAULT_VALUES,
    mode: 'onBlur',
  })
  const { register, handleSubmit, watch, setValue, reset, formState } = form
  const visibility = watch('visibility')
  const subjectType = watch('subjectType')
  const errors = formState.errors
  const isUnknownSubject = subjectType === 'unknown'

  useEffect(() => {
    if (isUnknownSubject) {
      setValue('subjectId', '')
    }
  }, [isUnknownSubject, setValue])

  const onSubmit = handleSubmit((values) => {
    if (botId === undefined) {
      toast.error('无法创建', { description: '机器人账号尚未识别，请先连接 OneBot 并同步目录。' })
      return
    }
    create.mutate(
      {
        botId,
        visibility: values.visibility,
        ownerKey: values.ownerKey,
        subjectType: values.subjectType,
        subjectId: values.subjectType === 'unknown' ? '' : values.subjectId,
        content: values.content,
        source: values.source,
        confidence: parseConfidence(values.confidence),
        approve: values.approve,
      },
      {
        onSuccess: (result) => {
          if (result.duplicate) {
            toast.warning('已存在相同记忆', {
              description: '未重复创建；如需启用，请在列表中审批该条记忆。',
            })
          } else {
            toast.success(values.approve ? '记忆已创建并启用' : '候选记忆已创建')
          }
          reset(DEFAULT_VALUES)
          onOpenChange(false)
        },
        onError: (error) => {
          toast.error('创建失败', { description: describeError(error).description })
        },
      },
    )
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建记忆</DialogTitle>
          <DialogDescription>
            写入的内容会成为检索来源。默认创建为候选，需审批后才会被会话读取。
          </DialogDescription>
        </DialogHeader>

        <form id="memory-create-form" onSubmit={onSubmit} className="mt-4 space-y-3">
          <FormField
            id="create-visibility"
            label="可见性"
            hint={MEMORY_VISIBILITY_HINTS[visibility]}
          >
            <Select id="create-visibility" {...register('visibility')}>
              {memoryWriteVisibilities.map((option) => (
                <option key={option} value={option}>
                  {MEMORY_VISIBILITY_LABELS[option]}
                </option>
              ))}
            </Select>
          </FormField>

          <FormField
            id="create-owner"
            label="归属"
            required
            hint="群号、QQ 号或会话 ID，需与可见性匹配。"
            error={errors.ownerKey?.message}
          >
            <Input
              id="create-owner"
              disabled={create.isPending}
              placeholder="例如 555123456"
              aria-invalid={errors.ownerKey ? true : undefined}
              aria-describedby={errors.ownerKey ? 'create-owner-error' : 'create-owner-hint'}
              {...register('ownerKey')}
            />
          </FormField>

          <div className="grid gap-3 sm:grid-cols-2">
            <FormField
              id="create-subject-type"
              label="主体类型"
              hint={SUBJECT_HINTS[subjectType]}
              error={errors.subjectType?.message}
            >
              <Select
                id="create-subject-type"
                disabled={create.isPending}
                aria-invalid={errors.subjectType ? true : undefined}
                aria-describedby={
                  errors.subjectType
                    ? 'create-subject-type-error'
                    : 'create-subject-type-hint'
                }
                {...register('subjectType')}
              >
                {SUBJECT_OPTIONS.map((option) => (
                  <option key={option} value={option}>
                    {option === 'unknown' ? '未知（不可替代）' : MEMORY_SUBJECT_LABELS[option]}
                  </option>
                ))}
              </Select>
            </FormField>

            <FormField
              id="create-subject-id"
              label="主体 ID"
              required={!isUnknownSubject}
              hint={
                isUnknownSubject
                  ? '未知主体不填写 ID；替代旧事实前必须先补齐主体。'
                  : '稳定 ID（QQ 号或群号），需手动填写，不会从来源推断。'
              }
              error={errors.subjectId?.message}
            >
              <Input
                id="create-subject-id"
                disabled={isUnknownSubject || create.isPending}
                placeholder={
                  isUnknownSubject
                    ? '不适用'
                    : subjectType === 'group'
                      ? '例如 700001'
                      : '例如 10001'
                }
                aria-invalid={errors.subjectId ? true : undefined}
                aria-describedby={
                  errors.subjectId ? 'create-subject-id-error' : 'create-subject-id-hint'
                }
                {...register('subjectId')}
              />
            </FormField>
          </div>

          <FormField
            id="create-content"
            label="内容"
            required
            hint="一条可独立理解的事实，避免含糊指代。"
            error={errors.content?.message}
          >
            <Textarea
              id="create-content"
              rows={4}
              disabled={create.isPending}
              placeholder="例如：每周五 20:00 在群里同步值班交接，负责人需要提前一天确认。"
              aria-invalid={errors.content ? true : undefined}
              aria-describedby={errors.content ? 'create-content-error' : 'create-content-hint'}
              {...register('content')}
            />
          </FormField>

          <div className="grid gap-3 sm:grid-cols-2">
            <FormField
              id="create-source"
              label="来源"
              hint="可选，记录人工或系统来源。"
              error={errors.source?.message}
            >
              <Input
                id="create-source"
                placeholder="manual"
                disabled={create.isPending}
                {...register('source')}
              />
            </FormField>
            <FormField
              id="create-confidence"
              label="置信度"
              hint="0 到 1，保留两位小数。"
              error={errors.confidence?.message}
            >
              <Input
                id="create-confidence"
                inputMode="decimal"
                className="text-right font-mono tabular-nums"
                disabled={create.isPending}
                aria-invalid={errors.confidence ? true : undefined}
                aria-describedby={
                  errors.confidence ? 'create-confidence-error' : 'create-confidence-hint'
                }
                {...register('confidence')}
              />
            </FormField>
          </div>

          <div className="flex items-start justify-between gap-4 rounded-lg border border-border/70 bg-secondary/30 px-3 py-2.5">
            <div className="min-w-0">
              <Label htmlFor="create-approve" className="text-sm">
                立即审批并启用
              </Label>
              <p className="mt-0.5 text-meta leading-relaxed text-muted-foreground">
                关闭时保存为候选，需要到列表中审批；重复内容不会被激活。
              </p>
            </div>
            <Switch
              id="create-approve"
              checked={watch('approve')}
              disabled={create.isPending}
              onCheckedChange={(checked) => setValue('approve', checked)}
            />
          </div>
        </form>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={create.isPending}
          >
            取消
          </Button>
          <Button
            type="submit"
            form="memory-create-form"
            size="sm"
            disabled={create.isPending || botId === undefined}
          >
            {create.isPending ? '创建中…' : '创建记忆'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
