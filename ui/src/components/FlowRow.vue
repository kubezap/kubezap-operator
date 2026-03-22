<script setup lang="ts">
import type { FlowSummary } from '../types/api'

defineProps<{ item: FlowSummary }>()

function relativeTime(isoString?: string): string {
  if (!isoString) return '—'
  const diff = Date.now() - new Date(isoString).getTime()
  if (diff < 60000) return 'just now'
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}
</script>

<template>
  <tr class="border-t border-gray-100 hover:bg-gray-50">
    <td class="px-4 py-3 text-sm font-medium text-gray-900 whitespace-nowrap">
      {{ item.name }}
    </td>
    <td class="px-4 py-3 text-sm text-gray-600">
      {{ item.stepCount }}
    </td>
    <td class="px-4 py-3">
      <span
        v-if="item.ready"
        class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800"
      >
        Ready
      </span>
      <span
        v-else
        class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-red-100 text-red-800"
      >
        Not Ready
      </span>
    </td>
    <td class="px-4 py-3 text-sm text-gray-500">
      {{ relativeTime(item.lastUsedTime) }}
    </td>
  </tr>
</template>
