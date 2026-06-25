<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
          <span class="text-lg font-medium text-primary-700 dark:text-primary-300">{{ user.email.charAt(0).toUpperCase() }}</span>
        </div>
        <div><p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p><p class="text-sm text-gray-500 dark:text-dark-400">{{ user.username }}</p></div>
      </div>
      <div v-if="loading" class="flex justify-center py-8"><svg class="h-8 w-8 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg></div>
      <div v-else-if="apiKeys.length === 0" class="py-8 text-center"><p class="text-sm text-gray-500">{{ t('admin.users.noApiKeys') }}</p></div>
      <div v-else ref="scrollContainerRef" class="max-h-96 space-y-3 overflow-y-auto" @scroll="closeGroupSelector">
        <div v-for="key in apiKeys" :key="key.id" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-start justify-between">
            <div class="min-w-0 flex-1">
              <div class="mb-1 flex items-center gap-2"><span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span><span :class="['badge text-xs', key.status === 'active' ? 'badge-success' : 'badge-danger']">{{ key.status }}</span></div>
              <p class="truncate font-mono text-sm text-gray-500">{{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}</p>
            </div>
            <button
              type="button"
              class="btn btn-secondary btn-sm ml-3 shrink-0 whitespace-nowrap"
              :disabled="transferSubmitting"
              @click="openTransferDialog(key)"
            >
              <Icon name="arrowRight" size="sm" />
              {{ t('admin.users.transferKey') }}
            </button>
          </div>
          <div class="mt-3 flex flex-wrap gap-4 text-xs text-gray-500">
            <div class="flex items-center gap-1">
              <span>{{ t('admin.users.group') }}:</span>
              <button
                :ref="(el) => setGroupButtonRef(key.id, el)"
                @click="openGroupSelector(key)"
                class="-mx-1 -my-0.5 flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                :disabled="updatingKeyIds.has(key.id)"
              >
                <GroupBadge
                  v-if="key.group_id && key.group"
                  :name="key.group.name"
                  :platform="key.group.platform"
                  :subscription-type="key.group.subscription_type"
                  :rate-multiplier="key.group.rate_multiplier"
                />
                <span v-else class="text-gray-400 italic">{{ t('admin.users.none') }}</span>
                <svg v-if="updatingKeyIds.has(key.id)" class="h-3 w-3 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg>
                <svg v-else class="h-3 w-3 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9" /></svg>
              </button>
            </div>
            <div class="flex items-center gap-1"><span>{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span></div>
          </div>
        </div>
      </div>
    </div>
  </BaseDialog>

  <!-- Group Selector Dropdown -->
  <Teleport to="body">
    <div
      v-if="groupSelectorKeyId !== null && dropdownPosition"
      ref="dropdownRef"
      class="animate-in fade-in slide-in-from-top-2 fixed z-[100000020] w-64 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 duration-200 dark:bg-dark-800 dark:ring-white/10"
      :style="{ top: dropdownPosition.top + 'px', left: dropdownPosition.left + 'px' }"
    >
      <div class="max-h-64 overflow-y-auto p-1.5">
        <!-- Unbind option -->
        <button
          @click="changeGroup(selectedKeyForGroup!, null)"
          :class="[
            'flex w-full items-center rounded-lg px-3 py-2 text-sm transition-colors',
            !selectedKeyForGroup?.group_id
              ? 'bg-primary-50 dark:bg-primary-900/20'
              : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <span class="text-gray-500 italic">{{ t('admin.users.none') }}</span>
          <svg
            v-if="!selectedKeyForGroup?.group_id"
            class="ml-auto h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400"
            fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"
          ><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
        </button>
        <!-- Group options -->
        <button
          v-for="group in allGroups"
          :key="group.id"
          @click="changeGroup(selectedKeyForGroup!, group.id)"
          :class="[
            'flex w-full items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors',
            selectedKeyForGroup?.group_id === group.id
              ? 'bg-primary-50 dark:bg-primary-900/20'
              : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <GroupOptionItem
            :name="group.name"
            :platform="group.platform"
            :subscription-type="group.subscription_type"
            :rate-multiplier="group.rate_multiplier"
            :description="group.description"
            :selected="selectedKeyForGroup?.group_id === group.id"
          />
        </button>
      </div>
    </div>
  </Teleport>

  <BaseDialog
    :show="transferDialogOpen"
    :title="t('admin.users.transferKeyTitle')"
    width="narrow"
    :z-index="60"
    @close="closeTransferDialog"
  >
    <form v-if="transferKey" class="space-y-4" @submit.prevent="handleTransferSubmit">
      <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ transferKey.name }}</p>
        <p class="mt-1 truncate font-mono text-sm text-gray-900 dark:text-white">
          {{ transferKey.key.substring(0, 20) }}...{{ transferKey.key.substring(transferKey.key.length - 8) }}
        </p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.users.transferKeyNameLabel') }}</label>
        <input
          v-model="transferKeyName"
          type="text"
          class="input"
          :placeholder="t('admin.users.transferKeyNamePlaceholder')"
          autocomplete="off"
          maxlength="100"
        />
        <p class="input-hint">{{ t('admin.users.transferKeyNameHint') }}</p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.users.transferKeyTargetLabel') }}</label>
        <input
          v-model="transferUserQuery"
          type="text"
          class="input"
          :placeholder="t('admin.users.transferKeyTargetPlaceholder')"
          autocomplete="off"
        />
        <p class="input-hint">{{ t('admin.users.transferKeyTargetHint') }}</p>

        <div
          v-if="transferUserQuery.trim().length > 0"
          class="mt-2 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800"
        >
          <div v-if="transferUserLoading" class="px-3 py-2 text-sm text-gray-500 dark:text-dark-400">
            {{ t('common.loading') }}
          </div>
          <template v-else>
            <button
              v-for="target in transferUserResults"
              :key="target.id"
              type="button"
              :class="[
                'flex w-full items-center gap-3 px-3 py-2 text-left transition-colors',
                selectedTransferUser?.id === target.id
                  ? 'bg-primary-50 dark:bg-primary-900/20'
                  : 'hover:bg-gray-50 dark:hover:bg-dark-700'
              ]"
              @click="selectTransferUser(target)"
            >
              <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary-100 text-sm font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
                {{ target.email.charAt(0).toUpperCase() }}
              </span>
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium text-gray-900 dark:text-white">{{ target.email }}</span>
                <span class="block truncate text-xs text-gray-500 dark:text-dark-400">{{ target.username || '-' }}</span>
              </span>
              <svg
                v-if="selectedTransferUser?.id === target.id"
                class="h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400"
                fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"
              ><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
            </button>
          </template>
          <div
            v-if="!transferUserLoading && transferUserQuery.trim().length >= 2 && transferUserResults.length === 0"
            class="px-3 py-2 text-sm text-gray-500 dark:text-dark-400"
          >
            {{ t('admin.users.transferKeyNoUsers') }}
          </div>
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('admin.users.transferKeyGroupLabel') }}</label>
        <div class="mt-2 max-h-52 overflow-y-auto rounded-lg border border-gray-200 bg-white p-1.5 dark:border-dark-600 dark:bg-dark-800">
          <button
            type="button"
            :class="[
              'flex w-full items-center rounded-lg px-3 py-2 text-sm transition-colors',
              transferGroupId === null
                ? 'bg-primary-50 dark:bg-primary-900/20'
                : 'hover:bg-gray-50 dark:hover:bg-dark-700'
            ]"
            @click="transferGroupId = null"
          >
            <span class="text-gray-500 italic">{{ t('admin.users.none') }}</span>
            <svg
              v-if="transferGroupId === null"
              class="ml-auto h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400"
              fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"
            ><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
          </button>
          <button
            v-for="group in transferGroups"
            :key="group.id"
            type="button"
            :class="[
              'flex w-full items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors',
              transferGroupId === group.id
                ? 'bg-primary-50 dark:bg-primary-900/20'
                : 'hover:bg-gray-50 dark:hover:bg-dark-700'
            ]"
            @click="transferGroupId = group.id"
          >
            <GroupOptionItem
              :name="group.name"
              :platform="group.platform"
              :subscription-type="group.subscription_type"
              :rate-multiplier="group.rate_multiplier"
              :description="group.description"
              :selected="transferGroupId === group.id"
            />
          </button>
          <div v-if="transferGroupsLoading" class="px-3 py-2 text-sm text-gray-500 dark:text-dark-400">
            {{ t('admin.users.transferKeyGroupsLoading') }}
          </div>
          <div
            v-else-if="!selectedTransferUser"
            class="px-3 py-2 text-sm text-gray-500 dark:text-dark-400"
          >
            {{ t('admin.users.transferKeySelectUserFirst') }}
          </div>
          <div
            v-else-if="transferGroups.length === 0"
            class="px-3 py-2 text-sm text-gray-500 dark:text-dark-400"
          >
            {{ t('admin.users.transferKeyNoValidSubscriptionGroups') }}
          </div>
        </div>
        <p class="input-hint">{{ t('admin.users.transferKeyGroupHint') }}</p>
      </div>

      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="transferSubmitting" @click="() => closeTransferDialog()">
          {{ t('common.cancel') }}
        </button>
        <button type="submit" class="btn btn-primary" :disabled="transferSubmitting || transferGroupsLoading">
          {{ transferSubmitting ? t('admin.users.transferringKey') : t('admin.users.confirmTransferKey') }}
        </button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser, AdminGroup, ApiKey, Group, UserSubscription } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const apiKeys = ref<ApiKey[]>([])
