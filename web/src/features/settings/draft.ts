import { useState } from 'react'
import type { SystemSettings } from '../../lib/api/contracts'
import type { SettingsErrors } from './schema'

/** Deep copy keeps the stored revision untouched while the draft is edited. */
export function cloneSettings(settings: SystemSettings): SystemSettings {
  return JSON.parse(JSON.stringify(settings)) as SystemSettings
}

/**
 * Draft state of the settings form: the editable copy, the per-field errors a
 * rejected save returned, and the mutation helpers shared by every section.
 * 配置表单的草稿状态：可编辑副本、保存被拒后的字段错误，
 * 以及各分区共用的修改助手。
 */
export function useDraft() {
  const [draft, setDraft] = useState<SystemSettings | null>(null)
  const [serverErrors, setServerErrors] = useState<SettingsErrors>({})

  const reset = (next: SystemSettings) => {
    setDraft(cloneSettings(next))
    setServerErrors({})
  }

  const update = (mutate: (next: SystemSettings) => void) => {
    setDraft((current) => {
      if (!current) {
        return current
      }
      const next = cloneSettings(current)
      mutate(next)
      return next
    })
    // Any edit invalidates the previous server response.
    // 任何编辑都会让上一轮服务端错误失效。
    setServerErrors({})
  }

  return { draft, serverErrors, setServerErrors, reset, update }
}
