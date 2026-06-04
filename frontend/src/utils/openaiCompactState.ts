export type OpenAICompactState = 'supported' | 'unsupported' | 'unknown' | null

export interface OpenAICompactAccountLike {
  platform?: unknown
  type?: unknown
  extra?: Record<string, unknown> | null
}

export const resolveOpenAICompactState = (
  account: OpenAICompactAccountLike
): OpenAICompactState => {
  if (account.platform !== 'openai' || (account.type !== 'oauth' && account.type !== 'apikey')) {
    return null
  }

  const extra = account.extra
  const mode = typeof extra?.openai_compact_mode === 'string' ? extra.openai_compact_mode : 'auto'

  if (mode === 'force_on') return 'supported'
  if (mode === 'force_off') return 'unsupported'

  if (typeof extra?.openai_compact_supported === 'boolean') {
    return extra.openai_compact_supported ? 'supported' : 'unsupported'
  }

  const checkedAt = typeof extra?.openai_compact_checked_at === 'string'
    ? extra.openai_compact_checked_at.trim()
    : ''

  return checkedAt ? 'unknown' : null
}
