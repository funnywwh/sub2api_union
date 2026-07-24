<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <div class="rounded-2xl border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200">
        {{ t('admin.realtimeVoiceTest.directMediaNotice') }}
      </div>

      <div class="grid grid-cols-1 gap-6 xl:grid-cols-5">
        <section class="card p-6 xl:col-span-3">
          <div class="mb-5 flex items-center justify-between gap-3">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.realtimeVoiceTest.configuration') }}
            </h2>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="hasSession" @click="resetSessionTemplate">
              {{ t('admin.realtimeVoiceTest.resetTemplate') }}
            </button>
          </div>

          <div class="space-y-5">
            <div>
              <label for="realtime-api-key" class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.realtimeVoiceTest.apiKey') }}
              </label>
              <input
                id="realtime-api-key"
                v-model="apiKey"
                type="password"
                autocomplete="off"
                spellcheck="false"
                class="input w-full font-mono"
                :placeholder="t('admin.realtimeVoiceTest.apiKeyPlaceholder')"
                :disabled="hasSession"
              />
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.realtimeVoiceTest.apiKeyHint') }}
              </p>
            </div>

            <div>
              <label for="realtime-proof-token" class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.realtimeVoiceTest.proofToken') }}
              </label>
              <input
                id="realtime-proof-token"
                v-model="proofToken"
                type="password"
                autocomplete="off"
                spellcheck="false"
                class="input w-full font-mono"
                :placeholder="t('admin.realtimeVoiceTest.proofTokenPlaceholder')"
                :disabled="hasSession"
              />
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.realtimeVoiceTest.proofTokenHint') }}
              </p>
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div>
                <label for="realtime-mode" class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.realtimeVoiceTest.mode') }}
                </label>
                <select id="realtime-mode" v-model="mode" class="input w-full" :disabled="hasSession">
                  <option value="vp">{{ t('admin.realtimeVoiceTest.advanced') }}</option>
                  <option value="vps">{{ t('admin.realtimeVoiceTest.standard') }}</option>
                  <option value="wm">{{ t('admin.realtimeVoiceTest.wingman') }}</option>
                </select>
              </div>
              <div>
                <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.realtimeVoiceTest.endpoint') }}
                </label>
                <div class="flex min-h-[42px] items-center rounded-lg border border-gray-200 bg-gray-50 px-3 font-mono text-sm text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200">
                  {{ endpoint }}
                </div>
              </div>
            </div>

            <div>
              <label for="realtime-session-json" class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.realtimeVoiceTest.sessionJson') }}
              </label>
              <textarea
                id="realtime-session-json"
                v-model="sessionJson"
                rows="10"
                spellcheck="false"
                class="input w-full resize-y font-mono text-xs leading-5"
                :disabled="hasSession"
              />
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.realtimeVoiceTest.sessionJsonHint') }}
              </p>
            </div>

            <div v-if="errorMessage" class="rounded-xl bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">
              {{ errorMessage }}
            </div>

            <div class="flex flex-wrap gap-3">
              <button type="button" class="btn btn-primary" :disabled="hasSession" @click="startSession">
                {{ isActive ? t('admin.realtimeVoiceTest.starting') : t('admin.realtimeVoiceTest.start') }}
              </button>
              <button type="button" class="btn btn-danger" :disabled="!hasSession" @click="stopSession">
                {{ t('admin.realtimeVoiceTest.stop') }}
              </button>
              <button type="button" class="btn btn-secondary" :disabled="!localStream" @click="toggleMicrophone">
                {{ microphoneMuted ? t('admin.realtimeVoiceTest.unmute') : t('admin.realtimeVoiceTest.mute') }}
              </button>
              <button type="button" class="btn btn-secondary" :disabled="eventLogs.length === 0 && transcripts.length === 0" @click="clearResults">
                {{ t('admin.realtimeVoiceTest.clear') }}
              </button>
            </div>
          </div>
        </section>

        <section class="card p-6 xl:col-span-2">
          <h2 class="mb-5 text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.realtimeVoiceTest.status') }}
          </h2>

          <div class="mb-6 flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
            <span class="h-3 w-3 rounded-full" :class="statusDotClass" />
            <div>
              <div class="font-medium text-gray-900 dark:text-white">{{ statusLabel }}</div>
              <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ formattedDuration }}</div>
            </div>
          </div>

          <dl class="space-y-3 text-sm">
            <div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-dark-700">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.realtimeVoiceTest.peerConnection') }}</dt>
              <dd class="font-mono text-gray-900 dark:text-gray-100">{{ peerConnectionState }}</dd>
            </div>
            <div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-dark-700">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.realtimeVoiceTest.iceConnection') }}</dt>
              <dd class="font-mono text-gray-900 dark:text-gray-100">{{ iceConnectionState }}</dd>
            </div>
            <div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-dark-700">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.realtimeVoiceTest.signalingState') }}</dt>
              <dd class="font-mono text-gray-900 dark:text-gray-100">{{ signalingState }}</dd>
            </div>
            <div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-dark-700">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.realtimeVoiceTest.duration') }}</dt>
              <dd class="font-mono text-gray-900 dark:text-gray-100">{{ formattedDuration }}</dd>
            </div>
            <div class="flex items-center justify-between gap-4">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.realtimeVoiceTest.mediaPath') }}</dt>
              <dd class="text-right font-medium text-emerald-600 dark:text-emerald-400">
                {{ t('admin.realtimeVoiceTest.mediaPathValue') }}
              </dd>
            </div>
          </dl>

          <div class="mt-6">
            <div class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.realtimeVoiceTest.remoteAudio') }}
            </div>
            <audio ref="remoteAudio" controls autoplay playsinline class="w-full" />
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.realtimeVoiceTest.remoteAudioHint') }}
            </p>
          </div>
        </section>
      </div>

      <section class="card p-6">
        <h2 class="mb-4 text-base font-semibold text-gray-900 dark:text-white">
          {{ t('admin.realtimeVoiceTest.transcript') }}
        </h2>
        <div v-if="transcripts.length === 0" class="rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
          {{ t('admin.realtimeVoiceTest.transcriptEmpty') }}
        </div>
        <div v-else class="space-y-3">
          <div
            v-for="item in transcripts"
            :key="item.id"
            class="rounded-xl px-4 py-3"
            :class="item.role === 'user' ? 'bg-blue-50 dark:bg-blue-950/25' : 'bg-emerald-50 dark:bg-emerald-950/25'"
          >
            <div class="mb-1 text-xs font-semibold uppercase tracking-wide" :class="item.role === 'user' ? 'text-blue-600 dark:text-blue-400' : 'text-emerald-600 dark:text-emerald-400'">
              {{ item.role === 'user' ? t('admin.realtimeVoiceTest.you') : t('admin.realtimeVoiceTest.assistant') }}
            </div>
            <div class="whitespace-pre-wrap text-sm text-gray-900 dark:text-gray-100">{{ item.text }}</div>
          </div>
        </div>
      </section>

      <section class="card p-6">
        <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.realtimeVoiceTest.events') }}
            </h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.realtimeVoiceTest.eventsHint') }}
            </p>
          </div>
          <span class="rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
            {{ eventLogs.length }}/100
          </span>
        </div>
        <div v-if="eventLogs.length === 0" class="rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
          {{ t('admin.realtimeVoiceTest.noEvents') }}
        </div>
        <div v-else class="max-h-[520px] space-y-2 overflow-y-auto rounded-xl bg-gray-950 p-3 font-mono text-xs text-gray-200">
          <details v-for="entry in eventLogs" :key="entry.id" class="rounded-lg bg-white/5 px-3 py-2">
            <summary class="cursor-pointer select-none text-gray-300">
              <span class="mr-2 text-gray-500">{{ entry.at }}</span>
              <span :class="entry.direction === 'server' ? 'text-emerald-400' : entry.direction === 'client' ? 'text-blue-400' : 'text-amber-400'">
                {{ entry.direction }}
              </span>
              <span class="ml-2">{{ entry.type }}</span>
            </summary>
            <pre class="mt-2 overflow-x-auto whitespace-pre-wrap break-all text-[11px] leading-5 text-gray-400">{{ entry.data }}</pre>
          </details>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'

