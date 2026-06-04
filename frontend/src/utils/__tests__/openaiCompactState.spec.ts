import { describe, expect, it } from 'vitest'
import { resolveOpenAICompactState } from '@/utils/openaiCompactState'

describe('openaiCompactState utils', () => {
  it('hides the compact badge for OpenAI accounts that were never probed', () => {
    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'oauth',
      extra: {}
    })).toBeNull()

    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'apikey',
      extra: null
    })).toBeNull()
  })

  it('uses force mode as an explicit compact state', () => {
    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'oauth',
      extra: { openai_compact_mode: 'force_on' }
    })).toBe('supported')

    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'apikey',
      extra: { openai_compact_mode: 'force_off' }
    })).toBe('unsupported')
  })

  it('uses persisted probe results when present', () => {
    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'oauth',
      extra: { openai_compact_supported: true }
    })).toBe('supported')

    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'apikey',
      extra: { openai_compact_supported: false }
    })).toBe('unsupported')
  })

  it('shows unknown only after a probe ran without a decisive result', () => {
    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'oauth',
      extra: {
        openai_compact_checked_at: '2026-06-04T10:00:00Z',
        openai_compact_last_error: 'network timeout'
      }
    })).toBe('unknown')
  })

  it('ignores non-OpenAI and unsupported account types', () => {
    expect(resolveOpenAICompactState({
      platform: 'claude',
      type: 'oauth',
      extra: { openai_compact_supported: true }
    })).toBeNull()

    expect(resolveOpenAICompactState({
      platform: 'openai',
      type: 'cookie',
      extra: { openai_compact_supported: true }
    })).toBeNull()
  })
})
