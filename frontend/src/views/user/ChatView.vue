<template>
  <AppLayout content-class="p-0">
    <div class="chat-workspace">
      <transition name="fade">
        <div
          v-if="historyMobileOpen"
          class="chat-mobile-backdrop lg:hidden"
          @click="closeHistorySidebar"
        ></div>
      </transition>

      <aside
        class="chat-history"
        :class="{
          'chat-history-collapsed': historyCollapsed,
          'chat-history-open': historyMobileOpen
        }"
      >
        <div class="chat-history-header">
          <button
            type="button"
            class="chat-new-session"
            :class="{ 'chat-new-session-collapsed': historyCollapsed }"
            :disabled="generating"
            :title="historyCollapsed ? t('chat.newChat') : undefined"
            @click="createSession(true)"
          >
            <Icon name="plus" size="sm" />
            <span v-if="!historyCollapsed">{{ t('chat.newChat') }}</span>
          </button>

          <button
            v-if="!historyCollapsed"
            type="button"
            class="chat-icon-button"
            :title="t('common.refresh')"
            :disabled="groupsLoading || generating"
            @click="refreshGroups"
          >
            <Icon name="refresh" size="sm" />
          </button>

          <button
            type="button"
            class="chat-icon-button hidden lg:inline-flex"
            :title="historyCollapsed ? t('chat.expandHistory') : t('chat.collapseHistory')"
            @click="toggleHistorySidebar"
          >
            <Icon :name="historyCollapsed ? 'chevronRight' : 'chevronLeft'" size="sm" />
          </button>

          <button
            type="button"
            class="chat-icon-button lg:hidden"
            :title="t('chat.closeHistory')"
            @click="closeHistorySidebar"
          >
            <Icon name="x" size="sm" />
          </button>
        </div>

        <template v-if="!historyCollapsed">
          <div class="chat-history-intro">
            <p class="chat-history-kicker">{{ t('chat.historyTitle') }}</p>
            <p class="chat-history-copy">{{ t('chat.historySubtitle') }}</p>
          </div>

          <div class="chat-history-stats">
            <span class="chat-pill">{{ t('chat.sessionCount', { count: orderedSessions.length }) }}</span>
            <span class="chat-pill" :class="{ 'chat-pill-active': groupsLoading }">
              {{ groupsLoading ? t('common.loading') : t('chat.modelsLoaded', { count: availableModels.length }) }}
            </span>
          </div>
        </template>

        <div v-if="!hasChatGroups && !groupsLoading" class="chat-history-empty">
          <div class="chat-history-empty-icon">
            <Icon name="chat" size="lg" />
          </div>
          <h2 class="chat-history-empty-title">{{ t('chat.noGroupsTitle') }}</h2>
          <p class="chat-history-empty-copy">{{ t('chat.noGroupsDescription') }}</p>
        </div>

        <div v-else class="chat-session-list">
          <div v-for="session in orderedSessions" :key="session.id" class="chat-session-shell">
            <button
              v-if="historyCollapsed"
              type="button"
              class="chat-session-mini"
              :class="{ 'chat-session-mini-active': session.id === activeSessionId }"
              :disabled="generating"
              :title="session.title"
              @click="selectSession(session.id)"
            >
              <span>{{ session.title.slice(0, 1).toUpperCase() }}</span>
            </button>

            <template v-else>
              <div
                class="chat-session-item"
                :class="{ 'chat-session-item-active': session.id === activeSessionId }"
              >
                <button
                  type="button"
                  class="chat-session-main"
                  :disabled="generating"
                  @click="selectSession(session.id)"
                >
                  <div class="chat-session-title-row">
                    <span class="chat-session-title">{{ session.title }}</span>
                    <span class="chat-session-time">{{ formatRelativeTime(session.updatedAt) }}</span>
                  </div>
                  <p class="chat-session-preview">{{ previewSession(session) }}</p>
                </button>

                <div class="chat-session-actions">
                  <button
                    type="button"
                    class="chat-session-action"
                    :title="t('chat.renameConversation')"
                    :disabled="generating"
                    @click.stop="beginRenamingSession(session)"
                  >
                    <Icon name="edit" size="sm" />
                  </button>
                  <button
                    type="button"
                    class="chat-session-action"
                    :title="t('common.delete')"
                    :disabled="generating"
                    @click.stop="removeSession(session.id)"
                  >
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </div>

              <div v-if="renamingSessionId === session.id" class="chat-rename-panel">
                <input
                  ref="renameInput"
                  v-model="renamingTitle"
                  class="chat-rename-input"
                  :placeholder="t('chat.renamePlaceholder')"
                  @blur="commitRenamingSession"
                  @keydown="handleRenamingKeydown"
                />
              </div>
            </template>
          </div>
        </div>

        <div v-if="!historyCollapsed" class="chat-history-footer">
          <p>{{ t('chat.localHistoryNotice') }}</p>
        </div>
      </aside>

      <section v-if="activeSession" class="chat-main">
        <header class="chat-main-header">
          <div class="chat-main-heading">
            <button
              type="button"
              class="chat-icon-button lg:hidden"
              :title="t('chat.openHistory')"
              @click="historyMobileOpen = true"
            >
              <Icon name="menu" size="sm" />
            </button>

            <button
              type="button"
              class="chat-icon-button hidden lg:inline-flex"
              :title="historyCollapsed ? t('chat.expandHistory') : t('chat.collapseHistory')"
              @click="toggleHistorySidebar"
            >
              <Icon :name="historyCollapsed ? 'chevronRight' : 'chevronLeft'" size="sm" />
            </button>

            <div class="min-w-0">
              <div class="chat-main-title-row">
                <h1 class="chat-main-title">{{ activeSession.title }}</h1>
                <button
                  type="button"
                  class="chat-inline-action"
                  :disabled="generating"
                  @click="beginRenamingSession(activeSession)"
                >
                  <Icon name="edit" size="xs" />
                  <span>{{ t('common.edit') }}</span>
                </button>
              </div>
              <p class="chat-main-subtitle">
                {{ selectedGroup ? formatGroupLabel(selectedGroup) : t('chat.selectGroupFirst') }}
              </p>
            </div>
          </div>

          <div class="chat-main-toolbar">
            <label class="chat-toolbar-field">
              <span class="chat-toolbar-label">{{ t('chat.groupLabel') }}</span>
              <select
                class="chat-toolbar-control"
                :value="currentGroupId"
                :disabled="generating || !hasChatGroups || groupsLoading"
                @change="onGroupChange"
              >
                <option
                  v-for="group in chatGroups"
                  :key="group.id"
                  :value="group.id"
                >
                  {{ formatGroupLabel(group) }}
                </option>
              </select>
            </label>

            <label class="chat-toolbar-field">
              <span class="chat-toolbar-label">{{ t('chat.modelLabel') }}</span>
              <input
                v-model="activeSession.model"
                class="chat-toolbar-control"
                :list="modelSuggestionsId"
                :placeholder="t('chat.modelPlaceholder')"
                :disabled="generating || !selectedGroup"
                @change="touchSession(activeSession)"
              />
              <datalist :id="modelSuggestionsId">
                <option
                  v-for="model in availableModels"
                  :key="model.id"
                  :value="model.id"
                >
                  {{ model.display_name || model.id }}
                </option>
              </datalist>
            </label>

            <button
              type="button"
              class="chat-secondary-button"
              :disabled="modelsLoading || !selectedGroup || generating"
              @click="refreshModels(true)"
            >
              <Icon name="refresh" size="sm" />
              <span>{{ t('common.refresh') }}</span>
            </button>
          </div>
        </header>

        <div ref="messagesContainer" class="chat-thread">
          <div v-if="activeSession.messages.length" class="chat-thread-inner">
            <article
              v-for="message in activeSession.messages"
              :key="message.id"
              class="chat-message-row group"
            >
              <div
                class="chat-message-avatar"
                :class="message.role === 'user' ? 'chat-message-avatar-user' : 'chat-message-avatar-assistant'"
              >
                <span>{{ message.role === 'user' ? 'U' : 'AI' }}</span>
              </div>

              <div class="chat-message-card">
                <div class="chat-message-meta">
                  <span class="chat-message-role">
                    {{ message.role === 'user' ? t('chat.you') : t('chat.assistant') }}
                  </span>
                  <span>{{ formatRelativeTime(message.createdAt) }}</span>
                </div>

                <div v-if="editingMessageId === message.id" class="chat-edit-box">
                  <textarea
                    ref="editingTextarea"
                    v-model="editingDraft"
                    class="chat-edit-textarea"
                    rows="1"
                    @input="resizeEditingTextarea"
                    @keydown="handleEditingKeydown"
                  ></textarea>

                  <div class="chat-edit-footer">
                    <p class="chat-edit-hint">{{ t('chat.editMessageHint') }}</p>
                    <div class="flex flex-wrap items-center gap-2">
                      <button type="button" class="chat-inline-action" @click="cancelEditingMessage">
                        <span>{{ t('common.cancel') }}</span>
                      </button>
                      <button type="button" class="chat-secondary-button" @click="submitEditedMessage">
                        <Icon name="arrowUp" size="sm" />
                        <span>{{ t('chat.saveAndResend') }}</span>
                      </button>
                    </div>
                  </div>
                </div>

                <div
                  v-else-if="message.role === 'user'"
                  class="chat-message-content chat-message-content-user"
                >
                  <ChatRichContent
                    :content="message.content"
                    :attachment-assets="buildRenderableAttachmentAssets(message.attachments)"
                  />
                </div>

                <div
                  v-else-if="message.error"
                  class="chat-message-content chat-message-content-error"
                >
                  {{ message.content }}
                </div>

                <div v-else class="chat-message-content chat-message-content-assistant">
                  <ChatRichContent
                    :content="message.content || (isStreamingMessage(message.id) ? '...' : '')"
                  />
                </div>

                <div class="chat-message-actions">
                  <button
                    type="button"
                    class="chat-inline-action"
                    :title="t('chat.copyMessage')"
                    @click="copyMessage(getCopyableMessageContent(message))"
                  >
                    <Icon name="copy" size="xs" />
                    <span>{{ t('common.copy') }}</span>
                  </button>

                  <button
                    v-if="message.role === 'user' && !message.error"
                    type="button"
                    class="chat-inline-action"
                    :disabled="generating"
                    :title="t('chat.editMessage')"
                    @click="startEditingMessage(message.id)"
                  >
                    <Icon name="edit" size="xs" />
                    <span>{{ t('chat.editMessage') }}</span>
                  </button>

                  <button
                    v-if="canRegenerateMessage(message.id)"
                    type="button"
                    class="chat-inline-action"
                    :disabled="generating"
                    :title="t('chat.regenerate')"
                    @click="regenerateAssistantFromMessage(message.id)"
                  >
                    <Icon name="refresh" size="xs" />
                    <span>{{ t('chat.regenerate') }}</span>
                  </button>
                </div>
              </div>
            </article>
          </div>

          <div v-else class="chat-empty-state">
            <div class="chat-empty-orb">
              <Icon name="sparkles" size="xl" />
            </div>
            <div class="chat-empty-badge">
              <Icon name="brain" size="sm" />
              <span>{{ t('chat.welcomeBadge') }}</span>
            </div>
            <h2 class="chat-empty-title">{{ t('chat.emptyTitle') }}</h2>
            <p class="chat-empty-description">{{ t('chat.emptyDescription') }}</p>

            <div class="chat-starter-grid">
              <button
                v-for="prompt in starterPrompts"
                :key="prompt.label"
                type="button"
                class="chat-starter-button"
                :disabled="generating || !hasChatGroups"
                @click="useStarterPrompt(prompt.prompt)"
              >
                <div class="flex items-center gap-3">
                  <div class="chat-starter-icon">
                    <Icon :name="prompt.icon" size="sm" />
                  </div>
                  <span class="chat-starter-title">{{ prompt.label }}</span>
                </div>
                <p class="chat-starter-copy">{{ prompt.prompt }}</p>
              </button>
            </div>
          </div>
        </div>

        <footer class="chat-composer-shell">
          <div v-if="editingMessageId" class="chat-editing-banner">
            <div class="flex items-center gap-2">
              <Icon name="edit" size="sm" />
              <span>{{ t('chat.editingMessage') }}</span>
            </div>
            <button type="button" class="chat-inline-action" @click="cancelEditingMessage">
              <span>{{ t('common.cancel') }}</span>
            </button>
          </div>

          <div class="chat-composer-card">
            <input
              ref="composerFileInput"
              type="file"
              class="hidden"
              :accept="composerFileAccept"
              multiple
              @change="handleComposerFilesChange"
            />

            <div v-if="pendingAttachments.length" class="chat-pending-attachments">
              <div
                v-for="attachment in pendingAttachments"
                :key="attachment.id"
                class="chat-pending-attachment"
              >
                <div class="chat-pending-attachment-main">
                  <span class="chat-pending-attachment-badge">
                    {{ attachmentBadgeLabel(attachment.kind) }}
                  </span>
                  <div class="min-w-0">
                    <p class="chat-pending-attachment-name">{{ attachment.name }}</p>
                    <p class="chat-pending-attachment-hint">
                      {{
                        attachment.includeInModelContext
                          ? t('chat.attachmentReady')
                          : t('chat.attachmentPreviewOnly')
                      }}
                    </p>
                  </div>
                </div>

                <button
                  type="button"
                  class="chat-pending-attachment-remove"
                  :title="t('chat.removeAttachment')"
                  @click="removePendingAttachment(attachment.id)"
                >
                  <Icon name="x" size="xs" />
                </button>
              </div>
            </div>

            <textarea
              ref="composerTextarea"
              v-model="draft"
              class="chat-textarea"
              :placeholder="composerPlaceholder"
              :disabled="!canCompose"
              rows="1"
              @input="resizeComposerTextarea"
              @keydown="handleComposerKeydown"
            ></textarea>

            <div class="chat-composer-footer">
              <div class="chat-composer-meta">
                <span class="chat-pill">{{ t('chat.enterHint') }}</span>
                <span class="chat-pill">
                  {{
                    generating
                      ? t('chat.streamingStatus')
                      : selectedGroup && activeSession.model.trim()
                        ? t('chat.modelAndGroup', {
                            group: selectedGroup.name,
                            model: activeSession.model.trim()
                          })
                        : t('chat.localHistoryNotice')
                  }}
                </span>
              </div>

              <div class="chat-composer-actions">
                <button
                  type="button"
                  class="chat-secondary-button"
                  :disabled="!canCompose || generating"
                  @click="openComposerFilePicker"
                >
                  <Icon name="upload" size="sm" />
                  <span>{{ t('chat.attachFiles') }}</span>
                </button>

                <button
                  v-if="lastAssistantMessage"
                  type="button"
                  class="chat-secondary-button"
                  :disabled="!canRegenerateLatest"
                  @click="regenerateLatestReply"
                >
                  <Icon name="refresh" size="sm" />
                  <span>{{ t('chat.regenerate') }}</span>
                </button>

                <button
                  v-if="generating"
                  type="button"
                  class="chat-secondary-button"
                  @click="stopGenerating"
                >
                  <Icon name="x" size="sm" />
                  <span>{{ t('chat.stop') }}</span>
                </button>

                <button
                  type="button"
                  class="chat-send-button"
                  :disabled="!canSend"
                  @click="sendMessage"
                >
                  <Icon name="arrowUp" size="sm" />
                  <span>{{ generating ? t('chat.sending') : t('chat.send') }}</span>
                </button>
              </div>
            </div>
          </div>
        </footer>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ChatRichContent from '@/components/chat/ChatRichContent.vue'
