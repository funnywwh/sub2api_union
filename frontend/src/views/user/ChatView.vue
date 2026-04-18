<template>
  <AppLayout>
    <div class="chat-page">
      <aside class="card chat-sidebar">
        <div class="chat-sidebar-header">
          <div>
            <p class="text-xs font-semibold uppercase tracking-[0.22em] text-primary-500">
              {{ t('chat.workspace') }}
            </p>
            <h1 class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">
              {{ t('chat.title') }}
            </h1>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">
              {{ t('chat.subtitle') }}
            </p>
          </div>

          <div class="mt-4 flex gap-2">
            <button class="btn btn-primary btn-sm flex-1" :disabled="generating" @click="createSession(true)">
              <Icon name="plus" size="sm" />
              <span>{{ t('chat.newChat') }}</span>
            </button>
            <button class="btn btn-secondary btn-sm" :disabled="groupsLoading || generating" @click="refreshGroups">
              <Icon name="refresh" size="sm" />
            </button>
          </div>
        </div>

        <div v-if="!hasChatGroups && !groupsLoading" class="chat-empty-state">
          <div class="chat-empty-icon">
            <Icon name="chat" size="lg" />
          </div>
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('chat.noGroupsTitle') }}
          </h2>
          <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-300">
            {{ t('chat.noGroupsDescription') }}
          </p>
        </div>

        <div v-else class="chat-session-list">
          <div
            v-for="session in orderedSessions"
            :key="session.id"
            class="chat-session-item"
            :class="{ 'chat-session-item-active': session.id === activeSessionId }"
          >
            <button
              type="button"
              class="min-w-0 flex-1 text-left"
              :disabled="generating"
              @click="selectSession(session.id)"
            >
              <div class="flex items-center justify-between gap-3">
                <span class="truncate text-sm font-medium text-gray-900 dark:text-white">
                  {{ session.title }}
                </span>
                <span class="text-[11px] text-gray-400 dark:text-dark-400">
                  {{ formatRelativeTime(session.updatedAt) }}
                </span>
              </div>
              <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">
                {{ previewSession(session) }}
              </p>
            </button>

            <button
              type="button"
              class="chat-session-delete"
              :title="t('common.delete')"
              :disabled="generating"
              @click.stop="removeSession(session.id)"
            >
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>
      </aside>

      <section class="card chat-panel">
        <template v-if="activeSession">
          <header class="chat-toolbar">
            <div class="chat-toolbar-grid">
              <label class="block">
                <span class="chat-field-label">{{ t('chat.groupLabel') }}</span>
                <select
                  class="input"
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

              <label class="block">
                <span class="chat-field-label">{{ t('chat.modelLabel') }}</span>
                <input
                  v-model="activeSession.model"
                  class="input"
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
            </div>

            <div class="mt-3 flex items-center justify-between gap-3">
              <p class="text-xs text-gray-500 dark:text-dark-300">
                <span v-if="modelsLoading">{{ t('chat.loadingModels') }}</span>
                <span v-else-if="availableModels.length">
                  {{ t('chat.modelsLoaded', { count: availableModels.length }) }}
                </span>
                <span v-else>
                  {{ t('chat.modelHint') }}
                </span>
              </p>

              <button
                class="btn btn-ghost btn-sm"
                :disabled="modelsLoading || !selectedGroup || generating"
                @click="refreshModels(true)"
              >
                <Icon name="refresh" size="sm" />
                <span>{{ t('common.refresh') }}</span>
              </button>
            </div>
          </header>

          <div ref="messagesContainer" class="chat-messages">
            <template v-if="activeSession.messages.length">
              <article
                v-for="message in activeSession.messages"
                :key="message.id"
                class="chat-message-row"
                :class="message.role === 'user' ? 'justify-end' : 'justify-start'"
              >
                <div
                  class="chat-message"
                  :class="{
                    'chat-message-user': message.role === 'user',
                    'chat-message-assistant': message.role === 'assistant' && !message.error,
                    'chat-message-error': message.error
                  }"
                >
                  <div class="chat-message-meta">
                    <span>
                      {{ message.role === 'user' ? t('chat.you') : t('chat.assistant') }}
                    </span>
                    <span>{{ formatRelativeTime(message.createdAt) }}</span>
                  </div>

                  <div v-if="message.role === 'assistant' && !message.error" class="mt-3">
                    <ChatMarkdown :content="message.content || (isStreamingMessage(message.id) ? '...' : '')" />
                  </div>
                  <div
                    v-else
                    class="mt-3 whitespace-pre-wrap break-words text-sm leading-7"
                  >
                    {{ message.content }}
                  </div>

                  <div class="mt-3 flex justify-end">
                    <button
                      class="chat-copy-button"
                      :title="t('chat.copyMessage')"
                      @click="copyMessage(message.content)"
                    >
                      <Icon name="copy" size="sm" />
                    </button>
                  </div>
                </div>
              </article>
            </template>

            <div v-else class="chat-welcome">
              <div class="chat-welcome-badge">
                <Icon name="sparkles" size="md" />
                <span>{{ t('chat.welcomeBadge') }}</span>
              </div>
              <h2 class="mt-6 text-3xl font-semibold tracking-tight text-gray-900 dark:text-white">
                {{ t('chat.emptyTitle') }}
              </h2>
              <p class="mt-4 max-w-2xl text-sm leading-7 text-gray-500 dark:text-dark-300">
                {{ t('chat.emptyDescription') }}
              </p>

              <div class="mt-8 grid gap-3 md:grid-cols-3">
                <button
                  v-for="prompt in starterPrompts"
                  :key="prompt"
                  type="button"
                  class="chat-starter"
                  :disabled="generating || !hasChatGroups"
                  @click="useStarterPrompt(prompt)"
                >
                  <Icon name="chat" size="sm" />
                  <span>{{ prompt }}</span>
                </button>
              </div>
            </div>
          </div>

          <footer class="chat-composer">
            <div class="relative">
              <textarea
                v-model="draft"
                class="input chat-textarea"
                :placeholder="composerPlaceholder"
                :disabled="!canCompose"
                rows="1"
                @keydown="handleComposerKeydown"
              ></textarea>
            </div>

            <div class="mt-4 flex items-center justify-between gap-3">
              <p class="text-xs text-gray-500 dark:text-dark-300">
                {{ t('chat.enterHint') }}
              </p>

              <div class="flex gap-2">
                <button
                  v-if="generating"
                  class="btn btn-secondary btn-sm"
                  @click="stopGenerating"
                >
                  <Icon name="x" size="sm" />
                  <span>{{ t('chat.stop') }}</span>
                </button>

                <button
                  class="btn btn-primary btn-sm"
                  :disabled="!canSend"
                  @click="sendMessage"
                >
                  <Icon :name="generating ? 'refresh' : 'arrowUp'" size="sm" />
                  <span>{{ generating ? t('chat.sending') : t('chat.send') }}</span>
                </button>
              </div>
            </div>
          </footer>
        </template>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ChatMarkdown from '@/components/chat/ChatMarkdown.vue'
