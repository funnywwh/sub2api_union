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

export interface UserChatImageURL {
  url: string
  detail?: 'auto' | 'low' | 'high'
}

export interface UserChatContentPart {
  type: 'text' | 'image_url'
  text?: string
  image_url?: UserChatImageURL
}

export interface UserChatMessagePayload {
  role: 'system' | 'user' | 'assistant'
  content: string | UserChatContentPart[]
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

export interface UserChatGeneratedImage {
  url: string
  mimeType: string
}

export interface GenerateUserChatImagesOptions {
  groupId: number
  model: string
  prompt: string
  signal?: AbortSignal
}

export interface GenerateUserChatImagesResult {
  images: UserChatGeneratedImage[]
  text: string
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
const USER_CHAT_IMAGES_URL = '/api/v1/user/chat/images'

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
      const type = typeof record.type === 'string' ? record.type : ''

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

      const nestedImageURL = record.image_url && typeof record.image_url === 'object'
        ? (record.image_url as Record<string, unknown>).url
        : null
      const imageURL = typeof record.image_url === 'string'
        ? record.image_url
        : typeof nestedImageURL === 'string'
          ? nestedImageURL
          : ''
      if (imageURL) {
        return `\n![${type || 'image'}](${imageURL})\n`
      }

      const fileURL = [
        record.download_url,
        record.file_url,
        record.url
      ].find((value): value is string => typeof value === 'string' && value.trim().length > 0) || ''
      if (fileURL) {
        return `\n[${type || 'attachment'}](${fileURL})\n`
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

function mimeTypeFromImageFormat(format: unknown): string {
  const normalized = typeof format === 'string' ? format.trim().toLowerCase() : ''
  if (!normalized) {
    return 'image/png'
  }
  if (normalized.startsWith('image/')) {
    return normalized
  }
  switch (normalized) {
    case 'jpg':
    case 'jpeg':
      return 'image/jpeg'
    case 'webp':
      return 'image/webp'
    case 'png':
      return 'image/png'
    default:
      return 'image/png'
  }
}

function mimeTypeFromImageURL(url: string, fallbackFormat?: unknown): string {
  const dataURLMatch = url.match(/^data:([^;,]+)[;,]/i)
  if (dataURLMatch?.[1]) {
    return dataURLMatch[1].toLowerCase()
  }
  return mimeTypeFromImageFormat(fallbackFormat)
}

function appendGeneratedImage(
  result: GenerateUserChatImagesResult,
  seenImageUrls: Set<string>,
  url: string,
  mimeType: string
): boolean {
  const trimmedURL = url.trim()
  if (!trimmedURL || seenImageUrls.has(trimmedURL)) {
    return false
  }
  seenImageUrls.add(trimmedURL)
  result.images.push({ url: trimmedURL, mimeType })
  return true
}

function appendOpenAIRevisedPrompt(result: GenerateUserChatImagesResult, prompt: string) {
  const revisedPrompt = prompt.trim()
  if (!revisedPrompt) {
    return
  }
  result.text = `${result.text}\n\n${revisedPrompt}`.trim()
}

function appendOpenAIImageRecord(
  record: Record<string, unknown>,
  result: GenerateUserChatImagesResult,
  seenImageUrls: Set<string>,
  fallbackFormat?: unknown
) {
  const revisedPrompt = typeof record.revised_prompt === 'string' ? record.revised_prompt.trim() : ''
  const outputFormat = record.output_format ?? fallbackFormat

  if (typeof record.url === 'string' && record.url.trim()) {
    const appended = appendGeneratedImage(
      result,
      seenImageUrls,
      record.url,
      mimeTypeFromImageURL(record.url, outputFormat)
    )
    if (appended) {
      appendOpenAIRevisedPrompt(result, revisedPrompt)
    }
    return
  }

  const base64Image = typeof record.b64_json === 'string' && record.b64_json.trim()
    ? record.b64_json.trim()
    : typeof record.result === 'string'
      ? record.result.trim()
      : ''

  if (base64Image) {
    const mimeType = mimeTypeFromImageFormat(outputFormat)
    const appended = appendGeneratedImage(
      result,
      seenImageUrls,
      `data:${mimeType};base64,${base64Image}`,
      mimeType
    )
    if (appended) {
      appendOpenAIRevisedPrompt(result, revisedPrompt)
    }
    return
  }

  appendOpenAIRevisedPrompt(result, revisedPrompt)
}

function getNestedString(record: Record<string, unknown>, path: string[]): string {
  let current: unknown = record
  for (const segment of path) {
    if (!current || typeof current !== 'object') {
      return ''
    }
    current = (current as Record<string, unknown>)[segment]
  }
  return typeof current === 'string' ? current.trim() : ''
}

function extractImageEventErrorMessage(payload: Record<string, unknown>): string {
  const directError = payload.error
  if (directError && typeof directError === 'object') {
    const rawMessage = (directError as Record<string, unknown>).message
    const message = typeof rawMessage === 'string' ? rawMessage.trim() : ''
    return message || 'Image generation failed'
  }

  const eventType = typeof payload.type === 'string' ? payload.type : ''
  if (
    eventType !== 'response.failed' &&
    eventType !== 'response.incomplete' &&
    eventType !== 'response.cancelled' &&
    eventType !== 'response.canceled'
  ) {
    return ''
  }

  for (const path of [
    ['response', 'error', 'message'],
    ['response', 'incomplete_details', 'reason'],
    ['message']
  ]) {
    const message = getNestedString(payload, path)
    if (message) {
      return message
    }
  }

  switch (eventType) {
    case 'response.failed':
      return 'Image generation failed'
    case 'response.incomplete':
      return 'Image generation incomplete'
    case 'response.cancelled':
    case 'response.canceled':
      return 'Image generation cancelled'
    default:
      return ''
  }
}

function extractOpenAIImageResult(payload: Record<string, unknown>): GenerateUserChatImagesResult {
  const result: GenerateUserChatImagesResult = { images: [], text: '' }
  const seenImageUrls = new Set<string>()
  const data = Array.isArray(payload.data) ? payload.data : []
  const fallbackFormat = payload.output_format

  for (const item of data) {
    if (!item || typeof item !== 'object') {
      continue
    }

    appendOpenAIImageRecord(item as Record<string, unknown>, result, seenImageUrls, fallbackFormat)
  }

  return result
}

function appendGeminiContentPart(
  part: Record<string, unknown>,
  result: GenerateUserChatImagesResult,
  seenImageUrls: Set<string>
) {
  const text = typeof part.text === 'string' ? part.text : ''
  if (text) {
    result.text = `${result.text}${text}`.trim()
  }

  const inlineData = part.inlineData
  if (!inlineData || typeof inlineData !== 'object') {
    return
  }

  const inlineRecord = inlineData as Record<string, unknown>
  const mimeType = typeof inlineRecord.mimeType === 'string' ? inlineRecord.mimeType : 'image/*'
  const data = typeof inlineRecord.data === 'string' ? inlineRecord.data.trim() : ''
  if (!data || !mimeType.toLowerCase().startsWith('image/')) {
    return
  }

  const url = `data:${mimeType};base64,${data}`
  if (seenImageUrls.has(url)) {
    return
  }

  seenImageUrls.add(url)
  result.images.push({ url, mimeType })
}

function processGeminiEvent(
  payload: Record<string, unknown>,
  result: GenerateUserChatImagesResult,
  seenImageUrls: Set<string>
) {
  const message = extractImageEventErrorMessage(payload)
  if (message) {
    throw new UserChatRequestError(500, message)
  }

  const response = payload.response
  const root = response && typeof response === 'object'
    ? response as Record<string, unknown>
    : payload

  const candidates = Array.isArray(root.candidates) ? root.candidates : []
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== 'object') {
      continue
    }

    const candidateRecord = candidate as Record<string, unknown>
    const content = candidateRecord.content
    if (!content || typeof content !== 'object') {
      continue
    }

    const parts = Array.isArray((content as Record<string, unknown>).parts)
      ? (content as Record<string, unknown>).parts as unknown[]
      : []
    for (const part of parts) {
      if (!part || typeof part !== 'object') {
        continue
      }
      appendGeminiContentPart(part as Record<string, unknown>, result, seenImageUrls)
    }
  }
}

function processOpenAIImageEvent(
  payload: Record<string, unknown>,
  result: GenerateUserChatImagesResult,
  seenImageUrls: Set<string>
) {
  const message = extractImageEventErrorMessage(payload)
  if (message) {
    throw new UserChatRequestError(500, message)
  }

  const type = typeof payload.type === 'string' ? payload.type : ''
  if (
    type === 'image_generation.completed' ||
    type === 'image_edit.completed' ||
    type === 'response.image_generation_call.completed' ||
    type === 'response.image_generation_call.done'
  ) {
    appendOpenAIImageRecord(payload, result, seenImageUrls)
    return
  }

  if (type === 'response.output_item.done') {
    const item = payload.item
    if (item && typeof item === 'object') {
      const record = item as Record<string, unknown>
      if (record.type === 'image_generation_call') {
        appendOpenAIImageRecord(record, result, seenImageUrls)
      }
    }
    return
  }

  if (type === 'response.completed') {
    const response = payload.response
    const output = response && typeof response === 'object'
      ? (response as Record<string, unknown>).output
      : null
    if (!Array.isArray(output)) {
      return
    }
    for (const item of output) {
      if (!item || typeof item !== 'object') {
        continue
      }
      const record = item as Record<string, unknown>
      if (record.type === 'image_generation_call') {
        appendOpenAIImageRecord(record, result, seenImageUrls)
      }
    }
  }
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

export async function generateUserChatImages(
  options: GenerateUserChatImagesOptions
): Promise<GenerateUserChatImagesResult> {
  const response = await fetch(USER_CHAT_IMAGES_URL, {
    method: 'POST',
    headers: createAuthHeaders('application/json, text/event-stream'),
    body: JSON.stringify({
      group_id: options.groupId,
      model: options.model,
      prompt: options.prompt
    }),
    signal: options.signal
  })

  if (!response.ok) {
    throw await buildChatError(response)
  }

  const contentType = response.headers.get('content-type') || ''
  if (contentType.includes('application/json')) {
    const payload = await response.json() as Record<string, unknown>
    if ((payload.error as { message?: string } | undefined)?.message) {
      throw new UserChatRequestError(response.status, (payload.error as { message: string }).message)
    }
    return extractOpenAIImageResult(payload)
  }

  if (!contentType.includes('text/event-stream') || !response.body) {
    throw new UserChatRequestError(response.status, 'Unsupported image generation response')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const result: GenerateUserChatImagesResult = { images: [], text: '' }
  const seenImageUrls = new Set<string>()

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
      if (!data || data === '[DONE]') {
        continue
      }

      const payload = JSON.parse(data) as Record<string, unknown>
      processOpenAIImageEvent(payload, result, seenImageUrls)
      processGeminiEvent(payload, result, seenImageUrls)
    }
  }

  const tail = buffer.trim()
  if (tail) {
    const data = parseSSEData(tail)
    if (data && data !== '[DONE]') {
      const payload = JSON.parse(data) as Record<string, unknown>
      processOpenAIImageEvent(payload, result, seenImageUrls)
      processGeminiEvent(payload, result, seenImageUrls)
    }
  }

  return {
    images: result.images,
    text: result.text.trim()
  }
}
