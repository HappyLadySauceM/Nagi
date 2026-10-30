import type {
  SettingsAgent,
  SettingsAI,
  SettingsCredentials,
  SettingsIntent,
  SettingsMessages,
  SettingsTools,
  SystemSettings,
} from '../../../lib/api/contracts'
import type { SettingsErrors } from '../schema'

/** Props shared by the settings sections. */
export interface SectionProps {
  draft: SystemSettings
  errors: SettingsErrors
  credentials: SettingsCredentials
  updateAI: (patch: Partial<SettingsAI>) => void
  updateIntent: (patch: Partial<SettingsIntent>) => void
  updateAgent: (patch: Partial<SettingsAgent>) => void
  updateMessages: (patch: Partial<SettingsMessages>) => void
  updateTools: (patch: Partial<SettingsTools>) => void
  /** Registered provider ids from the backend. */
  providers: string[]
}
