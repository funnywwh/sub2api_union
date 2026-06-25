import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post, put } = vi.hoisted(() => ({
  post: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post,
    put,
  },
}))

import { transferApiKey, updateApiKeyGroup } from '@/api/admin/apiKeys'

describe('admin api keys api', () => {
  beforeEach(() => {
    post.mockReset()
    put.mockReset()
  })

  it('updates an API key group with the backend-compatible payload', async () => {
    const response = { api_key: { id: 10 }, auto_granted_group_access: false }
    put.mockResolvedValue({ data: response })

    const result = await updateApiKeyGroup(10, null)

    expect(put).toHaveBeenCalledWith('/admin/api-keys/10', { group_id: 0 })
    expect(result).toEqual(response)
  })

  it('transfers an API key to the selected target user', async () => {
    const response = { api_key: { id: 10, user_id: 99, group_id: 2 }, auto_granted_group_access: false }
    post.mockResolvedValue({ data: response })

    const result = await transferApiKey(10, 99, 2, 'source-user')

    expect(post).toHaveBeenCalledWith('/admin/api-keys/10/transfer', {
      target_user_id: 99,
      group_id: 2,
      name: 'source-user',
    })
    expect(result).toEqual(response)
  })
})
