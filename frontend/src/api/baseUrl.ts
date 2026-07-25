/**
 * URLs for requests that are not made through the shared Axios client.
 *
 * The web deployment deliberately keeps relative URLs so it can be served by
 * the Go backend.  A Cordova build supplies VITE_API_BASE_URL instead, in
 * which case relative URLs must be resolved against the remote API origin
 * rather than the app's file:// origin.
 */
export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1'

export function serverUrl(path: string): string {
  if (!import.meta.env.VITE_API_BASE_URL) return path

  const apiUrl = new URL(API_BASE_URL)
  return new URL(path, apiUrl.origin).toString()
}