type VoiceMode = 'vp' | 'vps' | 'wm'
type TestStatus = 'idle' | 'requestingMicrophone' | 'signaling' | 'connecting' | 'connected' | 'disconnected' | 'failed' | 'stopped'
type LogDirection = 'client' | 'server' | 'system'

interface TranscriptItem {
  id: number
  role: 'user' | 'assistant'
  text: string
}

interface EventLogEntry {
  id: number
  at: string
  direction: LogDirection
  type: string
  data: string
}

const { t } = useI18n()

const apiKey = ref('')
const proofToken = ref('')
const mode = ref<VoiceMode>('vp')
const sessionJson = ref('')
const status = ref<TestStatus>('idle')
const sessionStarting = ref(false)
const errorMessage = ref('')
const microphoneMuted = ref(false)
const elapsedSeconds = ref(0)
const peerConnectionState = ref('new')
const iceConnectionState = ref('new')
const signalingState = ref('stable')
const transcripts = ref<TranscriptItem[]>([])
const eventLogs = ref<EventLogEntry[]>([])
const remoteAudio = ref<HTMLAudioElement | null>(null)
const localStream = ref<MediaStream | null>(null)

let peerConnection: RTCPeerConnection | null = null
let dataChannel: RTCDataChannel | null = null
let requestController: AbortController | null = null
let elapsedTimer: ReturnType<typeof setInterval> | null = null
let disconnectedCleanupTimer: ReturnType<typeof setTimeout> | null = null
let sessionGeneration = 0
let nextTranscriptId = 1
let nextEventId = 1
let activeAssistantTranscriptId: number | null = null
let activeUserTranscriptId: number | null = null

