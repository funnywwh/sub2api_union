<template>
  <div class="chat-rich-content">
    <ChatMarkdown :content="renderableMarkdown" />

    <div v-if="documentAssets.length" class="chat-rich-documents">
      <div class="chat-rich-documents-header">
        <span>{{ t('chat.attachmentsTitle') }}</span>
      </div>

      <div class="chat-rich-document-list">
        <article
          v-for="asset in documentAssets"
          :key="asset.url"
          class="chat-rich-document-card"
        >
          <div class="chat-rich-document-top">
            <div class="chat-rich-document-meta">
              <span class="chat-rich-document-badge">
                {{ labelForKind(asset.kind) }}
              </span>
              <span class="chat-rich-document-ext">{{ asset.extension.toUpperCase() }}</span>
            </div>

            <a
              class="chat-rich-document-link"
              :href="asset.url"
              target="_blank"
              rel="noopener noreferrer"
            >
              <Icon name="externalLink" size="xs" />
              <span>{{ t('chat.openAttachment') }}</span>
            </a>
          </div>

          <div class="chat-rich-document-title">
            {{ asset.label }}
          </div>

          <div v-if="asset.previewable" class="chat-rich-document-preview">
            <iframe
              :src="asset.previewUrl"
              :title="asset.label"
              loading="lazy"
              referrerpolicy="no-referrer"
            ></iframe>
          </div>

          <p v-else class="chat-rich-document-hint">
            {{ t('chat.previewUnavailable') }}
          </p>
        </article>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ChatMarkdown from './ChatMarkdown.vue'
import { Icon } from '@/components/icons'
import {
  buildDocumentAssetsFromAttachments,
  buildRenderableMarkdown,
  extractDocumentAssets,
  type ChatAssetKind,
  type ChatDocumentAsset,
  type ExplicitChatDocumentAssetInput
} from './chatRichContent'

const props = defineProps<{
  content: string
  attachmentAssets?: ExplicitChatDocumentAssetInput[]
}>()

const { t } = useI18n()

const renderableMarkdown = computed(() => buildRenderableMarkdown(props.content || ''))
const documentAssets = computed<ChatDocumentAsset[]>(() => {
  const inferred = extractDocumentAssets(props.content || '')
  const explicit = buildDocumentAssetsFromAttachments(props.attachmentAssets || [])
  const merged = new Map<string, ChatDocumentAsset>()

  for (const asset of [...explicit, ...inferred]) {
    merged.set(`${asset.kind}:${asset.url}:${asset.label}`, asset)
  }

  return [...merged.values()]
})

function labelForKind(kind: ChatAssetKind): string {
  switch (kind) {
    case 'pdf':
      return t('chat.attachmentKinds.pdf')
    case 'presentation':
      return t('chat.attachmentKinds.presentation')
    default:
      return t('chat.attachmentKinds.document')
  }
}
</script>

<style scoped>
.chat-rich-content {
  @apply min-w-0;
}

.chat-rich-documents {
  @apply mt-5 space-y-3;
}

.chat-rich-documents-header {
  @apply text-xs font-semibold uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500;
}

.chat-rich-document-list {
  @apply space-y-3;
}

.chat-rich-document-card {
  @apply overflow-hidden rounded-[24px] border border-black/5 bg-white/80 shadow-sm dark:border-white/10 dark:bg-white/[0.04];
}

.chat-rich-document-top {
  @apply flex flex-wrap items-center justify-between gap-3 border-b border-black/5 px-4 py-3 dark:border-white/10;
}

.chat-rich-document-meta {
  @apply flex items-center gap-2;
}

.chat-rich-document-badge {
  @apply inline-flex items-center rounded-full bg-[#202123] px-2.5 py-1 text-[11px] font-semibold uppercase tracking-[0.18em] text-white dark:bg-white dark:text-[#111214];
}

.chat-rich-document-ext {
  @apply text-xs font-medium text-gray-500 dark:text-gray-400;
}

.chat-rich-document-link {
  @apply inline-flex items-center gap-1 text-xs font-medium text-primary-600 transition hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300;
}

.chat-rich-document-title {
  @apply px-4 py-3 text-sm font-medium text-gray-800 dark:text-gray-100;
  word-break: break-word;
}

.chat-rich-document-preview {
  @apply border-t border-black/5 bg-[#f7f6f2] p-3 dark:border-white/10 dark:bg-black/20;
}

.chat-rich-document-preview iframe {
  @apply h-[420px] w-full rounded-2xl border border-black/5 bg-white dark:border-white/10 dark:bg-white;
}

.chat-rich-document-hint {
  @apply border-t border-black/5 px-4 py-3 text-sm text-gray-500 dark:border-white/10 dark:text-gray-400;
}
</style>
