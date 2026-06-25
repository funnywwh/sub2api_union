import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { UserSubscription } from '@/types'

const { get } = vi.hoisted(() => ({
  get: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
  },
}))

import { listByUser } from '@/api/admin/subscriptions'

type Assert<T extends true> = T
type IsExact<T, U> = (
  (<G>() => G extends T ? 1 : 2) extends (<G>() => G extends U ? 1 : 2)
    ? ((<G>() => G extends U ? 1 : 2) extends (<G>() => G extends T ? 1 : 2) ? true : false)
    : false
)

type ListByUserResult = Awaited<ReturnType<typeof listByUser>>
const listByUserResultExact: Assert<IsExact<ListByUserResult, UserSubscription[]>> = true
void listByUserResultExact

describe('admin subscriptions api', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('lists user subscriptions using the backend array response shape', async () => {
    const response: UserSubscription[] = [
      {
        id: 1,
        user_id: 99,
        group_id: 2,
        status: 'active',
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-06-25T00:00:00Z',
        updated_at: '2026-06-25T00:00:00Z',
        expires_at: '2026-07-25T00:00:00Z',
      },
    ]
    get.mockResolvedValue({ data: response })

    const result = await listByUser(99)

    expect(get).toHaveBeenCalledWith('/admin/users/99/subscriptions')
    expect(result).toEqual(response)
  })
})
