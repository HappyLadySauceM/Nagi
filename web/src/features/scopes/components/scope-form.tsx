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
  SCOPE_TYPE_LABELS,
  type ReplyMode,
  type ToolCatalogEntry,
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
        <Label htmlFor={id} className="text-xs">
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
    <Card hidden={hidden} className={hidden ? 'hidden' : undefined}>
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

/**
 * Explains why a tool is not usable in the scope being edited.
 * 说明工具在当前编辑的作用域中为何不可用。
 */
function unavailableLabel(tool: ToolCatalogEntry): string {
  if (tool.unavailableReason === 'scope_kind_mismatch') {
    const labels = tool.scopeKinds.map((kind) => SCOPE_TYPE_LABELS[kind])
    if (labels.length > 0) {
      return `仅适用于${labels.join('、')}`
    }
  }
  return '不适用于当前会话'
}

export function ScopeFormFields({ form, disabled = false, catalog, section }: ScopeFormProps) {
  const { register, formState, watch, setValue } = form
  const errors = formState.errors
  const enabled = watch('enabled')
  const replyMode = watch('replyMode')
  const overrides = watch('toolPermissions')

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

      {catalog && catalog.permissions.length > 0 ? (
        <Section
          hidden={Boolean(section && section !== 'capabilities')} title="能力授权"
          description="工具在启动时由后端注册；这里只控制该会话能否使用对应能力，默认值来自服务端。"
        >
          {catalog.permissions.filter((permission) => catalog.tools.some((tool) => tool.applicable && permission.tools.includes(tool.id))).map((permission) => {
            const tools = catalog.tools.filter((tool) => tool.applicable && permission.tools.includes(tool.id))
            const blocked = !catalog.allowSideEffects && tools.some((tool) => tool.sideEffect && tool.id !== 'qq.send_current')
            const unavailable = tools.filter((tool) => !tool.applicable)
            const allUnavailable = tools.length > 0 && unavailable.length === tools.length
            const unavailableReasons = [...new Set(unavailable.map(unavailableLabel))]
            const descriptions = tools
              .map((tool) => {
                const label = tool.description || tool.id
                return tool.applicable ? label : `${label}（${unavailableLabel(tool)}）`
              })
              .join('；')
            const granted = grantedPermission(permission, overrides)
            // Collect every reason that applies: a capability can be blocked by
            // the session kind and by side effects at the same time, and the
            // operator needs both to understand the disabled switch.
            // 汇总所有适用的原因：会话类型与副作用可能同时禁用同一能力，
            // 两个原因都要展示，用户才能理解开关为何不可用。
            const warnings: string[] = [...new Set(tools.flatMap(tool => tool.blockedReasons ?? []))]
            if (tools.some(tool => tool.id === 'qq.send_current') && !watch('storeMessages')) warnings.push('连续发送需要保存消息以维护投递历史。')
            if (!enabled) warnings.push('当前策略尚未启用，会话不会参与对话。')
            if (allUnavailable) {
              warnings.push(`该能力${unavailableReasons.join('、')}，当前会话无法授权。`)
            }
            if (blocked) {
              warnings.push('该能力包含有副作用的工具，当前版本未启用副作用工具，无法在此授权。')
            }
            if (!permission.defaultGranted && granted) {
              warnings.push('开启后模型可在该会话调用对应工具；撤销前请确认影响范围。')
            }
            return (
              <SwitchRow
                key={permission.id}
                id={`tool-${permission.id}`}
                label={tools.map((tool) => ({ "system.current_time": "查询当前时间", "qq.send_current": "连续回复当前会话", "qq.get_group_info": "查询当前群信息", "qq.get_group_member_info": "查询群成员", "qq.mute_group_member": "禁言群成员", "qq.set_group_whole_ban": "全员禁言", "qq.set_group_card": "修改群名片", "qq.kick_group_member": "移出群成员", "qq.leave_current_group": "退出当前群" }[tool.id] ?? tool.id)).join("、")}
                description={
                  `授权：${granted ? '开启' : '关闭'} · 实际状态：${!granted ? '未授权' : warnings.length ? '被阻断' : '可用'} · ${tools.map((tool) => tool.id).join('、')} · ${descriptions || '无工具声明'} · ${
                    permission.defaultGranted ? '默认允许' : '默认拒绝'
                  }`
                }
                checked={granted}
                // Revoking a default-allowed capability that no longer applies
                // persists an explicit false override, and this dialog then
                // disables the switch, so it cannot be re-enabled here.
                // 撤销「默认允许但当前不适用」的能力会写入显式 false 覆盖；
                // 本对话框随即禁用开关，因此本会话内无法再开启。
                disabled={disabled || blocked || (allUnavailable && !granted)}
                onChange={(next) => togglePermission(permission, next)}
                showWarningWhenUnchecked={allUnavailable || blocked}
                warning={warnings.length > 0 ? warnings.join(' ') : undefined}
              />
            )
          })}
          <p className="pt-2 text-meta leading-relaxed text-muted-foreground">
            清单来自后端实际注册结果；未列出的工具，模型无法调用。
          </p>
        </Section>
      ) : null}

      <Section
        hidden={Boolean(section && section !== 'sending')} title="发送设置与留存"
        description="数值由后端校验，0 表示不限制；留存天数 0 表示不设置过期时间。"
      >
        <div className="grid gap-3 sm:grid-cols-3">
          {([
            ['maxMessagesPerRun', '每轮最多消息', '条'],
            ['maxCharsPerMessage', '单条字数上限', '字'],
            ['maxTotalCharsPerRun', '整轮字数上限', '字'],
            ['messageIntervalMs', '气泡间隔', 'ms'],
          ] as const).map(([name, label, unit]) => <NumberField key={name} id={name} label={label} unit={unit} hint={name === 'messageIntervalMs' ? '连续消息之间的最小间隔。' : '对工具发送和最终回复共同生效。'} error={errors[name]?.message} registration={register(name)} disabled={disabled} />)}
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
