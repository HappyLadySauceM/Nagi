import { act, fireEvent, render } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Users } from 'lucide-react'
import { forwardRef, useImperativeHandle } from 'react'
import { AnimatedNavIcon } from '../animated-nav-icon'

const mocks = vi.hoisted(() => ({ start: vi.fn(async () => {}), stop: vi.fn(async () => {}), reduced: false }))
vi.mock('motion/react', () => ({ useReducedMotion: () => mocks.reduced }))
vi.mock('../animated-icons/users', () => ({
  UsersIcon: forwardRef(function TestIcon(_, ref) {
    useImperativeHandle(ref, () => ({ startAnimation: mocks.start, stopAnimation: mocks.stop }))
    return <svg data-testid="animated-icon" />
  }),
}))
beforeEach(() => { vi.useFakeTimers(); mocks.start.mockClear(); mocks.stop.mockClear(); mocks.reduced = false })
afterEach(() => vi.useRealTimers())
it('loops only while selected, ignores pointer leave, and cancels on deselection and unmount', async () => {
  const { rerender, getByRole, unmount } = render(<button><AnimatedNavIcon icon={Users} active /></button>)
  await act(async () => {})
  expect(mocks.start).toHaveBeenCalledTimes(1)
  fireEvent.pointerLeave(getByRole('button'))
  await act(async () => { await vi.advanceTimersByTimeAsync(600) })
  expect(mocks.start).toHaveBeenCalledTimes(2)
  rerender(<button><AnimatedNavIcon icon={Users} /></button>)
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(mocks.start).toHaveBeenCalledTimes(2)
  fireEvent.focus(getByRole('button'))
  await act(async () => {})
  expect(mocks.start).toHaveBeenCalledTimes(3)
  unmount()
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(mocks.start).toHaveBeenCalledTimes(3)
})
it('pauses when hidden and resumes when visible', async () => {
  const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  const { unmount } = render(<button><AnimatedNavIcon icon={Users} active /></button>)
  await act(async () => {})
  hidden.mockReturnValue(true)
  fireEvent(document, new Event('visibilitychange'))
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(mocks.start).toHaveBeenCalledTimes(1)
  hidden.mockReturnValue(false)
  fireEvent(document, new Event('visibilitychange'))
  await act(async () => {})
  expect(mocks.start).toHaveBeenCalledTimes(2)
  unmount(); hidden.mockRestore()
})
it('keeps the selected icon static with reduced motion', () => {
  mocks.reduced = true
  const { queryByTestId } = render(<button><AnimatedNavIcon icon={Users} active /></button>)
  expect(queryByTestId('animated-icon')).toBeNull()
  expect(mocks.start).not.toHaveBeenCalled()
})
