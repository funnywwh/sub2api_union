<template>
  <div class="card p-4">
    <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.usage.userPeriodRanking') }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ rankingHint }}
        </p>
      </div>
      <div class="flex items-center gap-2">
        <span class="text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('admin.usage.rankingMode') }}
        </span>
        <div class="w-32">
          <Select :model-value="mode" :options="modeOptions" @change="handleModeChange" />
        </div>
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
            <th class="pb-3 pr-4 font-medium">{{ entityColumnLabel }}</th>
            <th class="pb-3 pr-4 text-right font-medium">{{ t('admin.dashboard.spendingRankingTokens') }}</th>
            <th class="pb-3 pr-4 text-right font-medium">{{ t('admin.dashboard.spendingRankingSpend') }}</th>
            <th class="pb-3 text-right font-medium">{{ t('admin.usage.periodShare') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(item, index) in items"
            :key="getItemKey(item, index)"
            class="border-b border-gray-100 last:border-b-0 transition-colors hover:bg-gray-50 dark:border-gray-800 dark:hover:bg-dark-700/40"
          >
            <td class="py-3 pr-4">
              <span class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-gray-100 text-xs font-semibold text-gray-700 dark:bg-dark-700 dark:text-gray-200">
                {{ index + 1 }}
              </span>
            </td>
            <td class="py-3 pr-4">
              <template v-if="rankBy === 'api_key'">
                <div class="max-w-[280px]">
                  <div class="truncate font-medium text-gray-900 dark:text-white" :title="apiKeyLabel(item)">
                    {{ apiKeyLabel(item) }}
                  </div>
                  <button
                    type="button"
                    class="mt-1 max-w-full truncate text-left text-xs text-blue-600 transition-colors hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300"
                    :title="item.email || fallbackUserLabel(item.user_id)"
                    @click="$emit('userClick', item.user_id)"
                  >
                    {{ item.email || fallbackUserLabel(item.user_id) }}
                  </button>
                </div>
              </template>
              <button
                v-else
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
                {{ formatPercentage(primaryShare(item)) }}
              </div>
              <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ secondaryShareLabel }} {{ formatPercentage(secondaryShare(item)) }}
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import type { UserBreakdownItem } from '@/types'

type RankingSortBy = 'tokens' | 'actual_cost'
type RankingRankBy = 'user' | 'api_key'
type RankingMode = 'api_key' | 'tokens' | 'actual_cost'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  items: UserBreakdownItem[]
  totalTokens?: number
  totalActualCost?: number
  loading?: boolean
  error?: boolean
  mode?: RankingMode
}>(), {
  totalTokens: 0,
  totalActualCost: 0,
  loading: false,
  error: false,
  mode: 'tokens',
})

const emit = defineEmits<{
  userClick: [userId: number]
  'mode-change': [mode: RankingMode]
}>()

const modeOptions = computed(() => [
  { value: 'api_key', label: t('admin.usage.rankingModeApiKey') },
  { value: 'tokens', label: t('admin.usage.rankingSortToken') },
  { value: 'actual_cost', label: t('admin.usage.rankingSortCost') },
])

const sortBy = computed<RankingSortBy>(() => (props.mode === 'actual_cost' ? 'actual_cost' : 'tokens'))
const rankBy = computed<RankingRankBy>(() => (props.mode === 'api_key' ? 'api_key' : 'user'))

const entityColumnLabel = computed(() => (
  rankBy.value === 'api_key'
    ? t('usage.apiKeyFilter')
    : t('admin.dashboard.spendingRankingUser')
))

const rankingHint = computed(() => {
  if (rankBy.value === 'api_key') {
    return t('admin.usage.userPeriodRankingHintApiKeyToken')
  }
  return sortBy.value === 'actual_cost'
    ? t('admin.usage.userPeriodRankingHintCost')
    : t('admin.usage.userPeriodRankingHint')
})

const secondaryShareLabel = computed(() => (
  sortBy.value === 'actual_cost'
    ? t('admin.usage.tokenShareShort')
    : t('admin.usage.costShareShort')
))

const fallbackUserLabel = (userId: number): string => t('admin.redeem.userPrefix', { id: userId })

const apiKeyLabel = (item: UserBreakdownItem): string => {
  if (item.api_key_name) return item.api_key_name
  if (item.api_key_id) return t('admin.usage.apiKeyPrefix', { id: item.api_key_id })
  return t('admin.usage.unknownApiKey')
}

const tokenShare = (value: number): number => {
  if (!props.totalTokens || props.totalTokens <= 0) return 0
  return value / props.totalTokens
}

const costShare = (value: number): number => {
  if (!props.totalActualCost || props.totalActualCost <= 0) return 0
  return value / props.totalActualCost
}

const primaryShare = (item: UserBreakdownItem): number => (
  sortBy.value === 'actual_cost' ? costShare(item.actual_cost) : tokenShare(item.total_tokens)
)

const secondaryShare = (item: UserBreakdownItem): number => (
  sortBy.value === 'actual_cost' ? tokenShare(item.total_tokens) : costShare(item.actual_cost)
)

const handleModeChange = (value: string | number | boolean | null) => {
  if (value === 'api_key' || value === 'tokens' || value === 'actual_cost') emit('mode-change', value)
}

const getItemKey = (item: UserBreakdownItem, index: number): string => {
  if (rankBy.value === 'api_key') return `${item.user_id}-${item.api_key_id || 0}-${index}`
  return `${item.user_id}-${index}`
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