const endpoint = computed(() => `/v1/realtime/${mode.value}`)
const isActive = computed(() => ['requestingMicrophone', 'signaling', 'connecting'].includes(status.value))
const hasSession = computed(() => sessionStarting.value || localStream.value !== null)
const statusLabel = computed(() => t(`admin.realtimeVoiceTest.state.${status.value}`))
const formattedDuration = computed(() => {
  const minutes = Math.floor(elapsedSeconds.value / 60).toString().padStart(2, '0')
  const seconds = (elapsedSeconds.value % 60).toString().padStart(2, '0')
  return `${minutes}:${seconds}`
})
const statusDotClass = computed(() => {
  if (status.value === 'connected') return 'bg-emerald-500 shadow-[0_0_0_4px_rgba(16,185,129,0.15)]'
  if (status.value === 'failed' || status.value === 'disconnected') return 'bg-red-500 shadow-[0_0_0_4px_rgba(239,68,68,0.15)]'
  if (isActive.value) return 'animate-pulse bg-amber-500 shadow-[0_0_0_4px_rgba(245,158,11,0.15)]'
  return 'bg-gray-400'
})

function buildSessionTemplate(selectedMode: VoiceMode): Record<string, unknown> {
  const template: Record<string, unknown> = {
    voice_mode: selectedMode === 'vp' ? 'advanced' : selectedMode === 'vps' ? 'standard' : 'wingman',
    voice: 'alloy',
    requested_default_model: 'gpt-4o-realtime'
  }
  if (selectedMode === 'wm') template.session_type = 'wingman'
  return template
}

function resetSessionTemplate(): void {
  sessionJson.value = JSON.stringify(buildSessionTemplate(mode.value), null, 2)
}

watch(mode, () => resetSessionTemplate(), { immediate: true })

function appendEvent(direction: LogDirection, type: string, payload: unknown): void {
  let data: string
  if (typeof payload === 'string') {
    data = payload
  } else {
    try {
      data = JSON.stringify(payload, null, 2)
    } catch {
      data = String(payload)
    }
  }
  if (data.length > 12000) data = `${data.slice(0, 12000)}\n…truncated…`
  eventLogs.value.push({
    id: nextEventId++,
    at: new Date().toLocaleTimeString(),
    direction,
    type,
    data
  })
  if (eventLogs.value.length > 100) eventLogs.value.splice(0, eventLogs.value.length - 100)
}

