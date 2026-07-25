/**
 * URLs for requests that are not made through the shared Axios client.
 *
 * The web deployment deliberately keeps relative URLs so it can be served by
 * the Go backend. A Cordova build supplies VITE_API_BASE_URL instead, and can
 * override it locally from the login screen without rebuilding the APK.
 */
const RUNTIME_API_BASE_URL_STORAGE_KEY = 'sub2api.runtime_api_base_url'
export const API_BASE_URL_CHANGE_EVENT = 'sub2api:api-base-url-changed'

const buildApiBaseUrl = import.meta.env.VITE_API_BASE_URL || '/api/v1'

function readRuntimeApiBaseUrl(): string | null {
  if (typeof localStorage === 'undefined') return null

  try {
    const value = localStorage.getItem(RUNTIME_API_BASE_URL_STORAGE_KEY)?.trim()
    if (!value) return null
    return normalizeApiBaseUrl(value)
  } catch {
    try {
      localStorage.removeItem(RUNTIME_API_BASE_URL_STORAGE_KEY)
    } catch {
      // Ignore storage cleanup failures.
    }
    return null
  }
}

/** The active API base URL. This is a live binding so callers see runtime changes. */
export let API_BASE_URL = readRuntimeApiBaseUrl() || buildApiBaseUrl

export function isRemoteApiBaseUrl(): boolean {
  return /^https?:\/\//i.test(API_BASE_URL)
}

export function normalizeApiBaseUrl(value: string): string {
  const url = new URL(value.trim())
  if (url.protocol !== 'https:') {
    throw new Error('Only HTTPS API URLs are supported')
  }

  url.hash = ''
  url.search = ''
  const pathname = url.pathname.replace(/\/+$/, '')
  if (!pathname.endsWith('/api/v1')) {
    url.pathname = `${pathname}/api/v1`
  } else {
    url.pathname = pathname
  }

  return url.toString().replace(/\/$/, '')
}

/**
 * Persist and activate a user-selected mobile API URL.
 *
 * A root server URL is accepted and normalized to the management API path.
 */
export function setRuntimeApiBaseUrl(value: string): string {
  const normalized = normalizeApiBaseUrl(value)

  try {
    localStorage.setItem(RUNTIME_API_BASE_URL_STORAGE_KEY, normalized)
  } catch {
    // Keep the URL active for the current session when local storage is unavailable.
  }

  API_BASE_URL = normalized
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(API_BASE_URL_CHANGE_EVENT))
  }
  return normalized
}

export function serverUrl(path: string): string {
  if (!isRemoteApiBaseUrl()) return path

  const apiUrl = new URL(API_BASE_URL)
  return new URL(path, apiUrl.origin).toString()
}
