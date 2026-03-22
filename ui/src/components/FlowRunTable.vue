<script setup lang="ts">
import type { FlowRunSummary } from '../types/api'
import FlowRunRow from './FlowRunRow.vue'

defineProps<{ items: FlowRunSummary[]; namespace: string }>()

const columns = ['Name', 'Trigger', 'Flow', 'Phase', 'Created', 'Duration']
</script>

<template>
  <div class="overflow-x-auto rounded-lg border border-gray-200">
    <table class="min-w-full divide-y divide-gray-200 text-left">
      <thead class="bg-gray-50">
        <tr>
          <th
            v-for="col in columns"
            :key="col"
            class="px-4 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide"
          >
            {{ col }}
          </th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100 bg-white">
        <template v-if="items.length > 0">
          <FlowRunRow
            v-for="item in items"
            :key="item.name"
            :item="item"
            :namespace="namespace"
          />
        </template>
        <tr v-else>
          <td colspan="6" class="px-4 py-8 text-center text-sm text-gray-400">
            No FlowRuns found.
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
