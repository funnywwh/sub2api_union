import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

describe('runtime API base URL', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.unstubAllEnvs()
    vi.resetModules()
  })

  afterEach(() => {
    localStorage.clear()
    vi.unstubAllEnvs()
    vi.resetModules()
  })

  it('keeps the relative API path for the regular web deployment', async () => {
    vi.stubEnv('VITE_API_BASE_URL', '')
    const { API_BASE_URL, isRemoteApiBaseUrl, serverUrl } = await import('@/api/baseUrl')

    expect(API_BASE_URL).toBe('/api/v1')
    expect(isRemoteApiBaseUrl()).toBe(false)
    expect(serverUrl('/setup/status')).toBe('/setup/status')
  })

  it('normalizes and persists a mobile server URL', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://build.example/api/v1')
    const baseUrl = await import('@/api/baseUrl')

    expect(baseUrl.setRuntimeApiBaseUrl('https://mobile.example/')).toBe(
      'https://mobile.example/api/v1'
    )
    expect(baseUrl.API_BASE_URL).toBe('https://mobile.example/api/v1')
    expect(baseUrl.isRemoteApiBaseUrl()).toBe(true)
    expect(baseUrl.serverUrl('/setup/status')).toBe('https://mobile.example/setup/status')
  })

  it('rejects insecure server URLs', async () => {
    const { setRuntimeApiBaseUrl } = await import('@/api/baseUrl')

    expect(() => setRuntimeApiBaseUrl('http://mobile.example')).toThrow(
      'Only HTTPS API URLs are supported'
    )
  })
})
