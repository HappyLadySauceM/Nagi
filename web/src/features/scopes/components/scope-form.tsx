import { type ReactNode } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../../../components/ui/card'
import { FormField } from '../../../components/form-field'
import { Input } from '../../../components/ui/input'
import { Label } from '../../../components/ui/label'
import { Select } from '../../../components/ui/select'
import { Switch } from '../../../components/ui/switch'
import {
  REPLY_MODE_HINTS,
  REPLY_MODE_LABELS,
  type ReplyMode,
} from '../../../lib/api/contracts'
import { cn } from '../../../lib/utils'
import type { ScopeFormValues } from '../schema'

const REPLY_MODE_OPTIONS: ReplyMode[] = [
  'mention_only',
  'intent_based',
  'always_reply',
  'observe_only',
]

interface SwitchRowProps {
  id: string
  label: string
  description: string
  checked: boolean
  disabled: boolean
  onChange: (checked: boolean) => void
  warning?: string
}

function SwitchRow({
  id,
  label,
  description,
  checked,
  disabled,
  onChange,
  warning,
}: SwitchRowProps) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-border/60 py-3 last:border-b-0">
      <div className="min-w-0">
        <Label htmlFor={id} className="text-xs">
          {label}
        </Label>
        <p id={`${id}-hint`} className="mt-0.5 text-meta leading-relaxed text-muted-foreground">
          {description}
        </p>
        {checked && warning ? (
          <p className="mt-1.5 rounded-md border border-warning/25 bg-warning/10 px-2 py-1 text-meta leading-relaxed text-warning">
            {warning}
          </p>
        ) : null}
      </div>
      <Switch
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onChange}
        aria-describedby={`${id}-hint`}
      />
    </div>
  )
}

function NumberField({
  id,
  label,
  unit,
  hint,
  error,
  registration,
  disabled,
}: {
  id: string
  label: string
  unit: string
  hint: string
  error?: string
  registration: ReturnType<UseFormReturn<ScopeFormValues>['register']>
  disabled: boolean
}) {
  return (
    <FormField id={id} label={label} hint={hint} error={error}>
      <div className="relative">
        <Input
          id={id}
          inputMode="numeric"
          autoComplete="off"
          className={cn('pr-12 text-right font-mono tabular-nums')}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${id}-error` : `${id}-hint`}
          disabled={disabled}
          {...registration}
        />
        <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-meta text-muted-foreground">
          {unit}
        </span>
      </div>
    </FormField>
  )
}

function Section({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription className="mt-1">{description}</CardDescription>
      </CardHeader>
      <CardContent className="pt-1">{children}</CardContent>
    </Card>
  )
}

interface ScopeFormProps {
  form: UseFormReturn<ScopeFormValues>
  disabled?: boolean
}

export function ScopeFormFields({ form, disabled = false }: ScopeFormProps) {
  const { register, formState, watch, setValue } = form
  const errors = formState.errors
  const enabled = watch('enabled')
  const replyMode = watch('replyMode')

  return (
    <div className="space-y-3">
      <Section
        title="参与控制"
        description="关闭时该好友/群完全不参与对话；开启后按回复模式决定何时发言。"
      >
        <SwitchRow
          id="enabled"
          label="启用该作用域"
          description="默认关闭。只有显式开启并保存后，机器人才会处理该会话的消息。"
          checked={enabled}
          disabled={disabled}
          onChange={(checked) => setValue('enabled', checked, { shouldDirty: true })}
        />
        <div className="pt-3">
          <FormField
            id="replyMode"
            label="回复模式"
            hint={REPLY_MODE_HINTS[replyMode]}
            error={errors.replyMode?.message}
          >
            <Select
              id="replyMode"
              disabled={disabled}
              aria-describedby="replyMode-hint"
              {...register('replyMode')}
            >
              {REPLY_MODE_OPTIONS.map((mode) => (
                <option key={mode} value={mode}>
                  {REPLY_MODE_LABELS[mode]}
                </option>
              ))}
            </Select>
          </FormField>
        </div>
      </Section>

      <Section
        title="数据权限"
        description="这些开关决定机器人能读取和留下什么。开启前请确认群成员知情。"
      >
        <SwitchRow
          id="storeMessages"
          label="保存消息内容"
          description="把该会话的消息写入数据库，用于上下文与后续检索。"
          checked={watch('storeMessages')}
          disabled={disabled}
          onChange={(checked) => setValue('storeMessages', checked, { shouldDirty: true })}
          warning="消息正文将落库，并按留存天数清理；请确认群内成员知晓。"
        />
        <SwitchRow
          id="allowHistoryRead"
          label="允许读取聊天历史"
          description="回复时可以回溯该会话更早的消息。"
          checked={watch('allowHistoryRead')}
          disabled={disabled}
          onChange={(checked) => setValue('allowHistoryRead', checked, { shouldDirty: true })}
          warning="机器人将读取开启前的历史消息，可能包含未预期内容。"
        />
        <SwitchRow
          id="allowMemoryWrite"
          label="允许写入长期记忆"
          description="该会话中的信息可被提取为长期记忆，影响后续回复。"
          checked={watch('allowMemoryWrite')}
          disabled={disabled}
          onChange={(checked) => setValue('allowMemoryWrite', checked, { shouldDirty: true })}
          warning="写入的记忆可能在其它会话被检索（取决于记忆可见性），请先在记忆页审查。"
        />
      </Section>

      <Section
        title="限流与留存"
        description="数值由后端校验，0 表示不限制；留存天数 0 表示不设置过期时间。"
      >
        <div className="grid gap-3 sm:grid-cols-3">
          <NumberField
            id="minReplyIntervalMs"
            label="最小回复间隔"
            unit="ms"
            hint="同一会话两次回复的最小间隔。"
            error={errors.minReplyIntervalMs?.message}
            registration={register('minReplyIntervalMs')}
            disabled={disabled}
          />
          <NumberField
            id="maxRepliesPerHour"
            label="每小时上限"
            unit="次"
            hint="防止刷屏，超出后停止主动回复。"
            error={errors.maxRepliesPerHour?.message}
            registration={register('maxRepliesPerHour')}
            disabled={disabled}
          />
          <NumberField
            id="retentionDays"
            label="消息留存"
            unit="天"
            hint="超过期限的消息可被清理。"
            error={errors.retentionDays?.message}
            registration={register('retentionDays')}
            disabled={disabled}
          />
        </div>
      </Section>

    </div>
  )
}