function addTranscript(role: 'user' | 'assistant', text: string): number | null {
  const trimmed = text.trim()
  if (!trimmed) return null
  const id = nextTranscriptId++
  transcripts.value.push({ id, role, text: trimmed })
  if (transcripts.value.length > 100) transcripts.value.splice(0, transcripts.value.length - 100)
  return id
}

function appendTranscriptDelta(role: 'user' | 'assistant', delta: string): void {
  if (!delta) return
  const activeId = role === 'assistant' ? activeAssistantTranscriptId : activeUserTranscriptId
  const existing = activeId === null ? undefined : transcripts.value.find(item => item.id === activeId)
  if (existing) {
    existing.text += delta
    return
  }
  const id = nextTranscriptId++
  transcripts.value.push({ id, role, text: delta })
  if (role === 'assistant') activeAssistantTranscriptId = id
  else activeUserTranscriptId = id
}

function finalizeTranscript(role: 'user' | 'assistant', transcript?: string): void {
  const activeId = role === 'assistant' ? activeAssistantTranscriptId : activeUserTranscriptId
  const existing = activeId === null ? undefined : transcripts.value.find(item => item.id === activeId)
  if (existing && transcript?.trim()) existing.text = transcript.trim()
  else if (!existing && transcript?.trim()) addTranscript(role, transcript)
  if (role === 'assistant') activeAssistantTranscriptId = null
  else activeUserTranscriptId = null
}

function processRealtimeEvent(payload: Record<string, unknown>): void {
  const type = typeof payload.type === 'string' ? payload.type : 'message'
  const delta = typeof payload.delta === 'string' ? payload.delta : ''
  const transcript = typeof payload.transcript === 'string' ? payload.transcript : ''

  if (type === 'conversation.item.input_audio_transcription.delta') appendTranscriptDelta('user', delta)
  if (type === 'conversation.item.input_audio_transcription.completed') finalizeTranscript('user', transcript)
  if (type === 'response.audio_transcript.delta' || type === 'response.output_audio_transcript.delta') appendTranscriptDelta('assistant', delta)
  if (type === 'response.audio_transcript.done' || type === 'response.output_audio_transcript.done') finalizeTranscript('assistant', transcript)
}

async function handleDataChannelMessage(event: MessageEvent): Promise<void> {
  let raw = ''
  if (typeof event.data === 'string') raw = event.data
  else if (event.data instanceof Blob) raw = await event.data.text()
  else if (event.data instanceof ArrayBuffer) raw = new TextDecoder().decode(event.data)
  else raw = String(event.data)

  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const type = typeof parsed.type === 'string' ? parsed.type : 'message'
    appendEvent('server', type, parsed)
    processRealtimeEvent(parsed)
  } catch {
    appendEvent('server', 'message', raw)
  }
}

function bindDataChannel(channel: RTCDataChannel): void {
  dataChannel = channel
  channel.onopen = () => appendEvent('system', 'data_channel.open', { label: channel.label })
  channel.onclose = () => appendEvent('system', 'data_channel.close', { label: channel.label })
  channel.onerror = () => appendEvent('system', 'data_channel.error', { label: channel.label })
  channel.onmessage = event => void handleDataChannelMessage(event)
}

function updatePeerStates(): void {
  if (!peerConnection) return
  peerConnectionState.value = peerConnection.connectionState
  iceConnectionState.value = peerConnection.iceConnectionState
  signalingState.value = peerConnection.signalingState
}

function resetPeerStates(): void {
  peerConnectionState.value = 'new'
  iceConnectionState.value = 'new'
  signalingState.value = 'stable'
}

function waitForIceGatheringComplete(pc: RTCPeerConnection, timeoutMs = 5000): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise(resolve => {
    let settled = false
    const finish = () => {
      if (settled) return
      settled = true
      pc.removeEventListener('icegatheringstatechange', onStateChange)
      clearTimeout(timeout)
      resolve()
    }
    const onStateChange = () => {
      if (pc.iceGatheringState === 'complete') finish()
    }
    const timeout = setTimeout(finish, timeoutMs)
    pc.addEventListener('icegatheringstatechange', onStateChange)
  })
}