import { Icon } from '@/components/icons'
import { useAppStore, useAuthStore } from '@/stores'
import { userGroupsAPI } from '@/api'
import {
  listUserChatModels,
  streamUserChatCompletion,
  type ChatModel,
  type UserChatMessagePayload
} from '@/api/chat'
import type { Group } from '@/types'
import { formatRelativeTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { clearLegacyChatLocalStorage } from '@/utils/chatStorage'
import { extractPdfText, extractPptxText } from '@/utils/chatAttachmentExtraction'

type ChatRole = 'user' | 'assistant'
type ChatAttachmentKind = 'image' | 'markdown' | 'text' | 'pdf' | 'presentation' | 'document'
type ChatMessageRequestContent = UserChatMessagePayload['content']

interface ChatAttachment {
  id: string
  name: string
  kind: ChatAttachmentKind
  mimeType: string
  url: string
  size: number
  includeInModelContext: boolean
  textContent?: string
}

interface ChatMessage {
  id: string
  role: ChatRole
  content: string
  createdAt: string
  error?: boolean
  sourceText?: string
  requestContent?: ChatMessageRequestContent
  attachments?: ChatAttachment[]
}

interface ChatSession {
  id: string
  title: string
  groupId: number | null
  model: string
  createdAt: string
  updatedAt: string
  messages: ChatMessage[]
}

const STORAGE_VERSION = 'v2'
const modelSuggestionsId = 'chat-model-suggestions'
const composerFileAccept = '.png,.jpg,.jpeg,.gif,.webp,.svg,.bmp,.avif,.md,.markdown,.txt,.pdf,.ppt,.pptx'
const IMAGE_MIME_PREFIX = 'image/'
const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif'])
const MARKDOWN_EXTENSIONS = new Set(['md', 'markdown'])
const TEXT_EXTENSIONS = new Set(['txt'])
const PDF_EXTENSIONS = new Set(['pdf'])
const PRESENTATION_EXTENSIONS = new Set(['ppt', 'pptx'])
const MAX_INLINE_IMAGE_BYTES = 8 * 1024 * 1024
const MAX_INLINE_TEXT_BYTES = 2 * 1024 * 1024
const MAX_PREVIEW_FILE_BYTES = 20 * 1024 * 1024

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const sessions = ref<ChatSession[]>([])
const activeSessionId = ref('')
const draft = ref('')
const chatGroups = ref<Group[]>([])
const groupsLoading = ref(false)
const modelsLoading = ref(false)
const generating = ref(false)
const streamingMessageId = ref<string | null>(null)
const modelCache = ref<Record<number, ChatModel[]>>({})
const messagesContainer = ref<HTMLElement | null>(null)
const composerTextarea = ref<HTMLTextAreaElement | null>(null)
const editingTextarea = ref<HTMLTextAreaElement | null>(null)
const composerFileInput = ref<HTMLInputElement | null>(null)
const renameInput = ref<HTMLInputElement | null>(null)
const abortController = ref<AbortController | null>(null)
const historyCollapsed = ref(false)
const historyMobileOpen = ref(false)
const renamingSessionId = ref('')
const renamingTitle = ref('')
const editingMessageId = ref('')
const editingDraft = ref('')
const pendingAttachments = ref<ChatAttachment[]>([])

function randomId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

function nowIso(): string {
  return new Date().toISOString()
}

function getFileExtension(fileName: string): string {
  const trimmed = fileName.trim().toLowerCase()
  if (!trimmed.includes('.')) {
    return ''
  }
  return trimmed.split('.').pop() || ''
}

function resolveAttachmentKind(file: File): ChatAttachmentKind | null {
  const extension = getFileExtension(file.name)
  if (file.type.startsWith(IMAGE_MIME_PREFIX) || IMAGE_EXTENSIONS.has(extension)) {
    return 'image'
  }
  if (MARKDOWN_EXTENSIONS.has(extension)) {
    return 'markdown'
  }
  if (TEXT_EXTENSIONS.has(extension)) {
    return 'text'
  }
  if (PDF_EXTENSIONS.has(extension)) {
    return 'pdf'
  }
  if (PRESENTATION_EXTENSIONS.has(extension)) {
    return 'presentation'
  }
  return null
}

function cloneAttachment(attachment: ChatAttachment): ChatAttachment {
  return {
    ...attachment
  }
}

function cloneAttachments(attachments: ChatAttachment[] = []): ChatAttachment[] {
  return attachments.map(cloneAttachment)
}

function releaseAttachmentUrl(attachment: ChatAttachment) {
  if (attachment.url.startsWith('blob:')) {
    try {
      URL.revokeObjectURL(attachment.url)
    } catch {
      // Ignore revoked or invalid object URLs.
    }
  }
}

function releaseAttachments(attachments: ChatAttachment[] = []) {
  attachments.forEach(releaseAttachmentUrl)
}

function releaseMessageAttachments(message: ChatMessage | null | undefined) {
  if (!message?.attachments?.length) {
    return
  }
  releaseAttachments(message.attachments)
}

function releaseSessionAttachments(session: ChatSession | null | undefined) {
  if (!session) {
    return
  }
  session.messages.forEach((message) => releaseMessageAttachments(message))
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => reject(reader.error || new Error('Failed to read file as data URL'))
    reader.readAsDataURL(file)
  })
}

