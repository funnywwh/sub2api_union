import { apiClient } from './client'

export interface ChatModel {
  id: string
  type?: string
  object?: string
  display_name?: string
  created_at?: string
  created?: number
  owned_by?: string
}

interface ChatModelListResponse {
  object?: string
  data?: ChatModel[]
}

export interface UserChatMessagePayload {
  role: 'system' | 'user' | 'assistant'
  content: string
}

interface ChatCompletionResponse {
  choices?: Array<{
    message?: {
      content?: unknown
    }
  }>
  error?: {
    message?: string
  }
}

interface ChatCompletionChunk {
  choices?: Array<{
    delta?: {
      content?: unknown
    }
  }>
  error?: {
    message?: string
  }
}

export interface StreamUserChatOptions {
  groupId: number
  model: string
  messages: UserChatMessagePayload[]
  signal?: AbortSignal
  onDelta?: (chunk: string) => void
}

class UserChatRequestError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'UserChatRequestError'
    this.status = status
  }
}

const USER_CHAT_MODELS_URL = '/user/chat/models'
const USER_CHAT_COMPLETIONS_URL = '/api/v1/user/chat/completions'

function createAuthHeaders(accept = 'application/json'): HeadersInit {
  const token = localStorage.getItem('auth_token')
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: accept
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  return headers
}

function normalizeContent(content: unknown): string {
  if (typeof content === 'string') {
    return content
  }

  if (!Array.isArray(content)) {
    return ''
  }

  return content
    .map((part) => {
      if (typeof part === 'string') {
        return part
      }

      if (!part || typeof part !== 'object') {
        return ''
      }

      const record = part as Record<string, unknown>
      if (typeof record.text === 'string') {
        return record.text
      }
      if (record.text && typeof record.text === 'object') {
        const nested = record.text as Record<string, unknown>
        if (typeof nested.value === 'string') {
          return nested.value
        }
      }
      if (typeof record.content === 'string') {
        return record.content
      }
      return ''
    })
    .join('')
}

async function buildChatError(response: Response): Promise<UserChatRequestError> {
  const fallback = `Request failed with status ${response.status}`

  try {
    const contentType = response.headers.get('content-type') || ''
    if (contentType.includes('application/json')) {
      const payload = await response.json() as Record<string, unknown>
      const message =
        (payload.error as { message?: string } | undefined)?.message ||
        (typeof payload.message === 'string' ? payload.message : '') ||
        (typeof payload.detail === 'string' ? payload.detail : '') ||
        fallback
      return new UserChatRequestError(response.status, message)
    }

    const text = (await response.text()).trim()
    return new UserChatRequestError(response.status, text || fallback)
  } catch {
    return new UserChatRequestError(response.status, fallback)
  }
}

function parseSSEData(eventBlock: string): string {
  return eventBlock
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trimStart())
    .join('\n')
}

function extractStreamText(payload: ChatCompletionChunk): string {
  return normalizeContent(payload.choices?.[0]?.delta?.content)
}

function extractResponseText(payload: ChatCompletionResponse): string {
  return normalizeContent(payload.choices?.[0]?.message?.content)
}

export async function listUserChatModels(groupId: number, signal?: AbortSignal): Promise<ChatModel[]> {
  const { data } = await apiClient.get<ChatModelListResponse>(USER_CHAT_MODELS_URL, {
    params: { group_id: groupId },
    signal
  })

  return Array.isArray(data?.data) ? data.data : []
}

export async function streamUserChatCompletion(options: StreamUserChatOptions): Promise<string> {
  const response = await fetch(USER_CHAT_COMPLETIONS_URL, {
    method: 'POST',
    headers: createAuthHeaders('text/event-stream'),
    body: JSON.stringify({
      group_id: options.groupId,
      model: options.model,
      messages: options.messages,
      stream: true
    }),
    signal: options.signal
  })

  if (!response.ok) {
    throw await buildChatError(response)
  }

  const contentType = response.headers.get('content-type') || ''
  if (!response.body || !contentType.includes('text/event-stream')) {
    const payload = await response.json() as ChatCompletionResponse
    if (payload.error?.message) {
      throw new UserChatRequestError(response.status, payload.error.message)
    }

    const text = extractResponseText(payload)
    if (text) {
      options.onDelta?.(text)
    }
    return text
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let accumulated = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) {
      break
    }

    buffer += decoder.decode(value, { stream: true }).replace(/\r/g, '')

    let boundaryIndex = buffer.indexOf('\n\n')
    while (boundaryIndex !== -1) {
      const rawEvent = buffer.slice(0, boundaryIndex)
      buffer = buffer.slice(boundaryIndex + 2)
      boundaryIndex = buffer.indexOf('\n\n')

      const data = parseSSEData(rawEvent)
      if (!data) {
        continue
      }
      if (data === '[DONE]') {
        return accumulated
      }

      const payload = JSON.parse(data) as ChatCompletionChunk
      if (payload.error?.message) {
        throw new UserChatRequestError(response.status, payload.error.message)
      }

      const chunk = extractStreamText(payload)
      if (!chunk) {
        continue
      }

      accumulated += chunk
      options.onDelta?.(chunk)
    }
  }

  const tail = buffer.trim()
  if (tail) {
    const data = parseSSEData(tail)
    if (data && data !== '[DONE]') {
      const payload = JSON.parse(data) as ChatCompletionChunk
      if (payload.error?.message) {
        throw new UserChatRequestError(response.status, payload.error.message)
      }

      const chunk = extractStreamText(payload)
      if (chunk) {
        accumulated += chunk
        options.onDelta?.(chunk)
      }
    }
  }

  return accumulated
}
