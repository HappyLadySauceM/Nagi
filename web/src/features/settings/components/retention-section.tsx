import { Archive } from 'lucide-react'
import { NumberField } from './fields'
import { SettingsCard } from './settings-card'
import type { SectionProps } from './types'

/**
 * Global message retention. The cleanup loop runs in the backend and reads this
 * value on every sweep; zero disables cleanup entirely.
 * 全局消息留存。后端清理循环每轮读取该值；0 表示完全不清理。
 */
export function RetentionSection({ draft, errors, updateMessages }: SectionProps) {
  return (
    <SettingsCard
      id="retention"
      title="消息与留存"
      description="统一设置入库消息的保留期限，不再按会话单独配置。"
      icon={Archive}
      tag="全局策略"
      warning="超过期限的消息会连同其决策任务与投递记录一起清理；0 表示不清理。"
    >
      <div className="grid gap-x-6 sm:grid-cols-2">
        <NumberField
          id="messages-retention-days"
          label="消息留存"
          value={draft.messages.retentionDays}
          onChange={(retentionDays) => updateMessages({ retentionDays })}
          unit="天"
          hint="0 表示不清理；按批删除，不影响进行中的会话。"
          error={errors['Messages.RetentionDays']}
        />
      </div>
    </SettingsCard>
  )
}