function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => reject(reader.error || new Error('Failed to read file as text'))
    reader.readAsText(file)
  })
}

const storageKey = computed(() => {
  const userId = authStore.user?.id
  return userId ? `sub2api_chat_sessions_${STORAGE_VERSION}_${userId}` : ''
})

const activeStorageKey = computed(() => {
  const userId = authStore.user?.id
  return userId ? `sub2api_chat_active_${STORAGE_VERSION}_${userId}` : ''
})

const orderedSessions = computed(() =>
  [...sessions.value].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
)

const activeSession = computed(() => {
  return sessions.value.find((session) => session.id === activeSessionId.value) || null
})

const hasChatGroups = computed(() => chatGroups.value.length > 0)

const currentGroupId = computed(() => activeSession.value?.groupId ?? '')

const selectedGroup = computed(() => {
  const groupId = activeSession.value?.groupId
  if (!groupId) {
    return null
  }
  return chatGroups.value.find((group) => group.id === groupId) || null
})

const availableModels = computed(() => {
  const groupId = selectedGroup.value?.id
  if (!groupId) {
    return []
  }
  return modelCache.value[groupId] || []
})

const canCompose = computed(() => hasChatGroups.value && !!selectedGroup.value)

const canSend = computed(() => {
  return Boolean(
    canCompose.value &&
    activeSession.value &&
    activeSession.value.model.trim() &&
    (draft.value.trim() || pendingAttachments.value.length) &&
    !generating.value
  )
})

const lastAssistantMessage = computed(() => {
  if (!activeSession.value) {
    return null
  }
  return [...activeSession.value.messages].reverse().find((message) => message.role === 'assistant') || null
})

const canRegenerateLatest = computed(() => {
  return Boolean(
    !generating.value &&
    lastAssistantMessage.value &&
    activeSession.value?.model.trim() &&
    resolveGroupForSession(activeSession.value)
  )
})

const composerPlaceholder = computed(() => {
  if (!hasChatGroups.value) {
    return t('chat.noGroupsComposer')
  }
  if (!selectedGroup.value) {
    return t('chat.selectGroupFirst')
  }
  if (!activeSession.value?.model.trim()) {
    return t('chat.selectModelFirst')
  }
  return t('chat.inputPlaceholder')
})

const starterPrompts = computed(() => [
  {
    icon: 'cpu' as const,
    label: t('chat.starters.debug'),
    prompt: t('chat.starters.debug')
  },
  {
    icon: 'document' as const,
    label: t('chat.starters.summary'),
    prompt: t('chat.starters.summary')
  },
  {
    icon: 'sparkles' as const,
    label: t('chat.starters.plan'),
    prompt: t('chat.starters.plan')
  }
])

function focusComposer() {
  nextTick(() => {
    composerTextarea.value?.focus()
    resizeTextarea(composerTextarea.value)
  })
}

function resizeTextarea(textarea: HTMLTextAreaElement | null, maxHeight = 320) {
  if (!textarea) {
    return
  }

  textarea.style.height = 'auto'
  textarea.style.height = `${Math.min(textarea.scrollHeight, maxHeight)}px`
}

function resizeComposerTextarea() {
  resizeTextarea(composerTextarea.value)
}

function resizeEditingTextarea() {
  resizeTextarea(editingTextarea.value, 240)
}

function isSupportedChatGroup(group: Group): boolean {
  return !group.claude_code_only && ['anthropic', 'openai', 'antigravity'].includes(group.platform)
}

function createEmptySession(): ChatSession {
  return {
    id: randomId(),
    title: t('chat.newChat'),
    groupId: chatGroups.value[0]?.id ?? null,
    model: '',
    createdAt: nowIso(),
    updatedAt: nowIso(),
    messages: []
  }
}

