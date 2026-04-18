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

const props = defineProps<{
  content: string
}>()

marked.setOptions({
  breaks: true,
  gfm: true
})

const safeHtml = computed(() => {
  const html = marked.parse(props.content || '') as string
  return DOMPurify.sanitize(html)
})
</script>

<style scoped>
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

.chat-markdown :deep(a) {
  @apply text-primary-600 underline decoration-primary-300 underline-offset-2 hover:text-primary-700 dark:text-primary-400 dark:decoration-primary-700;
}
</style>
