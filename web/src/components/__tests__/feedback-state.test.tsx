import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../lib/api/client'
import { FeedbackState } from '../feedback-state'

function apiError(kind: 'unavailable' | 'cancelled'): ApiError {
  return new ApiError({ kind, endpoint: '/readyz', message: 'onebot connection not ready' })
}

describe('FeedbackState', () => {
  it('offers a retry when a dependency is not ready', () => {
    const onRetry = vi.fn()

    render(<FeedbackState variant="error" error={apiError('unavailable')} onRetry={onRetry} />)

    expect(screen.getByText('依赖服务未就绪')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('does not offer a retry for a cancelled request', () => {
    render(<FeedbackState variant="error" error={apiError('cancelled')} onRetry={vi.fn()} />)

    expect(screen.getByText('请求已取消')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })
})