import { Icon } from '@/components/icons'
import { useAppStore, useAuthStore } from '@/stores'
import { userGroupsAPI } from '@/api'
import { listUserChatModels, streamUserChatCompletion, type ChatModel, type UserChatMessagePayload } from '@/api/chat'
import type { Group } from '@/types'
import { formatRelativeTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { clearLegacyChatLocalStorage } from '@/utils/chatStorage'

type ChatRole = 'user' | 'assistant'

interface ChatMessage {
  id: string
  role: ChatRole
  content: string
  createdAt: string
  error?: boolean
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
const abortController = ref<AbortController | null>(null)

function randomId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

function nowIso(): string {
  return new Date().toISOString()
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
    draft.value.trim() &&
    !generating.value
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
  t('chat.starters.debug'),
  t('chat.starters.summary'),
  t('chat.starters.plan')
])

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
        .map((session) => ({
          ...session,
          messages: session.messages.filter((message): message is ChatMessage => {
            return (
              message &&
              typeof message === 'object' &&
              typeof message.id === 'string' &&
              (message.role === 'user' || message.role === 'assistant') &&
              typeof message.content === 'string'
            )
          })
        }))
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

function createSession(focusComposer = false) {
  const session = createEmptySession()
  sessions.value.unshift(session)
  activeSessionId.value = session.id
  draft.value = ''
  if (focusComposer) {
    nextTick(() => {
      const composer = document.querySelector<HTMLTextAreaElement>('.chat-textarea')
      composer?.focus()
    })
  }
}

function selectSession(sessionId: string) {
  activeSessionId.value = sessionId
  draft.value = ''
  queueScrollToBottom()
}

function removeSession(sessionId: string) {
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
  const lastMessage = [...session.messages].reverse().find((message) => message.content.trim())
  if (!lastMessage) {
    return t('chat.emptyPreview')
  }
  return lastMessage.content.replace(/\s+/g, ' ').trim()
}

function buildSessionTitle(text: string): string {
  const cleaned = text.replace(/\s+/g, ' ').trim()
  if (!cleaned) {
    return t('chat.newChat')
  }
  return cleaned.length > 32 ? `${cleaned.slice(0, 32)}...` : cleaned
}

function formatGroupLabel(group: Group): string {
  return `${group.name} · ${group.platform}`
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

function mapMessagesForChat(session: ChatSession, nextUserMessage: string): UserChatMessagePayload[] {
  const history = session.messages
    .filter((message) => !message.error && message.content.trim())
    .map((message) => ({
      role: message.role,
      content: message.content
    })) as UserChatMessagePayload[]

  history.push({
    role: 'user',
    content: nextUserMessage
  })

  return history
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
  nextTick(() => {
    const composer = document.querySelector<HTMLTextAreaElement>('.chat-textarea')
    composer?.focus()
  })
}

function handleComposerKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    if (canSend.value) {
      void sendMessage()
    }
  }
}

