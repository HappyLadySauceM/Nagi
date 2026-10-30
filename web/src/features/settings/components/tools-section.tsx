import { ShieldCheck, Wrench } from 'lucide-react'
import { Switch } from '../../../components/ui/switch'
import { Label } from '../../../components/ui/label'
import { NumberField } from './fields'
import { SettingsCard } from './settings-card'
import type { SectionProps } from './types'

/**
 * Tool call limits and permission defaults. Providers carry no enable list:
 * everything registered at startup is available, and the conversation's grant
 * decides whether the model ever sees a given tool.
 * 工具调用限制与权限默认值。提供方没有启用列表：
 * 启动时注册的全部可用，会话的授权决定模型是否能看到某个工具。
 */
export function ToolsSection({
  draft,
  errors,
  updateTools,
  providers,
  permissions,
}: SectionProps) {
  const togglePermission = (permission: string, granted: boolean) => {
    updateTools({
      permissionDefaults: { ...draft.tools.permissionDefaults, [permission]: granted },
    })
  }

  // Permissions stored before a catalogue change stay editable instead of
  // disappearing until the next successful load.
  const permissionRows = [
    ...permissions,
    ...Object.keys(draft.tools.permissionDefaults)
      .filter((id) => !permissions.some((permission) => permission.id === id))
      .map((id) => ({ id, tools: [] as string[] })),
  ]

  return (
    <div className="space-y-4">
      <SettingsCard
        id="tools"
        title="工具与能力"
        description="提供方随进程启动全部注册；会话是否可使用由联系人页的「能力授权」决定。"
        icon={Wrench}
        tag="全部可用"
      >
        <div className="py-2">
          <Label className="text-xs">已注册的提供方</Label>
          <div className="mt-2 flex flex-wrap gap-2">
            {providers.length === 0 ? (
              <p className="text-meta text-muted-foreground">当前进程没有注册任何工具提供方。</p>
            ) : (
              providers.map((provider) => (
                <span
                  key={provider}
                  className="rounded-full border border-border bg-secondary px-3 py-1.5 font-mono text-meta text-muted-foreground"
                >
                  {provider}
                </span>
              ))
            )}
          </div>
          <p className="mt-2 text-meta leading-relaxed text-muted-foreground">
            这里没有启用开关：进程注册多少提供方就全部可用，模型能否调用只看对应会话是否授权。
          </p>
        </div>
        <div className="grid gap-x-6 sm:grid-cols-2">
          <NumberField
            id="tools-timeout"
            label="单次调用超时"
            value={draft.tools.timeoutSeconds}
            onChange={(timeoutSeconds) => updateTools({ timeoutSeconds })}
            unit="秒"
            hint="超时的调用按失败返回，不会阻塞整个回合。"
            error={errors['Tools.TimeoutSeconds']}
          />
          <NumberField
            id="tools-max-result"
            label="结果字节上限"
            value={draft.tools.maxResultBytes}
            onChange={(maxResultBytes) => updateTools({ maxResultBytes })}
            unit="字节"
            hint="超出即拒绝，避免超长结果挤占上下文。"
            error={errors['Tools.MaxResultBytes']}
          />
        </div>
      </SettingsCard>

      <SettingsCard
        id="permissions"
        title="权限默认值"
        description="会话没有单独设置时使用的默认授权；只读工具默认允许，其余默认拒绝。"
        icon={ShieldCheck}
        tag="兜底授权"
        warning="这里只是默认值。调整后不会覆盖已经单独保存过的会话策略。"
      >
        {permissionRows.length === 0 ? (
          <p className="py-2 text-meta text-muted-foreground">
            后端没有返回任何权限声明，先确认工具提供方已启用。
          </p>
        ) : (
          <div className="divide-y divide-border/60">
            {permissionRows.map((permission) => {
              const granted = draft.tools.permissionDefaults[permission.id] ?? false
              return (
                <div
                  key={permission.id}
                  className="flex items-start justify-between gap-4 py-3"
                >
                  <div className="min-w-0">
                    <Label htmlFor={`permission-${permission.id}`} className="font-mono text-meta">
                      {permission.id}
                    </Label>
                    <p className="mt-0.5 text-caption leading-relaxed text-muted-foreground">
                      {permission.tools.length > 0
                        ? `覆盖工具：${permission.tools.join('、')}`
                        : '该权限对应的工具当前未注册'}
                    </p>
                  </div>
                  <Switch
                    id={`permission-${permission.id}`}
                    checked={granted}
                    onCheckedChange={(next) => togglePermission(permission.id, next)}
                  />
                </div>
              )
            })}
          </div>
        )}
        {errors['Tools.PermissionDefaults'] ? (
          <p role="alert" className="mt-1 text-meta text-destructive">
            {errors['Tools.PermissionDefaults']}
          </p>
        ) : null}
      </SettingsCard>
    </div>
  )
}
