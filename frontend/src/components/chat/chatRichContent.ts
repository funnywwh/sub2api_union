import { sanitizeUrl } from '@/utils/url'

export type ChatAssetKind = 'pdf' | 'presentation' | 'document'

export interface ChatDocumentAsset {
  kind: ChatAssetKind
  url: string
  label: string
  extension: string
  previewUrl: string
  previewable: boolean
}

export interface ExplicitChatDocumentAssetInput {
  kind: ChatAssetKind
  url: string
  label: string
  extension?: string
}

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif'])
const PDF_EXTENSIONS = new Set(['pdf'])
const PRESENTATION_EXTENSIONS = new Set(['ppt', 'pptx', 'key'])
const DOCUMENT_EXTENSIONS = new Set(['md', 'txt', 'doc', 'docx', 'xls', 'xlsx'])
const RAW_HTTP_URL_PATTERN = /https?:\/\/[^\s<>"')\]]+/gi
const RAW_RELATIVE_ASSET_PATTERN = /\/[^\s<>"')\]]+\.(?:png|jpe?g|gif|webp|svg|bmp|avif|pdf|ppt|pptx|key|md|txt|doc|docx|xls|xlsx)(?:\?[^\s<>"')\]]*)?/gi
const RAW_DATA_IMAGE_PATTERN = /data:image\/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+/gi
const MARKDOWN_LINK_PATTERN = /!?\[[^\]]*]\(([^)\s]+(?:\?[^)\s]+)?)\)/g
const STANDALONE_MARKDOWN_LINK_PATTERN = /^(!?)\[([^\]]*)\]\(([^)\s]+(?:\?[^)\s]+)?)\)$/

function trimTrailingUrlPunctuation(value: string): string {
  return value.replace(/[),.;!?]+$/g, '')
}

function extractExtension(value: string): string {
  if (value.startsWith('data:image/')) {
    return value.slice('data:image/'.length).split(';')[0]?.toLowerCase() || 'image'
  }

  try {
    const parsed = value.startsWith('http://') || value.startsWith('https://')
      ? new URL(value)
      : new URL(value, 'https://placeholder.local')
    const pathname = parsed.pathname || ''
    const segment = pathname.split('/').pop() || ''
    const extension = segment.includes('.') ? segment.split('.').pop() : ''
    return (extension || '').toLowerCase()
  } catch {
    return ''
  }
}

function resolveAssetKind(value: string): ChatAssetKind | 'image' | null {
  if (value.startsWith('data:image/')) {
    return 'image'
  }

  const extension = extractExtension(value)
  if (!extension) {
    return null
  }
  if (IMAGE_EXTENSIONS.has(extension)) {
    return 'image'
  }
  if (PDF_EXTENSIONS.has(extension)) {
    return 'pdf'
  }
  if (PRESENTATION_EXTENSIONS.has(extension)) {
    return 'presentation'
  }
  if (DOCUMENT_EXTENSIONS.has(extension)) {
    return 'document'
  }
  return null
}

function getSanitizedAssetUrl(rawValue: string): string {
  const trimmed = trimTrailingUrlPunctuation(rawValue.trim())
  const kind = resolveAssetKind(trimmed)
  if (kind === 'image') {
    return sanitizeUrl(trimmed, { allowRelative: true, allowDataUrl: true, allowBlobUrl: true })
  }
  return sanitizeUrl(trimmed, { allowRelative: true, allowBlobUrl: true })
}

function extractStandaloneAssetLine(line: string): string {
  const trimmed = line.trim()
  if (!trimmed) {
    return ''
  }

  const markdownMatch = trimmed.match(STANDALONE_MARKDOWN_LINK_PATTERN)
  if (markdownMatch) {
    const sanitized = getSanitizedAssetUrl(markdownMatch[3] || '')
    return sanitized && resolveAssetKind(sanitized) ? sanitized : ''
  }

  const sanitized = getSanitizedAssetUrl(trimmed)
  return sanitized && resolveAssetKind(sanitized) ? sanitized : ''
}

