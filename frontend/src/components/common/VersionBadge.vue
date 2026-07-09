<template>
  <span
    v-if="displayVersion"
    class="inline-flex items-center rounded-lg bg-gray-100 px-2 py-1 text-xs font-medium text-gray-600 dark:bg-dark-800 dark:text-dark-400"
  >
    {{ displayVersion }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { BUILD_HASH } from '@/generated/buildHash'

const props = defineProps<{
  version?: string
}>()

const displayVersion = computed(() => {
  if (BUILD_HASH) {
    return BUILD_HASH
  }
  return props.version?.trim() || ''
})
</script>
