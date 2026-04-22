<template>
  <div class="card p-4">
    <div class="mb-4 flex items-start justify-between gap-3">
      <div>
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.usage.userPeriodRanking') }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.usage.userPeriodRankingHint') }}
        </p>
      </div>
    </div>

    <div v-if="loading" class="flex h-56 items-center justify-center">
      <LoadingSpinner />
    </div>
    <div
      v-else-if="error"
      class="flex h-56 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('admin.usage.failedToLoad') }}
    </div>
    <div v-else-if="items.length === 0" class="h-56">
      <EmptyState :message="t('admin.usage.userPeriodRankingEmpty')" />
    </div>
    <div v-else class="overflow-x-auto">
      <table class="min-w-full text-sm">
        <thead>
          <tr class="border-b border-gray-100 text-left text-xs uppercase tracking-wide text-gray-500 dark:border-gray-700 dark:text-gray-400">
            <th class="pb-3 pr-4 font-medium">{{ t('admin.usage.rank') }}</th>
            <th class="pb-3 pr-4 font-medium">{{ t('admin.dashboard.spendingRankingUser') }}</th>
            <th class="pb-3 pr-4 text-right font-medium">{{ t('admin.dashboard.spendingRankingTokens') }}</th>
            <th class="pb-3 pr-4 text-right font-medium">{{ t('admin.dashboard.spendingRankingSpend') }}</th>
            <th class="pb-3 text-right font-medium">{{ t('admin.usage.periodShare') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(item, index) in items"
            :key="`${item.user_id}-${index}`"
            class="border-b border-gray-100 last:border-b-0 transition-colors hover:bg-gray-50 dark:border-gray-800 dark:hover:bg-dark-700/40"
          >
            <td class="py-3 pr-4">
              <span class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-gray-100 text-xs font-semibold text-gray-700 dark:bg-dark-700 dark:text-gray-200">
                {{ index + 1 }}
              </span>
            </td>
            <td class="py-3 pr-4">
              <button
                type="button"
                class="max-w-[240px] truncate text-left font-medium text-blue-600 transition-colors hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300"
                :title="item.email || fallbackUserLabel(item.user_id)"
                @click="$emit('userClick', item.user_id)"
              >
                {{ item.email || fallbackUserLabel(item.user_id) }}
              </button>
            </td>
            <td class="py-3 pr-4 text-right font-medium text-gray-900 dark:text-white">
              {{ formatTokens(item.total_tokens) }}
            </td>
            <td class="py-3 pr-4 text-right font-medium text-emerald-600 dark:text-emerald-400">
              ${{ formatCost(item.actual_cost) }}
            </td>
            <td class="py-3 text-right">
              <div class="font-medium text-gray-900 dark:text-white">
                {{ formatPercentage(tokenShare(item.total_tokens)) }}
              </div>
              <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.usage.costShareShort') }} {{ formatPercentage(costShare(item.actual_cost)) }}
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import type { UserBreakdownItem } from '@/types'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  items: UserBreakdownItem[]
  totalTokens?: number
  totalActualCost?: number
  loading?: boolean
  error?: boolean
}>(), {
  totalTokens: 0,
  totalActualCost: 0,
  loading: false,
  error: false,
})

defineEmits<{
  userClick: [userId: number]
}>()

const fallbackUserLabel = (userId: number): string => t('admin.redeem.userPrefix', { id: userId })

const tokenShare = (value: number): number => {
  if (!props.totalTokens || props.totalTokens <= 0) return 0
  return value / props.totalTokens
}

const costShare = (value: number): number => {
  if (!props.totalActualCost || props.totalActualCost <= 0) return 0
  return value / props.totalActualCost
}

const formatTokens = (value: number): string => {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`
  return value.toLocaleString()
}

const formatCost = (value: number): string => {
  if (value >= 1000) return `${(value / 1000).toFixed(2)}K`
  if (value >= 1) return value.toFixed(2)
  if (value >= 0.01) return value.toFixed(3)
  return value.toFixed(4)
}

const formatPercentage = (value: number): string => `${(value * 100).toFixed(1)}%`
</script>
