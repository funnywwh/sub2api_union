<template>
  <BaseDialog :show="show" :title="t('admin.users.importKeyTitle')" width="narrow" @close="handleClose">
    <form v-if="user" class="space-y-4" @submit.prevent="handleSubmit">
      <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.users.importKeyTarget') }}</p>
        <p class="mt-1 font-medium text-gray-900 dark:text-white">{{ user.email }}</p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.users.importKeyNameLabel') }}</label>
        <input
          v-model="form.name"
          type="text"
          maxlength="100"
          class="input"
          :placeholder="t('admin.users.importKeyNamePlaceholder')"
          @input="handleNameInput"
        />
        <p class="input-hint">{{ t('admin.users.importKeyNameHint') }}</p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.users.importKeyInputLabel') }}</label>
        <input
          v-model.trim="form.key"
          type="text"
          class="input font-mono"
          :placeholder="t('admin.users.importKeyPlaceholder')"
        />
        <p v-if="customKeyError" class="mt-1 text-sm text-red-500">{{ customKeyError }}</p>
        <p v-else class="input-hint">{{ t('admin.users.importKeyHint') }}</p>
      </div>

      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button type="submit" class="btn btn-primary" :disabled="submitting">
          {{ submitting ? t('admin.users.importingKey') : t('admin.users.confirmImportKey') }}
        </button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AdminUser } from '@/types'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{
  show: boolean
  user: AdminUser | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'success'): void
}>()

const { t } = useI18n()
const appStore = useAppStore()

const form = ref({
  name: '',
  key: ''
})
const submitting = ref(false)
const nameManuallyEdited = ref(false)

const buildSuggestedName = (key: string) => {
  const trimmedKey = key.trim()
  if (!trimmedKey) return ''
  const suffix = trimmedKey.length > 8 ? trimmedKey.slice(-8) : trimmedKey
  return `Imported Key ${suffix}`
}

const resetForm = () => {
  form.value.name = ''
  form.value.key = ''
  nameManuallyEdited.value = false
}

watch(() => props.show, (show) => {
  if (show) {
    resetForm()
  }
})

watch(() => form.value.key, (key) => {
  if (!nameManuallyEdited.value) {
    form.value.name = buildSuggestedName(key)
  }
})

const customKeyError = computed(() => {
  const key = form.value.key.trim()
  if (!key) return ''
  if (key.length < 16) {
    return t('admin.users.importKeyTooShort')
  }
  if (!/^[a-zA-Z0-9_-]+$/.test(key)) {
    return t('admin.users.importKeyInvalidChars')
  }
  return ''
})

const resolveImportErrorMessage = (error: any) => {
  switch (error?.reason) {
    case 'API_KEY_TOO_SHORT':
      return t('admin.users.importKeyTooShort')
    case 'API_KEY_INVALID_CHARS':
      return t('admin.users.importKeyInvalidChars')
    case 'API_KEY_EXISTS':
      return t('admin.users.importKeyExists')
    case 'API_KEY_RATE_LIMITED':
      return t('admin.users.importKeyRateLimited')
    default:
      return error?.message || t('admin.users.importKeyFailed')
  }
}

const handleClose = () => {
  if (submitting.value) return
  resetForm()
  emit('close')
}

const handleNameInput = () => {
  nameManuallyEdited.value = true
}

const handleSubmit = async () => {
  if (!props.user) return

  const key = form.value.key.trim()
  const name = form.value.name.trim()
  if (!key) {
    appStore.showError(t('admin.users.importKeyRequired'))
    return
  }
  if (customKeyError.value) {
    appStore.showError(customKeyError.value)
    return
  }

  submitting.value = true
  try {
    await adminAPI.users.importUserApiKey(props.user.id, key, name)
    appStore.showSuccess(t('admin.users.importKeySuccess'))
    resetForm()
    emit('success')
    emit('close')
  } catch (error: any) {
    appStore.showError(resolveImportErrorMessage(error))
  } finally {
    submitting.value = false
  }
}
</script>
