<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.managedNodes.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.managedNodes.description') }}
      </p>
    </div>

    <div class="space-y-5 p-6">
      <div class="grid gap-4 lg:grid-cols-2">
        <div>
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.settings.managedNodes.name') }}
          </label>
          <input
            v-model="form.name"
            type="text"
            maxlength="100"
            class="input"
            :placeholder="t('admin.settings.managedNodes.namePlaceholder')"
          />
        </div>
        <div>
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.settings.managedNodes.scheme') }}
          </label>
          <select v-model="form.scheme" class="input">
            <option value="https">https</option>
            <option value="http">http</option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.settings.managedNodes.host') }}
          </label>
          <input
            v-model="form.host"
            type="text"
            class="input"
            :placeholder="t('admin.settings.managedNodes.hostPlaceholder')"
          />
        </div>
        <div>
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.settings.managedNodes.port') }}
          </label>
          <input
            v-model.number="form.port"
            type="number"
            min="1"
            max="65535"
            class="input"
            :placeholder="t('admin.settings.managedNodes.portPlaceholder')"
          />
        </div>
        <div class="lg:col-span-2">
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ editingId ? t('admin.settings.managedNodes.apiKeyOptional') : t('admin.settings.managedNodes.apiKey') }}
          </label>
          <input
            v-model="form.api_key"
            type="text"
            class="input font-mono text-sm"
            :placeholder="editingId
              ? t('admin.settings.managedNodes.apiKeyKeepPlaceholder')
              : t('admin.settings.managedNodes.apiKeyPlaceholder')"
          />
        </div>
        <div class="lg:col-span-2">
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.settings.managedNodes.descriptionLabel') }}
          </label>
          <textarea
            v-model="form.description"
            rows="3"
            maxlength="1000"
            class="input"
            :placeholder="t('admin.settings.managedNodes.descriptionPlaceholder')"
          ></textarea>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="operating"
          @click="submit"
        >
          {{ operating
            ? t('admin.settings.managedNodes.saving')
            : editingId
              ? t('admin.settings.managedNodes.update')
              : t('admin.settings.managedNodes.create') }}
        </button>
        <button
          v-if="editingId"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="operating"
          @click="resetForm"
        >
          {{ t('common.cancel') }}
        </button>
      </div>

      <div v-if="loading" class="flex items-center gap-2 text-gray-500">
        <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
        {{ t('common.loading') }}
      </div>

      <div v-else-if="nodes.length === 0" class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.managedNodes.empty') }}
      </div>

      <div v-else class="space-y-4">
        <div
          v-for="node in nodes"
          :key="node.id"
          class="rounded-xl border border-gray-200 p-4 dark:border-dark-600"
        >
          <div class="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
            <div class="space-y-2">
              <div class="flex flex-wrap items-center gap-2">
                <h3 class="font-medium text-gray-900 dark:text-white">{{ node.name }}</h3>
                <code class="rounded bg-gray-100 px-2 py-0.5 text-xs dark:bg-dark-700">
                  {{ node.scheme }}://{{ node.host }}:{{ node.port }}
                </code>
              </div>
              <code
                class="inline-block rounded bg-gray-100 px-2 py-1 font-mono text-sm text-gray-900 dark:bg-dark-700 dark:text-gray-100"
              >
                {{ node.masked_key }}
              </code>
              <p v-if="node.description" class="text-sm text-gray-500 dark:text-gray-400">
                {{ node.description }}
              </p>
              <div class="grid gap-2 text-xs text-gray-500 dark:text-gray-400 md:grid-cols-2">
                <div>{{ t('admin.settings.managedNodes.createdAt') }}: {{ formatTime(node.created_at) }}</div>
                <div>{{ t('admin.settings.managedNodes.updatedAt') }}: {{ formatTime(node.updated_at) }}</div>
              </div>
              <div v-if="remoteInfo[node.id]" class="rounded-lg bg-gray-50 p-3 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                <div>{{ t('admin.settings.managedNodes.remoteSite') }}: {{ remoteInfo[node.id]?.site_name }}</div>
                <div>{{ t('admin.settings.managedNodes.remoteAuth') }}: {{ remoteInfo[node.id]?.auth_method || '-' }}</div>
                <div v-if="remoteInfo[node.id]?.frontend_url">{{ t('admin.settings.managedNodes.remoteFrontend') }}: {{ remoteInfo[node.id]?.frontend_url }}</div>
              </div>
            </div>

            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="busyId === node.id"
                @click="testNode(node)"
              >
                {{ t('admin.settings.managedNodes.test') }}
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="busyId === node.id"
                @click="jumpToNode(node)"
              >
                {{ t('admin.settings.managedNodes.jump') }}
              </button>
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="busyId === node.id"
                @click="editNode(node)"
              >
                {{ t('common.edit') }}
              </button>
              <button
                type="button"
                class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
                :disabled="busyId === node.id"
                @click="removeNode(node)"
              >
                {{ t('common.delete') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores'
import type { ManagedNode, ManagedNodeRemoteInfo } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

interface ManagedNodeForm {
  name: string
  description: string
  scheme: 'http' | 'https'
  host: string
  port: number
  api_key: string
}

const { t, locale } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const operating = ref(false)
const busyId = ref<string | null>(null)
const editingId = ref<string | null>(null)
const nodes = ref<ManagedNode[]>([])
const remoteInfo = ref<Record<string, ManagedNodeRemoteInfo>>({})

const form = reactive<ManagedNodeForm>({
  name: '',
  description: '',
  scheme: 'https',
  host: '',
  port: 8080,
  api_key: '',
})

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

function resetForm() {
  editingId.value = null
  form.name = ''
  form.description = ''
  form.scheme = 'https'
  form.host = ''
  form.port = 8080
  form.api_key = ''
}

async function loadNodes() {
  loading.value = true
  try {
    nodes.value = await adminAPI.settings.listManagedNodes()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    loading.value = false
  }
}

function editNode(node: ManagedNode) {
  editingId.value = node.id
  form.name = node.name
  form.description = node.description || ''
  form.scheme = node.scheme || 'https'
  form.host = node.host
  form.port = node.port
  form.api_key = ''
}

async function submit() {
  if (!form.name.trim()) {
    appStore.showError(t('admin.settings.managedNodes.nameRequired'))
    return
  }
  if (!form.host.trim()) {
    appStore.showError(t('admin.settings.managedNodes.hostRequired'))
    return
  }
  if (!editingId.value && !form.api_key.trim()) {
    appStore.showError(t('admin.settings.managedNodes.apiKeyRequired'))
    return
  }

  operating.value = true
  try {
    if (editingId.value) {
      const updated = await adminAPI.settings.updateManagedNode(editingId.value, {
        name: form.name.trim(),
        description: form.description.trim(),
        scheme: form.scheme,
        host: form.host.trim(),
        port: Number(form.port),
        api_key: form.api_key.trim() || undefined,
      })
      nodes.value = nodes.value.map((item) => (item.id === updated.id ? updated : item))
      appStore.showSuccess(t('admin.settings.managedNodes.updated'))
    } else {
      const created = await adminAPI.settings.createManagedNode({
        name: form.name.trim(),
        description: form.description.trim(),
        scheme: form.scheme,
        host: form.host.trim(),
        port: Number(form.port),
        api_key: form.api_key.trim(),
      })
      nodes.value = [created, ...nodes.value]
      appStore.showSuccess(t('admin.settings.managedNodes.created'))
    }
    resetForm()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    operating.value = false
  }
}

async function removeNode(node: ManagedNode) {
  if (!confirm(t('admin.settings.managedNodes.deleteConfirm', { name: node.name }))) return

  busyId.value = node.id
  try {
    await adminAPI.settings.deleteManagedNode(node.id)
    nodes.value = nodes.value.filter((item) => item.id !== node.id)
    delete remoteInfo.value[node.id]
    if (editingId.value === node.id) {
      resetForm()
    }
    appStore.showSuccess(t('admin.settings.managedNodes.deleted'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    busyId.value = null
  }
}

async function testNode(node: ManagedNode) {
  busyId.value = node.id
  try {
    const info = await adminAPI.settings.testManagedNode(node.id)
    remoteInfo.value = { ...remoteInfo.value, [node.id]: info }
    appStore.showSuccess(t('admin.settings.managedNodes.testSuccess', { siteName: info.site_name || node.name }))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.managedNodes.testFailed')))
  } finally {
    busyId.value = null
  }
}

async function jumpToNode(node: ManagedNode) {
  const popup = window.open('about:blank', '_blank')
  if (!popup) {
    appStore.showError(t('admin.settings.managedNodes.popupBlocked'))
    return
  }

  busyId.value = node.id
  try {
    const result = await adminAPI.settings.createManagedNodeJumpLink(node.id)
    popup.location.href = result.login_url
  } catch (error: unknown) {
    popup.close()
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.managedNodes.jumpFailed')))
  } finally {
    busyId.value = null
  }
}

onMounted(() => {
  void loadNodes()
})
</script>
