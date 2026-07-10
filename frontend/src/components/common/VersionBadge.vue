<template>
  <span
    v-if="displayVersion"
    class="inline-flex items-center rounded-lg bg-gray-100 px-2 py-1 text-xs font-medium text-gray-600 dark:bg-dark-800 dark:text-dark-400"
    :title="badgeTitle"
  >
    {{ displayVersion }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { BUILD_HASH, BUILD_TIME } from '@/generated/buildHash'

const props = defineProps<{
  version?: string
}>()

const displayVersion = computed(() => {
  if (BUILD_HASH) {
    return BUILD_HASH
  }
  return props.version?.trim() || ''
})

const badgeTitle = computed(() => {
  const details = [`Build: ${displayVersion.value}`]
  if (BUILD_TIME) {
    details.push(`Build time: ${BUILD_TIME}`)
  }
  return details.join('\n')
})
</script>