function stopGenerating() {
  abortController.value?.abort()
}

async function sendMessage() {
  const session = activeSession.value
  const group = selectedGroup.value
  const prompt = draft.value.trim()

  if (!session || !group) {
    appStore.showError(t('chat.selectGroupFirst'))
    return
  }

  if (!session.model.trim()) {
    appStore.showError(t('chat.selectModelFirst'))
    return
  }

  if (!prompt || generating.value) {
    return
  }

  const chatMessages = mapMessagesForChat(session, prompt)
  const userMessage: ChatMessage = {
    id: randomId(),
    role: 'user',
    content: prompt,
    createdAt: nowIso()
  }
  const assistantMessage: ChatMessage = {
    id: randomId(),
    role: 'assistant',
    content: '',
    createdAt: nowIso()
  }

  session.messages.push(userMessage, assistantMessage)
  if (session.messages.filter((message) => message.role === 'user').length === 1) {
    session.title = buildSessionTitle(prompt)
  }
  touchSession(session)
  draft.value = ''

  generating.value = true
  streamingMessageId.value = assistantMessage.id
  abortController.value = new AbortController()
  queueScrollToBottom()

  try {
    const finalText = await streamUserChatCompletion({
      groupId: group.id,
      model: session.model.trim(),
      messages: chatMessages,
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

onMounted(async () => {
  clearLegacyChatLocalStorage()
  restoreSessions()
  await loadGroups()
})

onBeforeUnmount(() => {
  abortController.value?.abort()
})

watch(
  () => authStore.user?.id,
  async () => {
    sessions.value = []
    activeSessionId.value = ''
    draft.value = ''
    chatGroups.value = []
    modelCache.value = {}
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
</script>

<style scoped>
.chat-page {
  @apply grid gap-6 xl:grid-cols-[320px,minmax(0,1fr)];
  min-height: calc(100vh - 10rem);
}

.chat-sidebar,
.chat-panel {
  @apply flex min-h-[72vh] flex-col overflow-hidden;
}

.chat-sidebar-header {
  @apply border-b border-gray-100 px-5 py-5 dark:border-dark-700;
}

.chat-empty-state {
  @apply flex flex-1 flex-col items-center justify-center px-6 py-10 text-center;
}

.chat-empty-icon {
  @apply flex h-14 w-14 items-center justify-center rounded-2xl bg-primary-50 text-primary-600 dark:bg-primary-900/20 dark:text-primary-300;
}

.chat-session-list {
  @apply flex-1 space-y-2 overflow-y-auto p-4;
}

.chat-session-item {
  @apply flex w-full items-start gap-3 rounded-2xl border border-transparent bg-gray-50 px-4 py-3 text-left transition;
  @apply hover:border-gray-200 hover:bg-white dark:bg-dark-900/40 dark:hover:border-dark-600 dark:hover:bg-dark-900/70;
}

.chat-session-item-active {
  @apply border-primary-200 bg-primary-50 shadow-sm dark:border-primary-700/40 dark:bg-primary-900/20;
}

.chat-session-delete {
  @apply flex h-8 w-8 items-center justify-center rounded-xl text-gray-400 transition hover:bg-white hover:text-red-500;
  @apply dark:hover:bg-dark-800;
}

.chat-toolbar {
  @apply border-b border-gray-100 px-5 py-5 dark:border-dark-700;
}

.chat-toolbar-grid {
  @apply grid gap-4 md:grid-cols-2;
}

.chat-field-label {
  @apply mb-1.5 block text-xs font-semibold uppercase tracking-[0.18em] text-gray-500 dark:text-dark-300;
}

.chat-messages {
  @apply flex-1 overflow-y-auto bg-gradient-to-b from-transparent via-gray-50/50 to-gray-50/80 px-5 py-6 dark:via-dark-900/20 dark:to-dark-950/20;
}

.chat-message-row {
  @apply mb-5 flex;
}

.chat-message {
  @apply w-full max-w-3xl rounded-3xl border px-5 py-4 shadow-sm;
}

.chat-message-user {
  @apply border-primary-500/10 bg-gradient-to-br from-primary-500 to-primary-600 text-white shadow-primary-500/20;
}

.chat-message-assistant {
  @apply border-gray-100 bg-white text-gray-900 dark:border-dark-700 dark:bg-dark-900/60 dark:text-gray-100;
}

.chat-message-error {
  @apply border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-200;
}

.chat-message-meta {
  @apply flex items-center justify-between gap-4 text-[11px] font-medium uppercase tracking-[0.16em];
  @apply opacity-70;
}

.chat-copy-button {
  @apply flex h-8 w-8 items-center justify-center rounded-xl text-current opacity-60 transition hover:bg-black/5 hover:opacity-100;
}

.chat-welcome {
  @apply flex h-full flex-col items-center justify-center px-4 py-10 text-center;
}

.chat-welcome-badge {
  @apply inline-flex items-center gap-2 rounded-full border border-primary-200 bg-primary-50 px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-primary-700;
  @apply dark:border-primary-700/40 dark:bg-primary-900/20 dark:text-primary-300;
}

.chat-starter {
  @apply flex items-start gap-3 rounded-2xl border border-gray-200 bg-white px-4 py-4 text-left text-sm text-gray-700 transition;
  @apply hover:-translate-y-0.5 hover:border-primary-200 hover:shadow-sm dark:border-dark-700 dark:bg-dark-900/60 dark:text-gray-200 dark:hover:border-primary-700/40;
}

.chat-composer {
  @apply border-t border-gray-100 bg-white/90 px-5 py-5 backdrop-blur dark:border-dark-700 dark:bg-dark-900/80;
}

.chat-textarea {
  @apply min-h-[120px] resize-none pr-4;
}

@media (max-width: 1279px) {
  .chat-page {
    @apply grid-cols-1;
  }

  .chat-sidebar {
    min-height: auto;
  }

  .chat-session-list {
    max-height: 18rem;
  }
}
</style>