function buildLinkLabel(url: string): string {
  if (url.startsWith('data:image/')) {
    return 'image'
  }

  try {
    const parsed = url.startsWith('http://') || url.startsWith('https://')
      ? new URL(url)
      : new URL(url, 'https://placeholder.local')
    const segment = parsed.pathname.split('/').pop() || ''
    return decodeURIComponent(segment || parsed.pathname || url)
  } catch {
    return url
  }
}

function collectCandidateUrls(content: string): string[] {
  const urls = new Set<string>()

  for (const match of content.matchAll(MARKDOWN_LINK_PATTERN)) {
    const candidate = getSanitizedAssetUrl(match[1] || '')
    if (candidate) {
      urls.add(candidate)
    }
  }

  for (const match of content.matchAll(RAW_HTTP_URL_PATTERN)) {
    const candidate = getSanitizedAssetUrl(match[0] || '')
    if (candidate) {
      urls.add(candidate)
    }
  }

  for (const match of content.matchAll(RAW_RELATIVE_ASSET_PATTERN)) {
    const candidate = getSanitizedAssetUrl(match[0] || '')
    if (candidate) {
      urls.add(candidate)
    }
  }

  for (const match of content.matchAll(RAW_DATA_IMAGE_PATTERN)) {
    const candidate = getSanitizedAssetUrl(match[0] || '')
    if (candidate) {
      urls.add(candidate)
    }
  }

  return [...urls]
}

function buildPreviewUrl(kind: ChatAssetKind, url: string): string {
  if (kind === 'pdf') {
    return url
  }

  if (
    kind === 'presentation' &&
    (url.startsWith('http://') || url.startsWith('https://'))
  ) {
    return `https://view.officeapps.live.com/op/embed.aspx?src=${encodeURIComponent(url)}`
  }

  return ''
}

export function buildDocumentAssetsFromAttachments(
  attachments: ExplicitChatDocumentAssetInput[]
): ChatDocumentAsset[] {
  return attachments
    .filter((attachment) => Boolean(attachment.url))
    .map((attachment) => ({
      kind: attachment.kind,
      url: attachment.url,
      label: attachment.label,
      extension: attachment.extension || extractExtension(attachment.url) || attachment.kind,
      previewUrl: buildPreviewUrl(attachment.kind, attachment.url),
      previewable: Boolean(buildPreviewUrl(attachment.kind, attachment.url))
    }))
}

export function buildRenderableMarkdown(content: string): string {
  return content
    .split('\n')
    .map((line) => {
      const trimmed = line.trim()
      const assetUrl = extractStandaloneAssetLine(line)
      if (!assetUrl) {
        return line
      }

      const kind = resolveAssetKind(assetUrl)
      const markdownMatch = trimmed.match(STANDALONE_MARKDOWN_LINK_PATTERN)
      const existingLabel = markdownMatch?.[2]?.trim() || buildLinkLabel(assetUrl)

      if (kind === 'image') {
        return `![${existingLabel}](${assetUrl})`
      }

      if (markdownMatch) {
        return line
      }

      return `[${buildLinkLabel(assetUrl)}](${assetUrl})`
    })
    .join('\n')
}

export function extractDocumentAssets(content: string): ChatDocumentAsset[] {
  return collectCandidateUrls(content)
    .map((url) => {
      const kind = resolveAssetKind(url)
      if (!kind || kind === 'image') {
        return null
      }

      const extension = extractExtension(url)
      const previewUrl = buildPreviewUrl(kind, url)
      return {
        kind,
        url,
        label: buildLinkLabel(url),
        extension: extension || kind,
        previewUrl,
        previewable: Boolean(previewUrl)
      } satisfies ChatDocumentAsset
    })
    .filter((asset): asset is ChatDocumentAsset => Boolean(asset))
}