function persistSessions() {
  if (!storageKey.value) {
    return
  }

  sessionStorage.setItem(storageKey.value, JSON.stringify(sessions.value))
  sessionStorage.setItem(activeStorageKey.value, activeSessionId.value)
}

function normalizeStoredAttachment(value: unknown): ChatAttachment | null {
  if (!value || typeof value !== 'object') {
    return null
  }

  const record = value as Record<string, unknown>
  const kind = typeof record.kind === 'string' ? record.kind : ''
  const supportedKinds: ChatAttachmentKind[] = ['image', 'markdown', 'text', 'pdf', 'presentation', 'document']
  if (!supportedKinds.includes(kind as ChatAttachmentKind)) {
    return null
  }

  if (
    typeof record.id !== 'string' ||
    typeof record.name !== 'string' ||
    typeof record.mimeType !== 'string' ||
    typeof record.url !== 'string' ||
    typeof record.size !== 'number' ||
    typeof record.includeInModelContext !== 'boolean'
  ) {
    return null
  }

  return {
    id: record.id,
    name: record.name,
    kind: kind as ChatAttachmentKind,
    mimeType: record.mimeType,
    url: record.url,
    size: record.size,
    includeInModelContext: record.includeInModelContext,
    textContent: typeof record.textContent === 'string' ? record.textContent : undefined
  }
}

function normalizeStoredSession(session: ChatSession): ChatSession {
  return {
    id: session.id,
    title: typeof session.title === 'string' && session.title.trim() ? session.title : t('chat.newChat'),
    groupId: typeof session.groupId === 'number' ? session.groupId : null,
    model: typeof session.model === 'string' ? session.model : '',
    createdAt: typeof session.createdAt === 'string' ? session.createdAt : nowIso(),
    updatedAt: typeof session.updatedAt === 'string' ? session.updatedAt : nowIso(),
    messages: session.messages.filter((message): message is ChatMessage => {
      return (
        message &&
        typeof message === 'object' &&
        typeof message.id === 'string' &&
        (message.role === 'user' || message.role === 'assistant') &&
        typeof message.content === 'string'
      )
    }).map((message) => ({
      ...message,
      sourceText: typeof message.sourceText === 'string' ? message.sourceText : undefined,
      requestContent:
        typeof message.requestContent === 'string' ||
        Array.isArray(message.requestContent)
          ? message.requestContent
          : undefined,
      attachments: Array.isArray(message.attachments)
        ? message.attachments
            .map((attachment) => normalizeStoredAttachment(attachment))
            .filter((attachment): attachment is ChatAttachment => Boolean(attachment))
        : undefined
    }))
  }
}

function restoreSessions() {
  if (!storageKey.value) {
    return
  }

  try {
    const raw = sessionStorage.getItem(storageKey.value)
    const stored = raw ? JSON.parse(raw) : []
    if (Array.isArray(stored)) {
      sessions.value = stored
        .filter((session): session is ChatSession => {
          return (
            session &&
            typeof session === 'object' &&
            typeof session.id === 'string' &&
            Array.isArray(session.messages)
          )
        })
        .map(normalizeStoredSession)
    }
  } catch (error) {
    console.warn('Failed to restore chat sessions:', error)
    sessions.value = []
  }

  const storedActiveId = activeStorageKey.value ? sessionStorage.getItem(activeStorageKey.value) : ''
  if (storedActiveId && sessions.value.some((session) => session.id === storedActiveId)) {
    activeSessionId.value = storedActiveId
  }

  if (!sessions.value.length) {
    const session = createEmptySession()
    sessions.value = [session]
    activeSessionId.value = session.id
  } else if (!activeSessionId.value) {
    activeSessionId.value = sessions.value[0].id
  }
}

function touchSession(session: ChatSession) {
  session.updatedAt = nowIso()
}

function closeHistorySidebar() {
  historyMobileOpen.value = false
}

function toggleHistorySidebar() {
  historyCollapsed.value = !historyCollapsed.value
  if (!historyCollapsed.value) {
    nextTick(() => renameInput.value?.focus())
  } else {
    cancelRenamingSession()
  }
}

function createSession(focusNewComposer = false) {
  const session = createEmptySession()
  sessions.value.unshift(session)
  activeSessionId.value = session.id
  draft.value = ''
  clearPendingAttachments()
  closeHistorySidebar()
  cancelRenamingSession()
  cancelEditingMessage()
  if (focusNewComposer) {
    focusComposer()
  }
}

function selectSession(sessionId: string) {
  activeSessionId.value = sessionId
  draft.value = ''
  clearPendingAttachments()
  closeHistorySidebar()
  cancelRenamingSession()
  cancelEditingMessage()
  queueScrollToBottom()
}

function removeSession(sessionId: string) {
  const removedSession = sessions.value.find((session) => session.id === sessionId)
  releaseSessionAttachments(removedSession)

  const nextSessions = sessions.value.filter((session) => session.id !== sessionId)
  sessions.value = nextSessions

  if (!nextSessions.length) {
    createSession()
    return
  }

  if (activeSessionId.value === sessionId) {
    activeSessionId.value = nextSessions[0].id
  }
}

function previewSession(session: ChatSession): string {
  const lastMessage = [...session.messages].reverse().find((message) => getMessageSummaryText(message).trim())
  if (!lastMessage) {
    return t('chat.emptyPreview')
  }
  return getMessageSummaryText(lastMessage).replace(/\s+/g, ' ').trim()
}

function buildSessionTitle(text: string): string {
  const cleaned = text.replace(/\s+/g, ' ').trim()
  if (!cleaned) {
    return t('chat.newChat')
  }
  return cleaned.length > 32 ? `${cleaned.slice(0, 32)}...` : cleaned
}

function getMessageSummaryText(message: ChatMessage): string {
  const primary = (message.sourceText || message.content || '').trim()
  if (primary) {
    return primary
  }

  if (message.attachments?.length) {
    return message.attachments.map((attachment) => attachment.name).join(', ')
  }

  return ''
}

function deriveSessionTitleFromMessages(session: ChatSession): string {
  const firstUserMessage = session.messages.find((message) => {
    return message.role === 'user' && getMessageSummaryText(message).trim()
  })
  return firstUserMessage ? buildSessionTitle(getMessageSummaryText(firstUserMessage)) : t('chat.newChat')
}

function formatGroupLabel(group: Group): string {
  return `${group.name} · ${group.platform}`
}

function buildAttachmentDisplayContent(attachment: ChatAttachment): string {
  switch (attachment.kind) {
    case 'image':
      return `![${attachment.name}](${attachment.url})`
    case 'markdown':
      return `#### ${attachment.name}\n\n${attachment.textContent || ''}`
    case 'text':
      return `#### ${attachment.name}\n\n\`\`\`text\n${attachment.textContent || ''}\n\`\`\``
    default:
      return `[${attachment.name}](${attachment.url})`
  }
}

function buildUserMessageDisplayContent(sourceText: string, attachments: ChatAttachment[] = []): string {
  const segments: string[] = []
  const trimmedText = sourceText.trim()
  if (trimmedText) {
    segments.push(trimmedText)
  }

  for (const attachment of attachments) {
    segments.push(buildAttachmentDisplayContent(attachment))
  }

  return segments.join('\n\n').trim()
}

