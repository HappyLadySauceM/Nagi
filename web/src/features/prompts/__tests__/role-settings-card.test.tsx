import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { apiRequest } from '../../../lib/api/client'
import { RoleSettingsCard } from '../role-settings-card'

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn() }
})

vi.mock('../../settings/queries', () => {
  const data = { settings: { role: { botName: 'Nagi' } }, version: 3 }
  return {
    useSystemSettings: () => ({
      data,
      isLoading: false,
      isError: false,
      error: null,
    }),
  }
})

vi.mock('../../../lib/api/adapters/settings', () => ({
  toSettingsView: (raw: unknown) => raw,
}))

const mockedRequest = vi.mocked(apiRequest)

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('saves the global bot name through the Role settings section', async () => {
  mockedRequest.mockResolvedValue({ settings: { role: { botName: 'Nagi 2' } }, version: 4 })
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RoleSettingsCard />
    </QueryClientProvider>,
  )

  expect(screen.getByLabelText('机器人自称')).toHaveValue('Nagi')
  fireEvent.change(screen.getByLabelText('机器人自称'), { target: { value: 'Nagi 2' } })
  fireEvent.click(screen.getByRole('button', { name: '保存角色设置' }))

  await waitFor(() =>
    expect(mockedRequest).toHaveBeenCalledWith('/api/v1/settings', {
      method: 'PATCH',
      body: { Version: 3, Sections: { Role: { BotName: 'Nagi 2' } } },
    }),
  )
  client.clear()
})
