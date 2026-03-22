<script setup lang="ts">
import { ref, computed, watch, toRef } from 'vue'
import type { FlowRunSummary } from '../types/api'
import { fetchFlowRuns } from '../composables/useApi'
import { useFlowRunEvents } from '../composables/useFlowRunEvents'
import FilterBar from '../components/FilterBar.vue'
import FlowRunTable from '../components/FlowRunTable.vue'

const props = defineProps<{ namespace: string }>()

// Filters
const phase = ref('')
const trigger = ref('')
const flow = ref('')
const since = ref('1h')

// Data store: keyed by name for SSE merge
const runsMap = ref(new Map<string, FlowRunSummary>())
const loading = ref(false)
const error = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    const resp = await fetchFlowRuns(props.namespace, {
      phase: phase.value || undefined,
      trigger: trigger.value || undefined,
      flow: flow.value || undefined,
      since: since.value,
    })
    const next = new Map<string, FlowRunSummary>()
    for (const item of resp.items) {
      next.set(item.name, item)
    }
    runsMap.value = next
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

// SSE: merge individual updates into the map without a full reload
const namespaceRef = toRef(props, 'namespace')
useFlowRunEvents(namespaceRef, (fr: FlowRunSummary) => {
  // Only merge if it matches current filters to avoid polluting the view
  if (phase.value && fr.phase !== phase.value) return
  if (trigger.value && !fr.triggerName.includes(trigger.value)) return
  if (flow.value && !fr.flowName.includes(flow.value)) return
  runsMap.value.set(fr.name, fr)
  // Force reactivity: replace the map reference
  runsMap.value = new Map(runsMap.value)
})

// Sorted items: newest first
const items = computed<FlowRunSummary[]>(() => {
  return [...runsMap.value.values()].sort(
    (a, b) => new Date(b.creationTimestamp).getTime() - new Date(a.creationTimestamp).getTime(),
  )
})

// Re-fetch on namespace change or filter changes
watch(
  () => props.namespace,
  () => load(),
  { immediate: true },
)

watch([phase, trigger, flow, since], () => load())
</script>

<template>
  <div class="px-6 py-4">
    <div class="flex items-center justify-between mb-2">
      <h1 class="text-lg font-semibold text-gray-900">FlowRuns</h1>
      <span v-if="!loading" class="text-xs text-gray-400">{{ items.length }} items</span>
    </div>

    <FilterBar
      v-model:phase="phase"
      v-model:trigger="trigger"
      v-model:flow="flow"
      v-model:since="since"
    />

    <div v-if="loading" class="flex items-center justify-center py-16 text-sm text-gray-400">
      Loading…
    </div>

    <div
      v-else-if="error"
      class="rounded-md bg-red-50 border border-red-200 px-4 py-3 text-sm text-red-700"
    >
      Failed to load FlowRuns: {{ error }}
    </div>

    <FlowRunTable v-else :items="items" :namespace="namespace" />
  </div>
</template>