function buildUserMessagePayloadContent(
  sourceText: string,
  attachments: ChatAttachment[] = []
): ChatMessageRequestContent {
  const textSegments: string[] = []
  const trimmedText = sourceText.trim()
  if (trimmedText) {
    textSegments.push(trimmedText)
  }

  const imageAttachments = attachments.filter((attachment) => attachment.kind === 'image')
  const textBackedAttachments = attachments.filter((attachment) => {
    return attachment.includeInModelContext && attachment.textContent?.trim()
  })
  const previewOnlyAttachments = attachments.filter((attachment) => !attachment.includeInModelContext)

  for (const attachment of textBackedAttachments) {
    const attachmentText = attachment.textContent?.trim() || ''
    if (!attachmentText) {
      continue
    }

    switch (attachment.kind) {
      case 'markdown':
        textSegments.push(`Attached markdown file "${attachment.name}":\n\n${attachmentText}`)
        break
      case 'text':
        textSegments.push(`Attached text file "${attachment.name}":\n\n${attachmentText}`)
        break
      case 'pdf':
        textSegments.push(`Extracted text from PDF "${attachment.name}":\n\n${attachmentText}`)
        break
      case 'presentation':
        textSegments.push(`Extracted text from presentation "${attachment.name}":\n\n${attachmentText}`)
        break
      default:
        textSegments.push(`Attached file "${attachment.name}":\n\n${attachmentText}`)
    }
  }

  if (previewOnlyAttachments.length) {
    textSegments.push(
      `${t('chat.previewOnlyContextNotice')} ${previewOnlyAttachments.map((attachment) => attachment.name).join(', ')}`
    )
  }

  const combinedText = textSegments.join('\n\n').trim()
  if (!imageAttachments.length) {
    return combinedText
  }

  const multimodalParts: Exclude<ChatMessageRequestContent, string> = []

  if (combinedText) {
    multimodalParts.push({
      type: 'text',
      text: combinedText
    })
  }

  for (const attachment of imageAttachments) {
    multimodalParts.push({
      type: 'image_url',
      image_url: {
        url: attachment.url,
        detail: 'auto'
      }
    })
  }

  return multimodalParts
}

function attachmentBadgeLabel(kind: ChatAttachmentKind): string {
  switch (kind) {
    case 'image':
      return 'IMG'
    case 'markdown':
      return 'MD'
    case 'text':
      return 'TXT'
    case 'pdf':
      return 'PDF'
    case 'presentation':
      return 'PPT'
    default:
      return 'FILE'
  }
}

function buildRenderableAttachmentAssets(attachments: ChatAttachment[] = []) {
  return attachments
    .filter((attachment) => attachment.kind === 'pdf' || attachment.kind === 'presentation' || attachment.kind === 'document')
    .map((attachment) => ({
      kind: attachment.kind === 'presentation' ? 'presentation' : attachment.kind === 'pdf' ? 'pdf' : 'document',
      url: attachment.url,
      label: attachment.name,
      extension: getFileExtension(attachment.name) || attachment.kind
    }))
}

function resetComposerFileInput() {
  if (composerFileInput.value) {
    composerFileInput.value.value = ''
  }
}

function removePendingAttachment(attachmentId: string) {
  const target = pendingAttachments.value.find((attachment) => attachment.id === attachmentId)
  if (target) {
    releaseAttachmentUrl(target)
  }
  pendingAttachments.value = pendingAttachments.value.filter((attachment) => attachment.id !== attachmentId)
  resetComposerFileInput()
}

function clearPendingAttachments() {
  releaseAttachments(pendingAttachments.value)
  pendingAttachments.value = []
  resetComposerFileInput()
}

function openComposerFilePicker() {
  composerFileInput.value?.click()
}

async function tryExtractAttachmentText(file: File, kind: ChatAttachmentKind): Promise<string> {
  if (kind === 'pdf') {
    return extractPdfText(file)
  }

  if (kind === 'presentation' && getFileExtension(file.name) === 'pptx') {
    return extractPptxText(file)
  }

  return ''
}

async function createAttachmentFromFile(file: File): Promise<ChatAttachment | null> {
  const kind = resolveAttachmentKind(file)
  if (!kind) {
    appStore.showWarning(t('chat.attachmentUnsupported', { name: file.name }))
    return null
  }

  if (kind === 'image') {
    if (file.size > MAX_INLINE_IMAGE_BYTES) {
      appStore.showError(t('chat.attachmentImageTooLarge'))
      return null
    }

    const dataUrl = await readFileAsDataURL(file)
    return {
      id: randomId(),
      name: file.name,
      kind,
      mimeType: file.type || 'image/*',
      url: dataUrl,
      size: file.size,
      includeInModelContext: true
    }
  }

  if (kind === 'markdown' || kind === 'text') {
    if (file.size > MAX_INLINE_TEXT_BYTES) {
      appStore.showError(t('chat.attachmentTextTooLarge'))
      return null
    }

    const textContent = await readFileAsText(file)
    return {
      id: randomId(),
      name: file.name,
      kind,
      mimeType: file.type || 'text/plain',
      url: URL.createObjectURL(file),
      size: file.size,
      includeInModelContext: true,
      textContent
    }
  }

  if (file.size > MAX_PREVIEW_FILE_BYTES) {
    appStore.showError(t('chat.attachmentPreviewTooLarge'))
    return null
  }

  let extractedText = ''
  let includeInModelContext = false

  try {
    extractedText = (await tryExtractAttachmentText(file, kind)).trim()
    includeInModelContext = Boolean(extractedText)
  } catch (error) {
    console.warn('Failed to extract attachment text:', error)
  }

  return {
    id: randomId(),
    name: file.name,
    kind,
    mimeType: file.type || 'application/octet-stream',
    url: URL.createObjectURL(file),
    size: file.size,
    includeInModelContext,
    textContent: extractedText || undefined
  }
}

async function handleComposerFilesChange(event: Event) {
  const target = event.target as HTMLInputElement
  const files = Array.from(target.files || [])
  if (!files.length) {
    return
  }

  const nextAttachments: ChatAttachment[] = []
  let addedPreviewOnlyAttachment = false

  for (const file of files) {
    try {
      const attachment = await createAttachmentFromFile(file)
      if (!attachment) {
        continue
      }

      if (!attachment.includeInModelContext) {
        addedPreviewOnlyAttachment = true
      }

      nextAttachments.push(attachment)
    } catch (error) {
      console.error('Failed to process attachment:', error)
      appStore.showError(t('chat.attachmentReadFailed', { name: file.name }))
    }
  }

  if (nextAttachments.length) {
    pendingAttachments.value = [...pendingAttachments.value, ...nextAttachments]
  }
  if (addedPreviewOnlyAttachment) {
    appStore.showInfo(t('chat.previewOnlyAttachmentNotice'))
  }

  resetComposerFileInput()
}

function normalizeGroupChoice() {
  if (!activeSession.value) {
    return
  }

  const currentId = activeSession.value.groupId
  const stillExists = currentId ? chatGroups.value.some((group) => group.id === currentId) : false
  if (!stillExists) {
    activeSession.value.groupId = chatGroups.value[0]?.id ?? null
  }
}

function resolveGroupForSession(session: ChatSession | null): Group | null {
  if (!session?.groupId) {
    return null
  }
  return chatGroups.value.find((group) => group.id === session.groupId) || null
}

async function loadGroups(showToast = false) {
  groupsLoading.value = true

  try {
    const groups = await userGroupsAPI.getAvailable()
    chatGroups.value = groups.filter(isSupportedChatGroup)
    normalizeGroupChoice()
    if (showToast) {
      appStore.showSuccess(t('chat.groupsRefreshed'))
    }
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('chat.failedToLoadGroups')))
  } finally {
    groupsLoading.value = false
  }
}

async function refreshGroups() {
  await loadGroups(true)
}

async function refreshModels(force = false) {
  const group = selectedGroup.value
  if (!group) {
    return
  }

  if (!force && modelCache.value[group.id]?.length) {
    return
  }

  modelsLoading.value = true
  try {
    const models = await listUserChatModels(group.id)
    modelCache.value = {
      ...modelCache.value,
      [group.id]: models
    }

    if (activeSession.value && !activeSession.value.model.trim() && models[0]?.id) {
      activeSession.value.model = models[0].id
    }
  } catch (error) {
    if (force) {
      appStore.showError(extractApiErrorMessage(error, t('chat.failedToLoadModels')))
    }
  } finally {
    modelsLoading.value = false
  }
}

function onGroupChange(event: Event) {
  const value = Number((event.target as HTMLSelectElement).value)
  if (!activeSession.value || Number.isNaN(value)) {
    return
  }

  activeSession.value.groupId = value
  const cachedModels = modelCache.value[value]
  if (cachedModels?.length && !cachedModels.some((model) => model.id === activeSession.value?.model)) {
    activeSession.value.model = cachedModels[0]?.id || activeSession.value.model
  } else if (!cachedModels?.length) {
    activeSession.value.model = ''
  }
  touchSession(activeSession.value)
}

