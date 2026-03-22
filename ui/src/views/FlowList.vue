<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import type { FlowSummary } from '../types/api'
import { fetchFlows } from '../composables/useApi'
import FlowRow from '../components/FlowRow.vue'

const props = defineProps<{ namespace: string }>()

const flows = ref<FlowSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

async function load(ns: string) {
  loading.value = true
  error.value = null
  try {
    flows.value = await fetchFlows(ns)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load flows'
    flows.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => load(props.namespace))
watch(() => props.namespace, load)
</script>

<template>
  <div class="px-6 py-4">
    <h1 class="text-xl font-semibold text-gray-900 mb-4">
      Flows
      <span class="ml-2 text-sm font-normal text-gray-500">{{ namespace }}</span>
    </h1>

    <div v-if="loading" class="text-sm text-gray-400 py-8 text-center">
      Loading...
    </div>

    <div v-else-if="error" class="text-sm text-red-600 py-8 text-center">
      {{ error }}
    </div>

    <div v-else-if="flows.length === 0" class="text-sm text-gray-500 py-8 text-center">
      No flows found.
    </div>

    <div v-else class="overflow-x-auto rounded-lg border border-gray-200">
      <table class="min-w-full bg-white text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 uppercase tracking-wider">
          <tr>
            <th class="px-4 py-3 font-medium">Name</th>
            <th class="px-4 py-3 font-medium">Steps</th>
            <th class="px-4 py-3 font-medium">Ready</th>
            <th class="px-4 py-3 font-medium">Last Used</th>
          </tr>
        </thead>
        <tbody>
          <FlowRow v-for="item in flows" :key="item.name" :item="item" />
        </tbody>
      </table>
    </div>
  </div>
</template>
