import { useEffect } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { Button } from '../../../components/ui/button'
import { FormField } from '../../../components/form-field'
import { Input } from '../../../components/ui/input'
import { Select } from '../../../components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { Textarea } from '../../../components/ui/textarea'
import {
  MEMORY_VISIBILITY_HINTS,
  MEMORY_VISIBILITY_LABELS,
  type MemoryEntry,
} from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { useUpdateMemory } from '../queries'
import { memoryEditSchema, memoryWriteVisibilities, type MemoryEditValues } from '../schema'

interface MemoryEditSheetProps {
  memory: MemoryEntry | null
  onOpenChange: (open: boolean) => void
}

export function MemoryEditSheet({ memory, onOpenChange }: MemoryEditSheetProps) {
  const update = useUpdateMemory()
  const form = useForm<MemoryEditValues>({
    resolver: zodResolver(memoryEditSchema),
    defaultValues: {
      visibility: 'group',
      ownerKey: '',
      content: '',
    },
    mode: 'onBlur',
  })
  const { register, handleSubmit, watch, reset, formState } = form
  const visibility = watch('visibility')
  const errors = formState.errors
  const isLegacyShared = memory?.visibility === 'bot_shared'

  useEffect(() => {
    if (!memory || memory.visibility === 'bot_shared') {
      return
    }
    reset({
      visibility: memory.visibility,
      ownerKey: memory.ownerKey,
      content: memory.content,
    })
  }, [memory, reset])

  const onSubmit = handleSubmit((values) => {
    if (!memory || isLegacyShared) {
      return
    }
    update.mutate(
      {
        id: memory.id,
        content: values.content,
        visibility: values.visibility,
        ownerKey: values.ownerKey,
      },
      {
        onSuccess: () => {
          toast.success('记忆已更新')
          onOpenChange(false)
        },
        onError: (error) => {
          toast.error('更新失败', { description: describeError(error).description })
        },
      },
    )
  })

  return (
    <Sheet open={memory !== null} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:w-[440px]">
        <div className="flex h-full flex-col">
          <div className="border-b border-border px-5 py-4">
            <SheetTitle>{isLegacyShared ? '查看历史共享记忆' : '编辑记忆'}</SheetTitle>
            <SheetDescription className="mt-1">
              {isLegacyShared ? '历史共享条目只可查看，可在列表中永久删除。' : '可修改内容、可见性与归属。'}
            </SheetDescription>
          </div>

          {isLegacyShared ? (
            <p className="flex-1 whitespace-pre-wrap overflow-y-auto px-5 py-4 text-sm">{memory?.content}</p>
          ) : (
            <form id="memory-edit-form" onSubmit={onSubmit} className="flex-1 space-y-3 overflow-y-auto px-5 py-4">
              <FormField
                id="edit-visibility"
                label="可见性"
                hint={MEMORY_VISIBILITY_HINTS[visibility]}
              >
                <Select id="edit-visibility" {...register('visibility')}>
                  {memoryWriteVisibilities.map((option) => (
                    <option key={option} value={option}>
                      {MEMORY_VISIBILITY_LABELS[option]}
                    </option>
                  ))}
                </Select>
              </FormField>

              <FormField
                id="edit-owner"
                label="归属"
                required
                hint="群号、QQ 号或会话 ID。"
                error={errors.ownerKey?.message}
              >
                <Input
                  id="edit-owner"
                  disabled={update.isPending}
                  aria-invalid={errors.ownerKey ? true : undefined}
                  aria-describedby={errors.ownerKey ? 'edit-owner-error' : 'edit-owner-hint'}
                  {...register('ownerKey')}
                />
              </FormField>

              <FormField
                id="edit-content"
                label="内容"
                required
                error={errors.content?.message}
              >
                <Textarea
                  id="edit-content"
                  rows={6}
                  disabled={update.isPending}
                  aria-invalid={errors.content ? true : undefined}
                  aria-describedby={errors.content ? 'edit-content-error' : undefined}
                  {...register('content')}
                />
              </FormField>

              <div className="grid grid-cols-2 gap-3 text-meta text-muted-foreground">
                <div>
                  <p className="text-muted-foreground">来源</p>
                  <p className="mt-0.5 font-mono text-foreground">{memory?.source ?? '未记录'}</p>
                </div>
                <div>
                  <p className="text-muted-foreground">状态</p>
                  <p className="mt-0.5 text-foreground">{memory?.status ?? '—'}</p>
                </div>
              </div>
            </form>
          )}

          <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onOpenChange(false)}
              disabled={update.isPending}
            >
              取消
            </Button>
            {!isLegacyShared && (
              <Button
                type="submit"
                form="memory-edit-form"
                size="sm"
                disabled={update.isPending || !formState.isDirty}
              >
                {update.isPending ? '保存中…' : '保存修改'}
              </Button>
            )}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
