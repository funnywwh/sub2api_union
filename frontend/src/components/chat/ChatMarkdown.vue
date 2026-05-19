<template>
  <div
    class="chat-markdown prose prose-sm max-w-none dark:prose-invert"
    v-html="safeHtml"
  ></div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { sanitizeUrl } from '@/utils/url'

const props = defineProps<{
  content: string
}>()

marked.setOptions({
  breaks: true,
  gfm: true
})

const SAFE_URI_PATTERN = /^(?:(?:https?:|mailto:|tel:|blob:)|(?:\/(?!\/))|(?:data:image\/[a-zA-Z0-9.+-]+;base64,))/i

const safeHtml = computed(() => {
  const html = marked.parse(props.content || '') as string
  const sanitized = DOMPurify.sanitize(html, {
    ADD_ATTR: ['target', 'rel', 'loading', 'referrerpolicy'],
    ALLOWED_URI_REGEXP: SAFE_URI_PATTERN
  })

  if (typeof window === 'undefined') {
    return sanitized
  }

  const parser = new window.DOMParser()
  const doc = parser.parseFromString(sanitized, 'text/html')

  doc.querySelectorAll('a[href]').forEach((element) => {
    const href = sanitizeUrl(element.getAttribute('href') || '', {
      allowRelative: true,
      allowBlobUrl: true
    })
    if (!href) {
      element.removeAttribute('href')
      return
    }

    element.setAttribute('href', href)
    element.setAttribute('target', '_blank')
    element.setAttribute('rel', 'noopener noreferrer')
  })

  doc.querySelectorAll('img[src]').forEach((element) => {
    const src = sanitizeUrl(element.getAttribute('src') || '', {
      allowRelative: true,
      allowDataUrl: true,
      allowBlobUrl: true
    })
    if (!src) {
      element.remove()
      return
    }

    element.setAttribute('src', src)
    element.setAttribute('loading', 'lazy')
    element.setAttribute('referrerpolicy', 'no-referrer')
  })

  return doc.body.innerHTML
})
</script>

<style scoped>
.chat-markdown {
  word-break: break-word;
}

.chat-markdown :deep(h1),
.chat-markdown :deep(h2),
.chat-markdown :deep(h3),
.chat-markdown :deep(h4) {
  @apply mt-6 font-semibold tracking-tight text-gray-900 dark:text-white;
}

.chat-markdown :deep(pre) {
  @apply overflow-x-auto rounded-2xl bg-slate-950 px-4 py-3 text-slate-100 shadow-inner;
}

.chat-markdown :deep(code) {
  @apply rounded bg-slate-100 px-1.5 py-0.5 text-[0.9em] text-slate-800 dark:bg-dark-700 dark:text-slate-100;
}

.chat-markdown :deep(pre code) {
  @apply bg-transparent p-0 text-inherit;
}

.chat-markdown :deep(p:first-child) {
  @apply mt-0;
}

.chat-markdown :deep(p:last-child) {
  @apply mb-0;
}

.chat-markdown :deep(ul),
.chat-markdown :deep(ol) {
  @apply my-3 pl-5;
}

.chat-markdown :deep(blockquote) {
  @apply border-l-4 border-primary-300 bg-primary-50/60 px-4 py-2 text-gray-700 dark:border-primary-700 dark:bg-primary-900/20 dark:text-gray-200;
}

.chat-markdown :deep(img) {
  @apply my-4 max-h-[520px] w-full rounded-[24px] border border-black/5 bg-white object-contain shadow-sm dark:border-white/10 dark:bg-white/[0.04];
}

.chat-markdown :deep(table) {
  @apply my-4 w-full overflow-hidden rounded-2xl border border-black/5 text-sm dark:border-white/10;
}

.chat-markdown :deep(thead) {
  @apply bg-gray-50 dark:bg-white/[0.05];
}

.chat-markdown :deep(th),
.chat-markdown :deep(td) {
  @apply border-b border-black/5 px-3 py-2 text-left dark:border-white/10;
}

.chat-markdown :deep(hr) {
  @apply my-5 border-black/10 dark:border-white/10;
}

.chat-markdown :deep(a) {
  @apply text-primary-600 underline decoration-primary-300 underline-offset-2 hover:text-primary-700 dark:text-primary-400 dark:decoration-primary-700;
}
</style>
