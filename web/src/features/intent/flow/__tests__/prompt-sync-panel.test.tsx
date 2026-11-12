import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { PromptSyncPanel } from '../prompt-sync-panel'
import type { PromptSyncStatus } from '../../../../lib/api/contracts'

const base: PromptSyncStatus = {
  promptId: 'intent.participation',
  provider: 'jev',
  configured: false,
  status: 'not_configured',
  reasonCode: 'langfuse_not_configured',
  message: '未配置 Langfuse',
  candidates: [],
}

describe('PromptSyncPanel', () => {
  it('keeps local prompts usable without Langfuse and disables sync', () => {
    render(
      <PromptSyncPanel
        status={base}
        loading={false}
        onRetry={vi.fn()}
        onSync={vi.fn()}
        syncing={false}
        onActivate={vi.fn()}
        activating={false}
      />,
    )
    expect(screen.getByText('未配置（仅本地）')).toBeVisible()
    expect(screen.getByText(/本地版本仍可使用/)).toBeVisible()
    expect(screen.getByRole('button', { name: /同步最新版本/ })).toBeDisabled()
  })

  it('shows candidates and activates the selected draft', () => {
    const onActivate = vi.fn()
    render(
      <PromptSyncPanel
        status={{
          ...base,
          configured: true,
          status: 'ok',
          message: '同步完成',
          active: { version: 2, source: 'langfuse', sourceRef: 'production', status: 'published' },
          candidates: [
            {
              id: 9,
              version: 3,
              name: '远端候选',
              hash: 'sha256:def',
              sourceRef: 'production/v3',
              createdAt: '2026-11-12T08:01:00Z',
            },
          ],
        }}
        loading={false}
        onRetry={vi.fn()}
        onSync={vi.fn()}
        syncing={false}
        onActivate={onActivate}
        activating={false}
      />,
    )
    expect(screen.getByText('已同步')).toBeVisible()
    expect(screen.getByText('production/v3')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: '激活' }))
    expect(onActivate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 9, version: 3, sourceRef: 'production/v3' }),
    )
  })
})