function startElapsedTimer(): void {
  elapsedSeconds.value = 0
  if (elapsedTimer) clearInterval(elapsedTimer)
  elapsedTimer = setInterval(() => {
    elapsedSeconds.value += 1
  }, 1000)
}

function stopElapsedTimer(): void {
  if (elapsedTimer) clearInterval(elapsedTimer)
  elapsedTimer = null
}

function clearDisconnectedCleanupTimer(): void {
  if (disconnectedCleanupTimer) clearTimeout(disconnectedCleanupTimer)
  disconnectedCleanupTimer = null
}

function cleanupSession(): void {
  sessionGeneration += 1
  sessionStarting.value = false
  clearDisconnectedCleanupTimer()
  requestController?.abort()
  requestController = null
  if (dataChannel) {
    dataChannel.onopen = null
    dataChannel.onclose = null
    dataChannel.onerror = null
    dataChannel.onmessage = null
    dataChannel.close()
  }
  dataChannel = null
  if (peerConnection) {
    peerConnection.ondatachannel = null
    peerConnection.ontrack = null
    peerConnection.onconnectionstatechange = null
    peerConnection.oniceconnectionstatechange = null
    peerConnection.onsignalingstatechange = null
    peerConnection.close()
  }
  peerConnection = null
  localStream.value?.getTracks().forEach(track => track.stop())
  localStream.value = null
  if (remoteAudio.value) remoteAudio.value.srcObject = null
  microphoneMuted.value = false
  activeAssistantTranscriptId = null
  activeUserTranscriptId = null
  stopElapsedTimer()
  resetPeerStates()
}

