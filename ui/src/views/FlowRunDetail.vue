<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import type { FlowRunDetail } from '../types/api'
import { fetchFlowRun } from '../composables/useApi'
import { useFlowRunEvents } from '../composables/useFlowRunEvents'
import FlowRunHeader from '../components/FlowRunHeader.vue'
import StepTimeline from '../components/StepTimeline.vue'

const props = defineProps<{ namespace: string; name: string }>()

const item = ref<FlowRunDetail | null>(null)
const error = ref<string | null>(null)
const loading = ref(true)

async function load() {
  error.value = null
  try {
    item.value = await fetchFlowRun(props.namespace, props.name)
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'Failed to load FlowRun'
  } finally {
    loading.value = false
  }
}

onMounted(load)

// SSE: re-fetch when an event arrives for the FlowRun we're viewing.
// The namespace prop is stable for the lifetime of this view, so wrap in a
// computed ref to satisfy useFlowRunEvents's Ref<string> signature.
const namespaceRef = computed(() => props.namespace)

useFlowRunEvents(namespaceRef, (summary) => {
  if (summary.name === props.name) {
    // Event is a summary; re-fetch to get full step detail.
    load()
  }
})
</script>

<template>
  <div class="p-6 max-w-4xl mx-auto space-y-6">
    <!-- Loading -->
    <div v-if="loading" class="text-gray-400 text-sm text-center py-16">Loading…</div>

    <!-- Error -->
    <div
      v-else-if="error"
      class="bg-red-50 border border-red-200 rounded-lg px-5 py-4 text-red-700 text-sm"
    >
      <span class="font-semibold">Error:</span> {{ error }}
    </div>

    <!-- Content -->
    <template v-else-if="item">
      <FlowRunHeader :item="item" />

      <section>
        <h2 class="text-sm font-semibold text-gray-600 uppercase tracking-wide mb-3">Steps</h2>
        <div class="bg-white border border-gray-200 rounded-lg px-5 py-2 shadow-sm">
          <StepTimeline :steps="item.steps" />
        </div>
      </section>

      <!-- Trigger data (optional, shown when present) -->
      <section v-if="item.triggerData">
        <h2 class="text-sm font-semibold text-gray-600 uppercase tracking-wide mb-3">
          Trigger Data
        </h2>
        <div class="bg-white border border-gray-200 rounded-lg px-5 py-4 shadow-sm space-y-3">
          <div v-if="item.triggerData.eventType" class="text-sm">
            <span class="text-gray-500">Event type:</span>
            <span class="ml-2 font-mono text-gray-800">{{ item.triggerData.eventType }}</span>
          </div>
          <div v-if="item.triggerData.body">
            <p class="text-xs text-gray-500 mb-1">Body</p>
            <pre
              class="bg-gray-50 border border-gray-100 rounded p-3 text-xs font-mono overflow-x-auto whitespace-pre-wrap break-words"
            >{{ item.triggerData.body }}</pre>
          </div>
          <div v-if="item.triggerData.headers && Object.keys(item.triggerData.headers).length">
            <p class="text-xs text-gray-500 mb-1">Headers</p>
            <dl class="text-xs font-mono space-y-0.5">
              <div
                v-for="(val, key) in item.triggerData.headers"
                :key="key"
                class="flex gap-3"
              >
                <dt class="text-gray-400 w-48 truncate flex-shrink-0">{{ key }}</dt>
                <dd class="text-gray-700 truncate">{{ val }}</dd>
              </div>
            </dl>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>
