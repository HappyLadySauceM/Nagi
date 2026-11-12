import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ReplaySourcePanel } from '../replay-source-panel'
import type { IntentReplayInfo } from '../../../../lib/api/contracts'

const mixed: IntentReplayInfo = {
  input: 'snapshot',
  semantic: 'current',
  prompt: 'snapshot',
  policy: 'current',
  rules: 'snapshot',
  persisted: false,
}

describe('ReplaySourcePanel', () => {
  it('marks which parts come from the snapshot and which from current config', () => {
    render(<ReplaySourcePanel replay={mixed} />)
    expect(screen.getByText('混合版本')).toBeVisible()
    expect(screen.getAllByText('当前')).toHaveLength(2)
    expect(screen.getAllByText('快照')).toHaveLength(3)
    expect(screen.getByText(/不是原执行的完全复现/)).toBeVisible()
    expect(screen.getByText(/Persisted=false/)).toBeVisible()
  })

  it('renders nothing when a run carries no replay info', () => {
    const { container } = render(<ReplaySourcePanel replay={undefined} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows unknown values as unrecorded instead of guessing', () => {
    render(<ReplaySourcePanel replay={{ ...mixed, input: '' }} />)
    expect(screen.getByText('未记录')).toBeVisible()
  })
})
