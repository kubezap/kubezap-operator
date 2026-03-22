<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { FlowRunDetail } from '../types/api'

const props = defineProps<{ item: FlowRunDetail }>()

const phaseClasses = computed(() => {
  switch (props.item.phase) {
    case 'Succeeded':
      return 'bg-green-100 text-green-700'
    case 'Failed':
      return 'bg-red-100 text-red-700'
    case 'Running':
      return 'bg-blue-100 text-blue-700'
    case 'Cancelled':
      return 'bg-orange-100 text-orange-700'
    case 'Pending':
    default:
      return 'bg-gray-100 text-gray-500'
  }
})

const duration = computed(() => {
  const d = props.item.durationSeconds
  if (d == null) return null
  if (d < 60) return `${d.toFixed(1)}s`
  const m = Math.floor(d / 60)
  const s = Math.round(d % 60)
  return `${m}m${s}s`
})

const startedAt = computed(() => {
  const t = props.item.startTime ?? props.item.creationTimestamp
  return new Date(t).toLocaleString()
})
</script>

<template>
  <div class="bg-white border border-gray-200 rounded-lg px-5 py-4 shadow-sm">
    <!-- Line 1: name + phase badge -->
    <div class="flex items-center gap-3 flex-wrap">
      <h1 class="font-bold text-gray-900 text-lg font-mono">{{ item.name }}</h1>
      <span
        class="text-xs font-medium px-2 py-0.5 rounded-full"
        :class="phaseClasses"
      >
        {{ item.phase }}
      </span>
    </div>

    <!-- Line 2: breadcrumb + timing -->
    <div class="mt-1.5 flex items-center gap-2 flex-wrap text-sm text-gray-500">
      <span>trigger</span>
      <RouterLink
        :to="`/triggers/${item.namespace}`"
        class="text-indigo-600 hover:text-indigo-800 font-medium"
      >
        {{ item.triggerName }}
      </RouterLink>
      <span class="text-gray-300">&#8594;</span>
      <span>flow</span>
      <RouterLink
        :to="`/flows/${item.namespace}`"
        class="text-indigo-600 hover:text-indigo-800 font-medium"
      >
        {{ item.flowName }}
      </RouterLink>

      <span class="text-gray-300 mx-1">|</span>

      <span>{{ startedAt }}</span>

      <template v-if="duration">
        <span class="text-gray-300 mx-1">|</span>
        <span class="font-mono text-gray-600">{{ duration }}</span>
      </template>

      <template v-if="item.stepCount > 0">
        <span class="text-gray-300 mx-1">|</span>
        <span>
          {{ item.stepsSucceeded }}/{{ item.stepCount }} steps
          <template v-if="item.stepsFailed > 0">
            <span class="text-red-500">({{ item.stepsFailed }} failed)</span>
          </template>
        </span>
      </template>
    </div>
  </div>
</template>