const allGroups = ref<AdminGroup[]>([])
const loading = ref(false)
const updatingKeyIds = ref(new Set<number>())
const groupSelectorKeyId = ref<number | null>(null)
const dropdownPosition = ref<{ top: number; left: number } | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const scrollContainerRef = ref<HTMLElement | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())
const transferDialogOpen = ref(false)
const transferKey = ref<ApiKey | null>(null)
const transferKeyName = ref('')
const transferUserQuery = ref('')
const transferUserResults = ref<AdminUser[]>([])
const selectedTransferUser = ref<AdminUser | null>(null)
const transferGroupId = ref<number | null>(null)
const transferGroups = ref<Group[]>([])
const transferUserLoading = ref(false)
const transferGroupsLoading = ref(false)
const transferSubmitting = ref(false)
let transferSearchTimer: ReturnType<typeof setTimeout> | null = null
let transferSearchSeq = 0
let transferGroupsSeq = 0
const MAX_TRANSFER_KEY_NAME_LENGTH = 100

const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

watch(() => props.show, (v) => {
  if (v && props.user) {
    load()
    loadGroups()
  } else {
    closeGroupSelector()
    closeTransferDialog(true)
  }
})

watch(transferUserQuery, (query) => {
  scheduleTransferUserSearch(query)
})

