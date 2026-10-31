import { Check, Search, MessageCircle, ShieldCheck, Clock, UsersRound, UserRound, LogOut, Pencil, Wrench, type LucideIcon } from 'lucide-react'
import { type ReactNode } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { FormField } from '../../../components/form-field'
import { Input } from '../../../components/ui/input'
import { Label } from '../../../components/ui/label'
import { Select } from '../../../components/ui/select'
import { Switch } from '../../../components/ui/switch'
import {
  REPLY_MODE_HINTS,
  REPLY_MODE_LABELS,
  type ReplyMode,
  type ToolPermission,
  type ToolsCatalog,
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
  showWarningWhenUnchecked?: boolean
}

function SwitchRow({
  id,
  label,
  description,
  checked,
  disabled,
  onChange,
  warning,
  showWarningWhenUnchecked = false,
}: SwitchRowProps) {
  const showWarning = (checked || showWarningWhenUnchecked) && Boolean(warning)
  return (
    <div className="flex items-start justify-between gap-4 border-b border-border/60 py-3 last:border-b-0">
      <div className="min-w-0">
        <Label htmlFor={id} className="text-sm">
          {label}
        </Label>
        <p id={`${id}-hint`} className="mt-0.5 text-meta leading-relaxed text-muted-foreground">
          {description}
        </p>
        {showWarning ? (
          <p
            id={`${id}-warning`}
            className="mt-1.5 rounded-md border border-warning/25 bg-warning/10 px-2 py-1 text-meta leading-relaxed text-warning"
          >
            {warning}
          </p>
        ) : null}
      </div>
      <Switch
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onChange}
        aria-describedby={showWarning ? `${id}-hint ${id}-warning` : `${id}-hint`}
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
  hidden = false,
}: {
  hidden?: boolean
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <section hidden={hidden} className={hidden ? 'hidden' : 'space-y-5'}>
      <header>
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{description}</p>
      </header>
      <div>{children}</div>
    </section>
  )
}

interface ScopeFormProps {
  form: UseFormReturn<ScopeFormValues>
  disabled?: boolean
  /**
   * Tools registered at startup, filtered by the edited scope kind; undefined
   * hides the section.
   * 启动时注册成功的工具，已按当前编辑的会话类型过滤；未加载时隐藏该区块。
   */
  section?: string
  catalog?: ToolsCatalog
}

/**
 * Resolves the effective grant of one permission: an explicit override wins,
 * otherwise the backend default applies.
 * 解析单个权限的生效授予：显式覆盖优先，否则使用后端默认。
 */
function grantedPermission(
  permission: ToolPermission,
  overrides: Record<string, boolean>,
): boolean {
  return overrides[permission.id] ?? permission.defaultGranted
}

const CAPABILITY_PRESENTATION: Record<string, { label: string; description: string; icon: LucideIcon; category: string }> = {
  'system.current_time': { label: '查询当前时间', description: '获取当前日期、时间与时区。', icon: Clock, category: '系统' },
  'qq.send_current': { label: '连续回复当前会话', description: '在当前会话发送消息，遵循发送预算。', icon: MessageCircle, category: '基础' },
  'qq.get_group_info': { label: '查询当前群信息', description: '查看群名称、成员数量等基本资料。', icon: Search, category: '基础' },
  'qq.get_group_member_info': { label: '查询群成员', description: '查看群成员的名片、昵称与角色。', icon: UserRound, category: '基础' },
  'qq.mute_group_member': { label: '禁言群成员', description: '对当前群的指定成员禁言或解除禁言。', icon: ShieldCheck, category: '群管' },
  'qq.set_group_whole_ban': { label: '全员禁言', description: '开启或关闭当前群的全员禁言。', icon: ShieldCheck, category: '群管' },
  'qq.set_group_card': { label: '修改群名片', description: '修改当前群中指定成员的群名片。', icon: Pencil, category: '群管' },
  'qq.kick_group_member': { label: '移出群成员', description: '将指定成员移出当前群。', icon: UsersRound, category: '群管' },
  'qq.leave_current_group': { label: '退出当前群', description: '让机器人退出当前群。', icon: LogOut, category: '群管' },
}

const CAPABILITY_CATEGORIES = [
  { label: '群管', color: 'bg-violet-500/10 text-violet-700 dark:text-violet-300' },
  { label: '基础', color: 'bg-cyan-500/10 text-cyan-700 dark:text-cyan-300' },
  { label: '系统', color: 'bg-blue-500/10 text-blue-700 dark:text-blue-300' },
  { label: '扩展', color: 'bg-secondary text-muted-foreground' },
]

