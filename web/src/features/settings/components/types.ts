import type {
  SettingsAgent,
  SettingsAI,
  SettingsCredentials,
  SettingsIntent,
  SettingsSending,
  SettingsTools,
  SystemSettings,
} from '../../../lib/api/contracts'
import type { SettingsErrors } from '../schema'

/** Props shared by the three settings sections. */
export interface SectionProps {
  draft: SystemSettings
  errors: SettingsErrors
  credentials: SettingsCredentials
  updateAI: (patch: Partial<SettingsAI>) => void
  updateIntent: (patch: Partial<SettingsIntent>) => void
  updateAgent: (patch: Partial<SettingsAgent>) => void
  updateSending: (patch: Partial<SettingsSending>) => void
  updateTools: (patch: Partial<SettingsTools>) => void
  /** Registered provider ids from the backend. */
  providers: string[]
  /** Known permissions with the tools they gate, from the tool catalogue. */
  permissions: Array<{ id: string; tools: string[] }>
}
