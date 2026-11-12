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
    expect(screen.getByText('混合版本')).toBeVisible()
    expect(screen.getByText(/部分来源未记录/)).toBeVisible()
  })

  it('reports an incomplete source set when every field is unknown', () => {
    render(
      <ReplaySourcePanel
        replay={{ input: '', semantic: '', prompt: '', policy: '', rules: '', persisted: false }}
      />,
    )
    expect(screen.getByText('来源不完整')).toBeVisible()
    expect(screen.getAllByText('未记录')).toHaveLength(5)
    expect(screen.getByText(/无法判断是否为原执行复现/)).toBeVisible()
  })

  it('only says all-from-snapshot when every field is snapshot', () => {
    render(
      <ReplaySourcePanel
        replay={{
          input: 'snapshot',
          semantic: 'snapshot',
          prompt: 'snapshot',
          policy: 'snapshot',
          rules: 'snapshot',
          persisted: false,
        }}
      />,
    )
    expect(screen.getByText('全部来自快照')).toBeVisible()
    expect(screen.queryByText('未记录')).toBeNull()
  })

  it('marks an all-current result as mixed, never as a snapshot reproduction', () => {
    render(
      <ReplaySourcePanel
        replay={{
          input: 'current',
          semantic: 'current',
          prompt: 'current',
          policy: 'current',
          rules: 'current',
          persisted: false,
        }}
      />,
    )
    expect(screen.getByText('混合版本')).toBeVisible()
    expect(screen.getAllByText('当前')).toHaveLength(5)
    expect(screen.getByText(/不是原执行的完全复现/)).toBeVisible()
  })
})