export function ScopeFormFields({ form, disabled = false, catalog, section }: ScopeFormProps) {
  const { register, formState, watch, setValue } = form
  const errors = formState.errors
  const enabled = watch('enabled')
  const replyMode = watch('replyMode')
  const overrides = watch('toolPermissions')
  const capabilities = (catalog?.permissions ?? []).map(permission => {
    const tools = catalog?.tools.filter(tool => tool.applicable && permission.tools.includes(tool.id)) ?? []
    const categories = CAPABILITY_CATEGORIES.filter(category => tools.some(tool => (CAPABILITY_PRESENTATION[tool.id]?.category ?? '扩展') === category.label))
    return { permission, tools, categories }
  }).filter(item => item.tools.length > 0)
    .sort((a, b) => CAPABILITY_CATEGORIES.indexOf(a.categories[0]) - CAPABILITY_CATEGORIES.indexOf(b.categories[0]))


  // Overrides equal to the backend default are removed, so changing the
  // default baseline later still applies to this scope.
  // 与后端默认一致的覆盖会被移除，因此以后调整默认基线仍会作用于该作用域。
  const togglePermission = (permission: ToolPermission, next: boolean) => {
    const updated = { ...overrides }
    if (next === permission.defaultGranted) {
      delete updated[permission.id]
    } else {
      updated[permission.id] = next
    }
    setValue('toolPermissions', updated, { shouldDirty: true })
  }

  return (
    <div className="space-y-3">
      <Section
        hidden={Boolean(section && section !== 'participation')} title="参与控制"
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
        hidden={Boolean(section && section !== 'data')} title="数据权限"
        description="消息内容默认入库，用于上下文与后续检索；这里控制机器人还能读取和留下什么。"
      >
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

      {catalog ? (
        <Section hidden={Boolean(section && section !== 'capabilities')} title="能力授权" description="为当前会话选择允许使用的能力，保存后生效。勾选仅表示授权，实际使用仍受运行条件限制。">
          <div className="overflow-hidden rounded-xl border border-border bg-card">
            <header className="flex items-center justify-between gap-3 px-4 py-4">
              <h3 className="text-sm font-semibold">Agent 能力</h3>
              <span className="rounded-full bg-secondary px-2 py-1 text-caption text-muted-foreground">{capabilities.length} 项能力</span>
            </header>
            {!enabled ? <p className="px-4 pb-3 text-meta text-warning">当前策略尚未启用，会话不会参与对话；勾选后仍需启用并保存策略。</p> : null}
            {capabilities.map(({ permission, tools, categories }) => {
              const label = tools.map(tool => CAPABILITY_PRESENTATION[tool.id]?.label ?? tool.id).join('、')
              const description = tools.map(tool => CAPABILITY_PRESENTATION[tool.id]?.description ?? (tool.description || '服务端已注册的扩展能力。')).join(' ')
              const Icon = CAPABILITY_PRESENTATION[tools[0].id]?.icon ?? (tools.some(tool => tool.riskLevel === 'admin') ? ShieldCheck : Wrench)
              const granted = grantedPermission(permission, overrides)
              const reasons = [...new Set(tools.flatMap(tool => tool.blockedReasons ?? []))]
              const id = `tool-${permission.id}`
              return <article key={permission.id} className="border-t border-border/70 px-3 py-2">
                <label htmlFor={id} className={cn('flex min-h-16 items-center gap-3 rounded-lg px-1 py-2 transition-colors', disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-secondary/50')}>
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-secondary"><Icon className="size-5" aria-hidden /></span>
                  <span className="min-w-0 flex-1"><span className="flex flex-wrap items-center gap-x-2 gap-y-1"><span className="text-sm font-medium">{label}</span>{categories.map(category => <span key={category.label} className={cn('rounded-md px-2 py-0.5 text-caption font-normal', category.color)}>{category.label}</span>)}</span><span id={`${id}-hint`} className="mt-0.5 block text-meta leading-relaxed text-muted-foreground">{description}</span>{granted && (reasons.length > 0 || !enabled) ? <span className="mt-1 block text-caption text-warning">已授权，当前不可用</span> : null}</span>
                  <input id={id} type="checkbox" aria-label={label} aria-describedby={`${id}-hint`} checked={granted} disabled={disabled} onChange={event => togglePermission(permission, event.target.checked)} className="peer sr-only" />
                  <span className={cn('mr-1 flex size-5 shrink-0 items-center justify-center rounded-full border transition-colors peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-offset-4 peer-focus-visible:outline-ring', granted ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground/60')} aria-hidden>{granted ? <Check className="size-3.5" strokeWidth={3} /> : null}</span>
                </label>
                <details className="ml-12 pb-1 text-caption text-muted-foreground"><summary className="w-fit cursor-pointer rounded px-1 py-1 focus-visible:outline-ring" aria-label={`${label}详情`}>详情</summary><div className="space-y-2 break-words px-1 py-2 [overflow-wrap:anywhere]"><p>默认授权：{permission.defaultGranted ? '允许' : '拒绝'}；当前授权：{granted ? '开启' : '关闭'}。</p>{tools.map(tool => <p key={tool.id}><span className="font-mono">{tool.id}</span> · {tool.description || '无工具声明'}</p>)}{reasons.map(reason => <p key={reason} className="text-warning">{reason}</p>)}</div></details>
              </article>
            })}
            {capabilities.length === 0 ? <p className="px-4 py-5 text-meta text-muted-foreground">当前会话没有可授权的能力。</p> : null}
            <p className="border-t border-border/60 px-4 py-3 text-meta leading-relaxed text-muted-foreground">清单来自后端实际注册结果；未列出的工具，模型无法调用。</p>
          </div>
        </Section>
      ) : null}

      <Section
        hidden={Boolean(section && section !== 'sending')} title="发送设置"
        description="数值由后端校验，0 表示不限制。消息留存已在「系统配置」中统一设置。"
      >
        <div className="grid gap-3 sm:grid-cols-2">
          {([
            ['maxMessagesPerRun', '每轮最多消息', '条', '0 表示不限；对工具发送和最终回复共同生效。'],
            ['maxCharsPerMessage', '单条字数上限', '字', '默认 4500，即 QQ 单条文本上限；0 表示不限。'],
            ['maxTotalCharsPerRun', '整轮字数上限', '字', '0 表示不限；对工具发送和最终回复共同生效。'],
            ['messageIntervalMs', '气泡间隔', 'ms', '连续消息之间的最小间隔。'],
          ] as const).map(([name, label, unit, hint]) => <NumberField key={name} id={name} label={label} unit={unit} hint={hint} error={errors[name]?.message} registration={register(name)} disabled={disabled} />)}
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
            hint="防止刷屏，超出后停止主动回复；0 表示不限。"
            error={errors.maxRepliesPerHour?.message}
            registration={register('maxRepliesPerHour')}
            disabled={disabled}
          />
        </div>
      </Section>

    </div>
  )
}