function buildPayloadMessages(messages: ChatMessage[]): UserChatMessagePayload[] {
  return messages
    .filter((message) => !message.error && message.content.trim())
    .map((message) => ({
      role: message.role,
      content: message.requestContent ?? message.content
    })) as UserChatMessagePayload[]
}

function queueScrollToBottom() {
  nextTick(() => {
    if (!messagesContainer.value) {
      return
    }

    messagesContainer.value.scrollTo({
      top: messagesContainer.value.scrollHeight,
      behavior: 'smooth'
    })
  })
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function isStreamingMessage(messageId: string): boolean {
  return streamingMessageId.value === messageId && generating.value
}

function canRegenerateMessage(messageId: string): boolean {
  return lastAssistantMessage.value?.id === messageId && !generating.value
}

function getCopyableMessageContent(message: ChatMessage): string {
  if (message.role !== 'user') {
    return message.content
  }

  const segments: string[] = []
  const primaryText = (message.sourceText || '').trim()
  if (primaryText) {
    segments.push(primaryText)
  }

  if (message.attachments?.length) {
    segments.push(
      message.attachments.map((attachment) => `[${attachment.kind}] ${attachment.name}`).join('\n')
    )
  }

  return segments.join('\n\n').trim() || message.content
}

async function copyMessage(content: string) {
  try {
    await navigator.clipboard.writeText(content)
    appStore.showSuccess(t('common.copied'))
  } catch {
    appStore.showError(t('common.copyFailed'))
  }
}

function useStarterPrompt(prompt: string) {
  draft.value = prompt
  focusComposer()
}

function handleComposerKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    if (canSend.value) {
      void sendMessage()
    }
  }
}

function handleEditingKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    cancelEditingMessage()
    return
  }

  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    void submitEditedMessage()
  }
}

function handleRenamingKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    cancelRenamingSession()
    return
  }

  if (event.key === 'Enter') {
    event.preventDefault()
    commitRenamingSession()
  }
}

function stopGenerating() {
  abortController.value?.abort()
}

function beginRenamingSession(session: ChatSession) {
  renamingSessionId.value = session.id
  renamingTitle.value = session.title
  if (historyCollapsed.value) {
    historyCollapsed.value = false
  }
  nextTick(() => {
    renameInput.value?.focus()
    renameInput.value?.select()
  })
}

function cancelRenamingSession() {
  renamingSessionId.value = ''
  renamingTitle.value = ''
}

function commitRenamingSession() {
  const sessionId = renamingSessionId.value
  if (!sessionId) {
    return
  }

  const session = sessions.value.find((item) => item.id === sessionId)
  if (!session) {
    cancelRenamingSession()
    return
  }

  const nextTitle = renamingTitle.value.trim()
  session.title = nextTitle || deriveSessionTitleFromMessages(session)
  touchSession(session)
  cancelRenamingSession()
}

function startEditingMessage(messageId: string) {
  const session = activeSession.value
  if (!session || generating.value) {
    return
  }

  const message = session.messages.find((item) => item.id === messageId && item.role === 'user')
  if (!message) {
    return
  }

  editingMessageId.value = message.id
  editingDraft.value = message.sourceText || message.content
  nextTick(() => {
    editingTextarea.value?.focus()
    resizeEditingTextarea()
  })
}

function cancelEditingMessage() {
  editingMessageId.value = ''
  editingDraft.value = ''
}

async function streamAssistantReply(session: ChatSession, payloadMessages: UserChatMessagePayload[]) {
  const group = resolveGroupForSession(session)
  if (!group) {
    appStore.showError(t('chat.selectGroupFirst'))
    return
  }

  if (!session.model.trim()) {
    appStore.showError(t('chat.selectModelFirst'))
    return
  }

  const assistantMessage: ChatMessage = {
    id: randomId(),
    role: 'assistant',
    content: '',
    createdAt: nowIso()
  }

  session.messages.push(assistantMessage)
  generating.value = true
  streamingMessageId.value = assistantMessage.id
  abortController.value = new AbortController()
  queueScrollToBottom()

  try {
    const finalText = await streamUserChatCompletion({
      groupId: group.id,
      model: session.model.trim(),
      messages: payloadMessages,
      signal: abortController.value.signal,
      onDelta: (chunk) => {
        assistantMessage.content += chunk
        queueScrollToBottom()
      }
    })

    if (!assistantMessage.content.trim() && finalText.trim()) {
      assistantMessage.content = finalText
    }

    if (!assistantMessage.content.trim()) {
      assistantMessage.content = t('chat.emptyReply')
    }
  } catch (error) {
    if (isAbortError(error)) {
      if (!assistantMessage.content.trim()) {
        session.messages = session.messages.filter((message) => message.id !== assistantMessage.id)
      }
    } else if (!assistantMessage.content.trim()) {
      assistantMessage.error = true
      assistantMessage.content = extractApiErrorMessage(error, t('chat.requestFailed'))
      appStore.showError(assistantMessage.content)
    } else {
      appStore.showError(extractApiErrorMessage(error, t('chat.requestFailed')))
    }
  } finally {
    generating.value = false
    streamingMessageId.value = null
    abortController.value = null
    touchSession(session)
    queueScrollToBottom()
  }
}

async function sendMessage() {
  const session = activeSession.value
  const prompt = draft.value.trim()
  const attachments = cloneAttachments(pendingAttachments.value)

  if (!session) {
    return
  }

  if (!resolveGroupForSession(session)) {
    appStore.showError(t('chat.selectGroupFirst'))
    return
  }

  if (!session.model.trim()) {
    appStore.showError(t('chat.selectModelFirst'))
    return
  }

  if ((!prompt && !attachments.length) || generating.value) {
    return
  }

  const displayContent = buildUserMessageDisplayContent(prompt, attachments)
  const requestContent = buildUserMessagePayloadContent(prompt, attachments)

  const userMessage: ChatMessage = {
    id: randomId(),
    role: 'user',
    content: displayContent,
    createdAt: nowIso(),
    sourceText: prompt,
    requestContent,
    attachments
  }

  session.messages.push(userMessage)
  if (session.messages.filter((message) => message.role === 'user').length === 1) {
    session.title = buildSessionTitle(getMessageSummaryText(userMessage))
  }
  touchSession(session)
  draft.value = ''
  pendingAttachments.value = []
  resetComposerFileInput()
  resizeComposerTextarea()

  await streamAssistantReply(session, buildPayloadMessages(session.messages))
}

async function submitEditedMessage() {
  const session = activeSession.value
  if (!session || !editingMessageId.value || generating.value) {
    return
  }

  const nextContent = editingDraft.value.trim()
  if (!nextContent) {
    appStore.showError(t('chat.emptyEditError'))
    return
  }

  const messageIndex = session.messages.findIndex((message) => message.id === editingMessageId.value)
  if (messageIndex === -1) {
    cancelEditingMessage()
    return
  }

  const targetMessage = session.messages[messageIndex]
  if (targetMessage.role !== 'user') {
    cancelEditingMessage()
    return
  }

  const retainedAttachments = cloneAttachments(targetMessage.attachments || [])
  const removedMessages = session.messages.slice(messageIndex + 1)
  removedMessages.forEach((message) => releaseMessageAttachments(message))

  session.messages = session.messages.slice(0, messageIndex + 1)
  session.messages[messageIndex] = {
    ...targetMessage,
    content: buildUserMessageDisplayContent(nextContent, retainedAttachments),
    createdAt: nowIso(),
    sourceText: nextContent,
    requestContent: buildUserMessagePayloadContent(nextContent, retainedAttachments),
    attachments: retainedAttachments
  }

  if (session.messages.filter((message) => message.role === 'user').length === 1) {
    session.title = buildSessionTitle(getMessageSummaryText(session.messages[messageIndex]))
  }

  touchSession(session)
  cancelEditingMessage()

  await streamAssistantReply(session, buildPayloadMessages(session.messages))
}

async function regenerateAssistantFromMessage(messageId: string) {
  const session = activeSession.value
  if (!session || generating.value) {
    return
  }

  const messageIndex = session.messages.findIndex((message) => message.id === messageId)
  if (messageIndex === -1 || session.messages[messageIndex].role !== 'assistant') {
    return
  }

  const previousMessages = session.messages.slice(0, messageIndex)
  const lastUserBeforeAssistant = [...previousMessages].reverse().find((message) => message.role === 'user')
  if (!lastUserBeforeAssistant) {
    return
  }

  session.messages.slice(messageIndex).forEach((message) => releaseMessageAttachments(message))
  session.messages = previousMessages
  touchSession(session)

  await streamAssistantReply(session, buildPayloadMessages(session.messages))
}

