<script setup lang="ts">
import type { FlowRunSummary } from '../types/api'
import PhaseChip from './PhaseChip.vue'
import RelativeTime from './RelativeTime.vue'
import DurationCell from './DurationCell.vue'

defineProps<{ item: FlowRunSummary; namespace: string }>()
</script>

<template>
  <tr class="hover:bg-gray-50 transition-colors">
    <td class="px-4 py-2 text-sm font-mono">
      <RouterLink
        :to="`/runs/${namespace}/${item.name}`"
        class="text-indigo-600 hover:text-indigo-800 hover:underline"
      >
        {{ item.name }}
      </RouterLink>
    </td>
    <td class="px-4 py-2 text-sm text-gray-700">{{ item.triggerName }}</td>
    <td class="px-4 py-2 text-sm text-gray-700">{{ item.flowName }}</td>
    <td class="px-4 py-2">
      <PhaseChip :phase="item.phase" />
    </td>
    <td class="px-4 py-2">
      <RelativeTime :time="item.creationTimestamp" />
    </td>
    <td class="px-4 py-2">
      <DurationCell :seconds="item.durationSeconds" />
    </td>
  </tr>
</template>