async function startSession(): Promise<void> {
  errorMessage.value = ''
  if (hasSession.value) return
  if (!apiKey.value.trim()) {
    errorMessage.value = t('admin.realtimeVoiceTest.apiKeyRequired')
    return
  }
  if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia || typeof RTCPeerConnection === 'undefined') {
    errorMessage.value = t('admin.realtimeVoiceTest.microphoneUnsupported')
    return
  }

  let sessionPayload: Record<string, unknown>
  try {
    const parsed = JSON.parse(sessionJson.value) as unknown
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('invalid object')
    sessionPayload = parsed as Record<string, unknown>
  } catch {
    errorMessage.value = t('admin.realtimeVoiceTest.invalidSessionJson')
    return
  }

  cleanupSession()
  const generation = sessionGeneration
  sessionStarting.value = true
  startElapsedTimer()

  try {
    status.value = 'requestingMicrophone'
    const stream = await navigator.mediaDevices.getUserMedia({
      audio: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true
      }
    })
    if (generation !== sessionGeneration) {
      stream.getTracks().forEach(track => track.stop())
      return
    }
    localStream.value = stream
  } catch (error) {
    if (generation !== sessionGeneration) return
    errorMessage.value = error instanceof Error && error.message
      ? `${t('admin.realtimeVoiceTest.microphoneDenied')} ${error.message}`
      : t('admin.realtimeVoiceTest.microphoneDenied')
    appendEvent('system', 'microphone.error', { message: errorMessage.value })
    cleanupSession()
    status.value = 'failed'
    return
  }

  try {
    if (generation !== sessionGeneration || !localStream.value) return
    const pc = new RTCPeerConnection()
    peerConnection = pc
    localStream.value.getTracks().forEach(track => pc.addTrack(track, localStream.value as MediaStream))
    bindDataChannel(pc.createDataChannel('oai-events'))

    pc.ondatachannel = event => {
      if (!dataChannel || dataChannel.readyState === 'closed') bindDataChannel(event.channel)
    }
    pc.ontrack = event => {
      const stream = event.streams[0] ?? new MediaStream([event.track])
      if (remoteAudio.value) {
        remoteAudio.value.srcObject = stream
        void remoteAudio.value.play().catch(() => undefined)
      }
      appendEvent('system', 'remote_track', { kind: event.track.kind, id: event.track.id })
    }
    pc.onconnectionstatechange = () => {
      if (peerConnection !== pc) return
      updatePeerStates()
      appendEvent('system', 'peer_connection.state', { state: pc.connectionState })
      if (pc.connectionState === 'connected') {
        clearDisconnectedCleanupTimer()
        sessionStarting.value = false
        status.value = 'connected'
      } else if (pc.connectionState === 'failed') {
        errorMessage.value = t('admin.realtimeVoiceTest.peerConnectionFailed')
        cleanupSession()
        status.value = 'failed'
      } else if (pc.connectionState === 'closed') {
        cleanupSession()
        status.value = 'disconnected'
      } else if (pc.connectionState === 'disconnected') {
        status.value = 'disconnected'
        clearDisconnectedCleanupTimer()
        disconnectedCleanupTimer = setTimeout(() => {
          if (peerConnection !== pc || pc.connectionState !== 'disconnected') return
          appendEvent('system', 'peer_connection.disconnect_timeout', { grace_ms: 5000 })
          cleanupSession()
          status.value = 'disconnected'
        }, 5000)
      }
    }
    pc.oniceconnectionstatechange = updatePeerStates
    pc.onsignalingstatechange = updatePeerStates

    status.value = 'connecting'
    const offer = await pc.createOffer({ offerToReceiveAudio: true })
    await pc.setLocalDescription(offer)
    await waitForIceGatheringComplete(pc)
    if (generation !== sessionGeneration || peerConnection !== pc) return
    const offerSdp = pc.localDescription?.sdp
    if (!offerSdp) throw new Error('Failed to create local SDP offer')

    status.value = 'signaling'
    const controller = new AbortController()
    requestController = controller
    const form = new FormData()
    form.append('sdp', offerSdp)
    form.append('session', JSON.stringify(sessionPayload))
    const idempotencyKey = createIdempotencyKey()
    appendEvent('client', 'signaling.offer', {
      endpoint: endpoint.value,
      session: sessionPayload,
      sdp_size: offerSdp.length,
      idempotency_key: idempotencyKey
    })
    const headers: Record<string, string> = {
      Authorization: `Bearer ${apiKey.value.trim()}`,
      'Idempotency-Key': idempotencyKey
    }
    if (proofToken.value.trim()) headers['OpenAI-Sentinel-Proof-Token'] = proofToken.value.trim()

    const response = await fetch(endpoint.value, {
      method: 'POST',
      headers,
      body: form,
      signal: controller.signal
    })
    if (requestController === controller) requestController = null
    if (generation !== sessionGeneration || peerConnection !== pc) return
    const answerSdp = await response.text()
    if (!response.ok) throw new Error(answerSdp.trim() || `${response.status} ${response.statusText}`)
    if (!answerSdp.trim()) throw new Error(t('admin.realtimeVoiceTest.emptySdpAnswer'))

    appendEvent('server', 'signaling.answer', {
      status: response.status,
      request_id: response.headers.get('x-request-id') || response.headers.get('openai-request-id'),
      media_path: response.headers.get('x-realtime-media-path'),
      sdp_size: answerSdp.length
    })
    await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp })
    if (generation !== sessionGeneration || peerConnection !== pc) return
    sessionStarting.value = false
    status.value = 'connecting'
    updatePeerStates()
  } catch (error) {
    if (generation !== sessionGeneration) return
    if (error instanceof DOMException && error.name === 'AbortError') {
      cleanupSession()
      status.value = 'stopped'
      return
    }
    const detail = error instanceof Error ? error.message : String(error)
    errorMessage.value = `${t('admin.realtimeVoiceTest.requestFailed')} ${detail}`.trim()
    appendEvent('system', 'session.error', { message: detail })
    cleanupSession()
    status.value = 'failed'
  }
}

function createIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`
}

function stopSession(): void {
  appendEvent('system', 'session.stop', {})
  cleanupSession()
  status.value = 'stopped'
}

function toggleMicrophone(): void {
  microphoneMuted.value = !microphoneMuted.value
  localStream.value?.getAudioTracks().forEach(track => {
    track.enabled = !microphoneMuted.value
  })
  appendEvent('system', 'microphone.mute', { muted: microphoneMuted.value })
}

function clearResults(): void {
  transcripts.value = []
  eventLogs.value = []
  activeAssistantTranscriptId = null
  activeUserTranscriptId = null
}

onBeforeUnmount(() => cleanupSession())
</script>