async function regenerateLatestReply() {
  if (!lastAssistantMessage.value) {
    return
  }
  await regenerateAssistantFromMessage(lastAssistantMessage.value.id)
}

onMounted(async () => {
  clearLegacyChatLocalStorage()
  restoreSessions()
  await loadGroups()
  nextTick(() => resizeComposerTextarea())
})

onBeforeUnmount(() => {
  abortController.value?.abort()
  clearPendingAttachments()
  sessions.value.forEach((session) => releaseSessionAttachments(session))
})

watch(
  () => authStore.user?.id,
  async () => {
    sessions.value.forEach((session) => releaseSessionAttachments(session))
    clearPendingAttachments()
    sessions.value = []
    activeSessionId.value = ''
    draft.value = ''
    chatGroups.value = []
    modelCache.value = {}
    cancelRenamingSession()
    cancelEditingMessage()
    restoreSessions()
    await loadGroups()
  }
)

watch(
  () => selectedGroup.value?.id,
  () => {
    normalizeGroupChoice()
    void refreshModels()
  },
  { immediate: true }
)

watch(
  [sessions, activeSessionId],
  () => {
    persistSessions()
  },
  { deep: true }
)

watch(draft, () => {
  nextTick(() => resizeComposerTextarea())
})

watch(editingDraft, () => {
  nextTick(() => resizeEditingTextarea())
})

watch(activeSessionId, () => {
  nextTick(() => resizeComposerTextarea())
})
</script>