const load = async () => {
  if (!props.user) return
  loading.value = true
  groupButtonRefs.value.clear()
  try {
    const res = await adminAPI.users.getUserApiKeys(props.user.id)
    apiKeys.value = res.items || []
  } catch (error) {
    console.error('Failed to load API keys:', error)
  } finally {
    loading.value = false
  }
}

const loadGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    allGroups.value = groups
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

const DROPDOWN_HEIGHT = 272 // max-h-64 = 16rem = 256px + padding
const DROPDOWN_GAP = 4

const openGroupSelector = (key: ApiKey) => {
  if (groupSelectorKeyId.value === key.id) {
    closeGroupSelector()
  } else {
    const buttonEl = groupButtonRefs.value.get(key.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()
      const spaceBelow = window.innerHeight - rect.bottom
      const openUpward = spaceBelow < DROPDOWN_HEIGHT && rect.top > spaceBelow
      dropdownPosition.value = {
        top: openUpward ? rect.top - DROPDOWN_HEIGHT - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
        left: rect.left
      }
    }
    groupSelectorKeyId.value = key.id
  }
}

const closeGroupSelector = () => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  closeGroupSelector()
  if (key.group_id === newGroupId || (!key.group_id && newGroupId === null)) return

  updatingKeyIds.value.add(key.id)
  try {
    const result = await adminAPI.apiKeys.updateApiKeyGroup(key.id, newGroupId)
    // Update local data
    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(t('admin.users.groupChangedWithGrant', { group: result.granted_group_name }))
    } else {
      appStore.showSuccess(t('admin.users.groupChangedSuccess'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

const clearTransferSearchTimer = () => {
  if (transferSearchTimer !== null) {
    clearTimeout(transferSearchTimer)
    transferSearchTimer = null
  }
}

const normalizeUserSearch = (value: string) => value.trim().toLowerCase()

const isExactTransferUserMatch = (user: AdminUser, query: string) => {
  const normalized = normalizeUserSearch(query)
  return normalizeUserSearch(user.email) === normalized || normalizeUserSearch(user.username || '') === normalized
}

const firstNonEmpty = (...values: Array<string | null | undefined>) => {
  for (const value of values) {
    const trimmed = value?.trim()
    if (trimmed) return trimmed
  }
  return ''
}

const limitTransferKeyName = (name: string) =>
  Array.from(name).slice(0, MAX_TRANSFER_KEY_NAME_LENGTH).join('')

const getDefaultTransferKeyName = (key: ApiKey) => {
  const sourceUser = props.user
  return limitTransferKeyName(firstNonEmpty(key.name, sourceUser?.notes, sourceUser?.username, sourceUser?.email))
}

const clearTransferGroups = () => {
  transferGroupsSeq++
  transferGroups.value = []
  transferGroupId.value = null
  transferGroupsLoading.value = false
}

const scheduleTransferUserSearch = (query: string) => {
  clearTransferSearchTimer()
  const trimmed = query.trim()
  const selectedExactMatch = selectedTransferUser.value
    ? isExactTransferUserMatch(selectedTransferUser.value, trimmed)
    : false

  if (selectedTransferUser.value && !selectedExactMatch) {
    selectedTransferUser.value = null
    clearTransferGroups()
  }

  if (trimmed.length < 2) {
    transferSearchSeq++
    transferUserResults.value = selectedExactMatch && selectedTransferUser.value ? [selectedTransferUser.value] : []
    transferUserLoading.value = false
    if (!selectedExactMatch) {
      clearTransferGroups()
    }
    return
  }

  transferSearchTimer = setTimeout(() => {
    void searchTransferUsers(trimmed)
  }, 250)
}

const searchTransferUsers = async (query: string) => {
  const seq = ++transferSearchSeq
  transferUserLoading.value = true
  try {
    const res = await adminAPI.users.list(1, 8, {
      search: query,
      status: 'active',
      include_subscriptions: false
    })
    if (seq !== transferSearchSeq) return
    transferUserResults.value = (res.items || []).filter((u) => u.id !== props.user?.id)
  } catch (error) {
    if (seq === transferSearchSeq) {
      transferUserResults.value = []
    }
    console.error('Failed to search users:', error)
  } finally {
    if (seq === transferSearchSeq) {
      transferUserLoading.value = false
    }
  }
}

const getValidTransferGroups = (subscriptions: UserSubscription[]) => {
  const now = Date.now()
  const seenGroupIds = new Set<number>()
  const groups: Group[] = []

  for (const subscription of subscriptions) {
    const group = subscription.group
    const expiresAt = subscription.expires_at ? Date.parse(subscription.expires_at) : Number.NaN

    if (
      subscription.status !== 'active' ||
      !group ||
      group.status !== 'active' ||
      group.subscription_type !== 'subscription' ||
      Number.isNaN(expiresAt) ||
      expiresAt <= now ||
      seenGroupIds.has(group.id)
    ) {
      continue
    }

    seenGroupIds.add(group.id)
    groups.push(group)
  }

  return groups
}

const loadTransferGroups = async (targetUserId: number) => {
  const seq = ++transferGroupsSeq
  transferGroups.value = []
  transferGroupId.value = null
  transferGroupsLoading.value = true

  try {
    const subscriptions = await adminAPI.subscriptions.listByUser(targetUserId)
    if (seq !== transferGroupsSeq) return
    transferGroups.value = getValidTransferGroups(subscriptions)
  } catch (error) {
    if (seq === transferGroupsSeq) {
      transferGroups.value = []
      appStore.showError(t('admin.users.transferKeyGroupLoadFailed'))
    }
    console.error('Failed to load transfer groups:', error)
  } finally {
    if (seq === transferGroupsSeq) {
      transferGroupsLoading.value = false
    }
  }
}

const openTransferDialog = (key: ApiKey) => {
  closeGroupSelector()
  transferKey.value = key
  transferKeyName.value = getDefaultTransferKeyName(key)
  transferUserQuery.value = ''
  transferUserResults.value = []
  selectedTransferUser.value = null
  clearTransferGroups()
  transferDialogOpen.value = true
}

const closeTransferDialog = (force = false) => {
  if (transferSubmitting.value && !force) return
  clearTransferSearchTimer()
  transferSearchSeq++
  transferDialogOpen.value = false
  transferKey.value = null
  transferKeyName.value = ''
  transferUserQuery.value = ''
  transferUserResults.value = []
  selectedTransferUser.value = null
  transferUserLoading.value = false
  clearTransferGroups()
}

const selectTransferUser = (target: AdminUser) => {
  selectedTransferUser.value = target
  transferUserQuery.value = target.username || target.email
  transferUserResults.value = [target]
  void loadTransferGroups(target.id)
}

const resolveTransferTarget = () => {
  const query = transferUserQuery.value.trim()
  if (selectedTransferUser.value && isExactTransferUserMatch(selectedTransferUser.value, query)) {
    return selectedTransferUser.value
  }
  return transferUserResults.value.find((user) => isExactTransferUserMatch(user, query)) || null
}

const resolveTransferErrorMessage = (error: any) => {
  switch (error?.reason) {
    case 'USER_NOT_FOUND':
      return t('admin.users.transferKeyUserNotFound')
    case 'USER_NOT_ACTIVE':
      return t('admin.users.transferKeyUserNotActive')
    case 'INVALID_TARGET_USER_ID':
      return t('admin.users.transferKeyTargetRequired')
    case 'INVALID_KEY_NAME':
      return t('admin.users.transferKeyNameInvalid')
    case 'INVALID_GROUP_ID':
      return t('admin.users.transferKeyGroupInvalid')
    case 'GROUP_NOT_ACTIVE':
      return t('admin.users.transferKeyGroupNotActive')
    case 'SUBSCRIPTION_REQUIRED':
      return t('admin.users.transferKeySubscriptionRequired')
    default:
      return error?.message || t('admin.users.transferKeyFailed')
  }
}

const handleTransferSubmit = async () => {
  if (!transferKey.value) return
  const target = resolveTransferTarget()
  if (!target) {
    appStore.showError(t('admin.users.transferKeyTargetRequired'))
    return
  }
  if (target.id === props.user?.id) {
    appStore.showError(t('admin.users.transferKeyTargetSame'))
    return
  }
  const keyName = transferKeyName.value.trim()
  if (!keyName) {
    appStore.showError(t('admin.users.transferKeyNameRequired'))
    return
  }
  if (Array.from(keyName).length > MAX_TRANSFER_KEY_NAME_LENGTH) {
    appStore.showError(t('admin.users.transferKeyNameInvalid'))
    return
  }
  if (transferGroupId.value !== null && !transferGroups.value.some((group) => group.id === transferGroupId.value)) {
    appStore.showError(t('admin.users.transferKeySubscriptionRequired'))
    return
  }

  transferSubmitting.value = true
  try {
    const result = await adminAPI.apiKeys.transferApiKey(transferKey.value.id, target.id, transferGroupId.value, keyName)
    apiKeys.value = apiKeys.value.filter((k) => k.id !== transferKey.value?.id)
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(t('admin.users.transferKeySuccessWithGrant', { group: result.granted_group_name }))
    } else {
      appStore.showSuccess(t('admin.users.transferKeySuccess'))
    }
    closeTransferDialog(true)
  } catch (error: any) {
    appStore.showError(resolveTransferErrorMessage(error))
  } finally {
    transferSubmitting.value = false
  }
}

const handleKeyDown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && groupSelectorKeyId.value !== null) {
    event.stopPropagation()
    closeGroupSelector()
  }
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (dropdownRef.value && !dropdownRef.value.contains(target)) {
    // Check if the click is on one of the group trigger buttons
    for (const el of groupButtonRefs.value.values()) {
      if (el.contains(target)) return
    }
    closeGroupSelector()
  }
}

const handleClose = () => {
  closeGroupSelector()
  closeTransferDialog(true)
  emit('close')
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeyDown, true)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeyDown, true)
  clearTransferSearchTimer()
})
</script>
