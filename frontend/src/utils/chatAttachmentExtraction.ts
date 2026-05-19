const PDFJS_MODULE_URL = 'https://cdn.jsdelivr.net/npm/pdfjs-dist@4.10.38/build/pdf.min.mjs'
const PDFJS_WORKER_URL = 'https://cdn.jsdelivr.net/npm/pdfjs-dist@4.10.38/build/pdf.worker.min.mjs'
const JSZIP_MODULE_URL = 'https://cdn.jsdelivr.net/npm/jszip@3.10.1/+esm'
const MAX_EXTRACTED_TEXT_CHARS = 24000
const MAX_PDF_PAGES = 40
const SLIDE_PATH_PATTERN = /^ppt\/slides\/slide(\d+)\.xml$/i

type PDFTextItem = {
  str?: string
  hasEOL?: boolean
}

type PDFPageProxy = {
  getTextContent: () => Promise<{ items: PDFTextItem[] }>
}

type PDFDocumentProxy = {
  numPages: number
  getPage: (pageNumber: number) => Promise<PDFPageProxy>
  destroy?: () => void
  cleanup?: () => void
}

type PDFJSLib = {
  getDocument: (source: { data: Uint8Array }) => { promise: Promise<PDFDocumentProxy> }
  GlobalWorkerOptions?: { workerSrc?: string }
}

type JSZipObject = {
  async: (type: 'text') => Promise<string>
}

type JSZipArchive = {
  files: Record<string, JSZipObject | undefined>
}

type JSZipModule = {
  loadAsync: (data: ArrayBuffer) => Promise<JSZipArchive>
}

let pdfJsLibPromise: Promise<PDFJSLib> | null = null
let jsZipPromise: Promise<JSZipModule> | null = null

function normalizeExtractedText(value: string): string {
  const compact = value
    .replace(/\r/g, '')
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()

  if (compact.length <= MAX_EXTRACTED_TEXT_CHARS) {
    return compact
  }

  return `${compact.slice(0, MAX_EXTRACTED_TEXT_CHARS)}\n\n[Truncated]`
}

function decodeXmlEntities(value: string): string {
  return value
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, '&')
}

async function loadPdfJsLib(): Promise<PDFJSLib> {
  if (!pdfJsLibPromise) {
    pdfJsLibPromise = (async () => {
      const module = await import(/* @vite-ignore */ PDFJS_MODULE_URL) as PDFJSLib
      if (module.GlobalWorkerOptions) {
        module.GlobalWorkerOptions.workerSrc = PDFJS_WORKER_URL
      }
      return module
    })()
  }
  return pdfJsLibPromise
}

async function loadJSZip(): Promise<JSZipModule> {
  if (!jsZipPromise) {
    jsZipPromise = (async () => {
      const module = await import(/* @vite-ignore */ JSZIP_MODULE_URL) as { default?: JSZipModule } & JSZipModule
      return module.default || module
    })()
  }
  return jsZipPromise
}

export async function extractPdfText(file: File): Promise<string> {
  const pdfjsLib = await loadPdfJsLib()
  const buffer = await file.arrayBuffer()
  const document = await pdfjsLib.getDocument({ data: new Uint8Array(buffer) }).promise

  try {
    const pages = Math.min(document.numPages || 0, MAX_PDF_PAGES)
    const pageTexts: string[] = []

    for (let pageNumber = 1; pageNumber <= pages; pageNumber += 1) {
      const page = await document.getPage(pageNumber)
      const textContent = await page.getTextContent()
      const pageText = textContent.items
        .map((item) => {
          const piece = item.str || ''
          return item.hasEOL ? `${piece}\n` : piece
        })
        .join(' ')
        .replace(/[ \t]+\n/g, '\n')
        .replace(/\s{2,}/g, ' ')
        .trim()

      if (pageText) {
        pageTexts.push(`Page ${pageNumber}\n${pageText}`)
      }

      if (pageTexts.join('\n\n').length >= MAX_EXTRACTED_TEXT_CHARS) {
        break
      }
    }

    return normalizeExtractedText(pageTexts.join('\n\n'))
  } finally {
    document.cleanup?.()
    document.destroy?.()
  }
}

export async function extractPptxText(file: File): Promise<string> {
  const JSZip = await loadJSZip()
  const archive = await JSZip.loadAsync(await file.arrayBuffer())

  const slideEntries = Object.keys(archive.files)
    .map((name) => {
      const match = name.match(SLIDE_PATH_PATTERN)
      if (!match) {
        return null
      }
      return {
        name,
        index: Number(match[1])
      }
    })
    .filter((entry): entry is { name: string; index: number } => Boolean(entry))
    .sort((a, b) => a.index - b.index)

  const slides: string[] = []

  for (const slideEntry of slideEntries) {
    const fileEntry = archive.files[slideEntry.name]
    if (!fileEntry) {
      continue
    }

    const xml = await fileEntry.async('text')
    const fragments = [...xml.matchAll(/<a:t[^>]*>([\s\S]*?)<\/a:t>/g)]
      .map((match) => decodeXmlEntities(match[1] || '').trim())
      .filter(Boolean)

    if (!fragments.length) {
      continue
    }

    slides.push(`Slide ${slideEntry.index}\n${fragments.join('\n')}`)
    if (slides.join('\n\n').length >= MAX_EXTRACTED_TEXT_CHARS) {
      break
    }
  }

  return normalizeExtractedText(slides.join('\n\n'))
}