<style scoped>
.chat-workspace {
  @apply relative flex min-h-[calc(100vh-4rem)] bg-[#fcfbf8] text-gray-900 dark:bg-[#111214] dark:text-white;
}

.chat-mobile-backdrop {
  @apply fixed inset-0 z-30 bg-black/50 backdrop-blur-sm;
}

.chat-history {
  @apply relative z-40 flex w-[320px] shrink-0 flex-col border-r border-black/5 bg-[#f3f1eb] transition-all duration-300 dark:border-white/10 dark:bg-[#16181c];
}

.chat-history-collapsed {
  @apply w-[88px];
}

.chat-history-header {
  @apply flex items-center gap-2 border-b border-black/5 p-4 dark:border-white/10;
}

.chat-new-session {
  @apply inline-flex flex-1 items-center justify-center gap-2 rounded-2xl bg-[#202123] px-4 py-3 text-sm font-medium text-white transition hover:bg-black disabled:cursor-not-allowed disabled:opacity-50 dark:bg-white dark:text-[#111214] dark:hover:bg-white/90;
}

.chat-new-session-collapsed {
  @apply flex-none px-0;
  width: 3rem;
}

.chat-icon-button {
  @apply inline-flex h-10 w-10 items-center justify-center rounded-2xl text-gray-500 transition hover:bg-black/5 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-white/5 dark:hover:text-white;
}

.chat-history-intro {
  @apply border-b border-black/5 px-4 pb-4 pt-1 dark:border-white/10;
}

.chat-history-kicker {
  @apply text-[11px] font-semibold uppercase tracking-[0.22em] text-gray-400 dark:text-gray-500;
}

.chat-history-copy {
  @apply mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300;
}

.chat-history-stats {
  @apply flex flex-wrap gap-2 px-4 py-4;
}

.chat-pill {
  @apply inline-flex items-center gap-2 rounded-full border border-black/5 bg-white px-3 py-1 text-xs font-medium text-gray-600 dark:border-white/10 dark:bg-white/[0.05] dark:text-gray-300;
}

.chat-pill-active {
  @apply border-primary-200 text-primary-700 dark:border-primary-700/40 dark:text-primary-300;
}

.chat-history-empty {
  @apply flex flex-1 flex-col items-center justify-center px-6 text-center;
}

.chat-history-empty-icon {
  @apply flex h-14 w-14 items-center justify-center rounded-2xl bg-white text-gray-900 shadow-sm dark:bg-white/[0.08] dark:text-white;
}

.chat-history-empty-title {
  @apply mt-6 text-base font-semibold text-gray-900 dark:text-white;
}

.chat-history-empty-copy {
  @apply mt-3 text-sm leading-6 text-gray-500 dark:text-gray-400;
}

.chat-session-list {
  @apply flex-1 space-y-2 overflow-y-auto p-3;
}

.chat-session-shell {
  @apply space-y-2;
}

.chat-session-mini {
  @apply inline-flex h-12 w-12 items-center justify-center rounded-2xl border border-transparent bg-white text-sm font-semibold text-gray-700 shadow-sm transition hover:border-black/5 hover:text-gray-900 dark:bg-white/[0.05] dark:text-gray-200 dark:hover:border-white/10 dark:hover:text-white;
}

.chat-session-mini-active {
  @apply border-black/10 bg-[#202123] text-white dark:border-white/10 dark:bg-white dark:text-[#111214];
}

.chat-session-item {
  @apply group flex items-start gap-2 rounded-2xl border border-transparent bg-white/80 px-3 py-3 shadow-sm transition hover:border-black/5 hover:bg-white dark:bg-white/[0.04] dark:hover:border-white/10 dark:hover:bg-white/[0.07];
}

.chat-session-item-active {
  @apply border-black/10 bg-white dark:border-white/10 dark:bg-white/[0.08];
}

.chat-session-main {
  @apply min-w-0 flex-1 text-left;
}

.chat-session-title-row {
  @apply flex items-start justify-between gap-3;
}

.chat-session-title {
  @apply line-clamp-1 text-sm font-medium text-gray-900 dark:text-white;
}

.chat-session-time {
  @apply shrink-0 text-[11px] uppercase tracking-[0.14em] text-gray-400 dark:text-gray-500;
}

.chat-session-preview {
  @apply mt-1 line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400;
}

.chat-session-actions {
  @apply flex items-center gap-1 transition;
}

.chat-session-action {
  @apply inline-flex h-8 w-8 items-center justify-center rounded-xl text-gray-400 transition hover:bg-black/5 hover:text-gray-900 dark:hover:bg-white/5 dark:hover:text-white;
}

.chat-rename-panel {
  @apply px-2;
}

.chat-rename-input {
  @apply w-full rounded-2xl border border-black/10 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 dark:border-white/10 dark:bg-white/[0.06] dark:text-white;
}

.chat-history-footer {
  @apply border-t border-black/5 px-4 py-4 text-xs leading-6 text-gray-500 dark:border-white/10 dark:text-gray-400;
}

.chat-main {
  @apply flex min-w-0 flex-1 flex-col;
}

.chat-main-header {
  @apply flex flex-col gap-4 border-b border-black/5 bg-white/80 px-4 py-4 backdrop-blur dark:border-white/10 dark:bg-[#111214]/90 md:px-6 xl:flex-row xl:items-center xl:justify-between;
}

.chat-main-heading {
  @apply flex min-w-0 items-center gap-3;
}

.chat-main-title-row {
  @apply flex flex-wrap items-center gap-3;
}

.chat-main-title {
  @apply line-clamp-1 text-lg font-semibold tracking-tight text-gray-900 dark:text-white;
}

.chat-main-subtitle {
  @apply mt-1 text-sm text-gray-500 dark:text-gray-400;
}

.chat-main-toolbar {
  @apply grid gap-3 xl:min-w-[560px] xl:grid-cols-[minmax(0,1fr),minmax(0,1fr),auto];
}

.chat-toolbar-field {
  @apply min-w-0;
}

.chat-toolbar-label {
  @apply mb-2 block text-[11px] font-semibold uppercase tracking-[0.2em] text-gray-500 dark:text-gray-400;
}

.chat-toolbar-control {
  @apply w-full rounded-2xl border border-black/10 bg-white px-4 py-2.5 text-sm text-gray-900 outline-none transition focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 disabled:cursor-not-allowed disabled:bg-gray-100 dark:border-white/10 dark:bg-white/[0.04] dark:text-white dark:disabled:bg-white/[0.03];
}

.chat-thread {
  @apply flex-1 overflow-y-auto;
}

.chat-thread-inner {
  @apply mx-auto flex w-full max-w-5xl flex-col gap-2 px-4 py-8 md:px-6 lg:px-10;
}

.chat-message-row {
  @apply flex gap-4 rounded-3xl px-4 py-5 transition hover:bg-black/[0.025] dark:hover:bg-white/[0.02];
}

.chat-message-avatar {
  @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl text-sm font-semibold shadow-sm;
}

.chat-message-avatar-user {
  @apply bg-[#202123] text-white dark:bg-white dark:text-[#111214];
}

.chat-message-avatar-assistant {
  @apply bg-emerald-500 text-white;
}

.chat-message-card {
  @apply min-w-0 flex-1;
}

.chat-message-meta {
  @apply flex items-center gap-3 text-[11px] font-medium uppercase tracking-[0.16em] text-gray-400 dark:text-gray-500;
}

.chat-message-role {
  @apply text-gray-900 dark:text-white;
}

.chat-message-content {
  @apply mt-3 text-[15px] leading-8 text-gray-800 dark:text-gray-100;
}

.chat-message-content-user {
  @apply overflow-hidden rounded-[28px] border border-black/5 bg-white px-5 py-4 shadow-sm dark:border-white/10 dark:bg-white/[0.05];
}

.chat-message-content-assistant {
  @apply min-w-0;
}

.chat-message-content-error {
  @apply rounded-[28px] border border-red-200 bg-red-50 px-5 py-4 text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-200;
}

.chat-message-actions {
  @apply mt-3 flex flex-wrap items-center gap-2 transition;
}

.chat-inline-action {
  @apply inline-flex items-center gap-1 rounded-full border border-black/5 bg-white px-3 py-1.5 text-xs font-medium text-gray-600 transition hover:border-black/10 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-50 dark:border-white/10 dark:bg-white/[0.04] dark:text-gray-300 dark:hover:bg-white/[0.08] dark:hover:text-white;
}

.chat-edit-box {
  @apply mt-3 rounded-[28px] border border-black/10 bg-white p-4 shadow-sm dark:border-white/10 dark:bg-white/[0.05];
}

.chat-edit-textarea {
  @apply min-h-[120px] max-h-[240px] w-full resize-none bg-transparent text-[15px] leading-7 text-gray-900 outline-none dark:text-white;
}

.chat-edit-footer {
  @apply mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-black/5 pt-3 dark:border-white/10;
}

.chat-edit-hint {
  @apply text-xs text-gray-500 dark:text-gray-400;
}

.chat-empty-state {
  @apply mx-auto flex min-h-[60vh] w-full max-w-5xl flex-col items-center justify-center px-6 py-12 text-center;
}

.chat-empty-orb {
  @apply flex h-20 w-20 items-center justify-center rounded-[28px] bg-[#202123] text-white shadow-lg dark:bg-white dark:text-[#111214];
}

.chat-empty-badge {
  @apply mt-5 inline-flex items-center gap-2 rounded-full border border-black/5 bg-white px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-gray-600 dark:border-white/10 dark:bg-white/[0.05] dark:text-gray-300;
}

.chat-empty-title {
  @apply mt-6 text-4xl font-semibold tracking-tight text-gray-900 dark:text-white;
}

.chat-empty-description {
  @apply mt-4 max-w-2xl text-sm leading-7 text-gray-500 dark:text-gray-400;
}

.chat-starter-grid {
  @apply mt-10 grid w-full max-w-4xl gap-3 md:grid-cols-3;
}

.chat-starter-button {
  @apply rounded-[28px] border border-black/5 bg-white p-5 text-left transition hover:-translate-y-0.5 hover:border-black/10 hover:shadow-lg disabled:cursor-not-allowed disabled:opacity-50 dark:border-white/10 dark:bg-white/[0.04] dark:hover:bg-white/[0.08];
}

.chat-starter-icon {
  @apply flex h-10 w-10 items-center justify-center rounded-2xl bg-[#f3f1eb] text-gray-700 dark:bg-white/[0.06] dark:text-gray-200;
}

.chat-starter-title {
  @apply text-sm font-semibold text-gray-900 dark:text-white;
}

.chat-starter-copy {
  @apply mt-4 text-sm leading-6 text-gray-600 dark:text-gray-300;
}

.chat-composer-shell {
  @apply sticky bottom-0 bg-gradient-to-t from-[#fcfbf8] via-[#fcfbf8]/96 to-transparent px-4 pb-4 pt-3 dark:from-[#111214] dark:via-[#111214]/96 md:px-6 lg:px-10;
}

.chat-editing-banner {
  @apply mx-auto mb-3 flex max-w-5xl items-center justify-between gap-3 rounded-2xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200;
}

.chat-composer-card {
  @apply mx-auto flex max-w-5xl flex-col gap-3 rounded-[28px] border border-black/5 bg-white p-3 shadow-[0_24px_70px_-30px_rgba(15,23,42,0.45)] dark:border-white/10 dark:bg-[#17181c];
}

.chat-pending-attachments {
  @apply flex flex-col gap-2 px-1 pt-1;
}

.chat-pending-attachment {
  @apply flex items-center justify-between gap-3 rounded-2xl border border-black/5 bg-[#f7f6f2] px-3 py-2 dark:border-white/10 dark:bg-white/[0.05];
}

.chat-pending-attachment-main {
  @apply flex min-w-0 items-center gap-3;
}

.chat-pending-attachment-badge {
  @apply inline-flex min-w-[3rem] items-center justify-center rounded-full bg-[#202123] px-2.5 py-1 text-[11px] font-semibold uppercase tracking-[0.16em] text-white dark:bg-white dark:text-[#111214];
}

.chat-pending-attachment-name {
  @apply truncate text-sm font-medium text-gray-800 dark:text-gray-100;
}

.chat-pending-attachment-hint {
  @apply text-xs text-gray-500 dark:text-gray-400;
}

.chat-pending-attachment-remove {
  @apply inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-xl text-gray-400 transition hover:bg-black/5 hover:text-gray-900 dark:hover:bg-white/5 dark:hover:text-white;
}

.chat-textarea {
  @apply min-h-[56px] max-h-[320px] w-full resize-none bg-transparent px-3 py-3 text-[15px] leading-7 text-gray-900 outline-none placeholder:text-gray-400 disabled:cursor-not-allowed disabled:text-gray-400 dark:text-white dark:placeholder:text-gray-500;
}

.chat-composer-footer {
  @apply flex flex-col gap-3 border-t border-black/5 px-2 pt-3 dark:border-white/10 lg:flex-row lg:items-center lg:justify-between;
}

.chat-composer-meta {
  @apply flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400;
}

.chat-composer-actions {
  @apply flex flex-wrap items-center justify-end gap-2;
}

.chat-secondary-button {
  @apply inline-flex items-center gap-2 rounded-full border border-black/5 bg-white px-4 py-2 text-sm font-medium text-gray-700 transition hover:border-black/10 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-50 dark:border-white/10 dark:bg-white/[0.04] dark:text-gray-200 dark:hover:bg-white/[0.08] dark:hover:text-white;
}

.chat-send-button {
  @apply inline-flex items-center gap-2 rounded-full bg-[#202123] px-4 py-2 text-sm font-medium text-white transition hover:bg-black disabled:cursor-not-allowed disabled:opacity-50 dark:bg-white dark:text-[#111214] dark:hover:bg-white/90;
}

@media (max-width: 1023px) {
  .chat-history {
    @apply fixed bottom-0 left-0 top-16 w-[min(88vw,320px)] -translate-x-full shadow-2xl;
  }

  .chat-history-open {
    @apply translate-x-0;
  }

  .chat-history-collapsed {
    width: min(88vw, 320px);
  }
}

@media (max-width: 767px) {
  .chat-main-toolbar {
    @apply grid-cols-1;
  }

  .chat-message-row {
    @apply px-0;
  }

  .chat-empty-title {
    @apply text-3xl;
  }
}

@media (min-width: 768px) {
  .chat-session-actions,
  .chat-message-actions {
    opacity: 0;
  }

  .chat-session-item:hover .chat-session-actions,
  .chat-message-row:hover .chat-message-actions {
    opacity: 1;
  }
}
</style>
