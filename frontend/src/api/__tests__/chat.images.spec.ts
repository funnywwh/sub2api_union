import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { generateUserChatImages } from '@/api/chat'

function mockFetchResponse(response: Response) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
}

describe('generateUserChatImages', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('preserves OpenAI JSON image output format', async () => {
    mockFetchResponse(new Response(JSON.stringify({
      created: 1710000000,
      output_format: 'webp',
      data: [
        {
          b64_json: 'aGVsbG8=',
          revised_prompt: 'draw a cat'
        }
      ]
    }), {
      status: 200,
      headers: { 'content-type': 'application/json; charset=utf-8' }
    }))

    const result = await generateUserChatImages({
      groupId: 1,
      model: 'gpt-image-2',
      prompt: 'draw a cat'
    })

    expect(result.text).toBe('draw a cat')
    expect(result.images).toEqual([
      {
        url: 'data:image/webp;base64,aGVsbG8=',
        mimeType: 'image/webp'
      }
    ])
  })

  it('parses OpenAI streaming image completed events', async () => {
    mockFetchResponse(new Response(
      'event: image_generation.completed\n' +
        'data: {"type":"image_generation.completed","b64_json":"ZmluYWw=","output_format":"webp"}\n\n' +
        'data: [DONE]\n\n',
      {
        status: 200,
        headers: { 'content-type': 'text/event-stream' }
      }
    ))

    const result = await generateUserChatImages({
      groupId: 1,
      model: 'gpt-image-2',
      prompt: 'draw a cat'
    })

    expect(result.images).toEqual([
      {
        url: 'data:image/webp;base64,ZmluYWw=',
        mimeType: 'image/webp'
      }
    ])
  })

  it('parses raw Responses output item image events', async () => {
    mockFetchResponse(new Response(
      'data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","result":"cmF3","output_format":"png","revised_prompt":"raw prompt"}}\n\n' +
        'data: [DONE]\n\n',
      {
        status: 200,
        headers: { 'content-type': 'text/event-stream' }
      }
    ))

    const result = await generateUserChatImages({
      groupId: 1,
      model: 'gpt-image-2',
      prompt: 'draw a cat'
    })

    expect(result.text).toBe('raw prompt')
    expect(result.images).toEqual([
      {
        url: 'data:image/png;base64,cmF3',
        mimeType: 'image/png'
      }
    ])
  })

  it('parses a final OpenAI streaming image event without a trailing delimiter', async () => {
    mockFetchResponse(new Response(
      'data: {"type":"image_generation.completed","b64_json":"bm8tdGFpbA==","output_format":"webp"}',
      {
        status: 200,
        headers: { 'content-type': 'text/event-stream' }
      }
    ))

    const result = await generateUserChatImages({
      groupId: 1,
      model: 'gpt-image-2',
      prompt: 'draw a cat'
    })

    expect(result.images).toEqual([
      {
        url: 'data:image/webp;base64,bm8tdGFpbA==',
        mimeType: 'image/webp'
      }
    ])
  })

  it('deduplicates repeated raw Responses image events without duplicating revised prompts', async () => {
    mockFetchResponse(new Response(
      'data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","result":"cmF3","output_format":"png","revised_prompt":"raw prompt"}}\n\n' +
        'data: {"type":"response.completed","response":{"output":[{"id":"ig_123","type":"image_generation_call","result":"cmF3","output_format":"png","revised_prompt":"raw prompt"}]}}\n\n' +
        'data: [DONE]\n\n',
      {
        status: 200,
        headers: { 'content-type': 'text/event-stream' }
      }
    ))

    const result = await generateUserChatImages({
      groupId: 1,
      model: 'gpt-image-2',
      prompt: 'draw a cat'
    })

    expect(result.text).toBe('raw prompt')
    expect(result.images).toEqual([
      {
        url: 'data:image/png;base64,cmF3',
        mimeType: 'image/png'
      }
    ])
  })

  it('surfaces raw Responses terminal failure messages', async () => {
    mockFetchResponse(new Response(
      'data: {"type":"response.failed","response":{"error":{"message":"image backend overloaded"}}}\n\n' +
        'data: [DONE]\n\n',
      {
        status: 200,
        headers: { 'content-type': 'text/event-stream' }
      }
    ))

    await expect(generateUserChatImages({
      groupId: 1,
      model: 'gpt-image-2',
      prompt: 'draw a cat'
    })).rejects.toThrow('image backend overloaded')
  })
})
