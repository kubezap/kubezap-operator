<script setup lang="ts">
import { ref, computed } from 'vue'
import type { StepRunStatus } from '../types/api'
import StepBadge from './StepBadge.vue'

const props = defineProps<{ step: StepRunStatus }>()

const expanded = ref(false)

const truncatedMessage = computed(() => {
  const msg = props.step.message
  if (!msg) return ''
  return msg.length > 80 ? msg.slice(0, 80) + '…' : msg
})

const needsExpand = computed(() => (props.step.message?.length ?? 0) > 80)

const duration = computed(() => {
  const d = props.step.durationSeconds
  if (d == null) return ''
  if (d < 60) return `${d.toFixed(1)}s`
  const m = Math.floor(d / 60)
  const s = Math.round(d % 60)
  return `${m}m${s}s`
})
</script>

<template>
  <li class="flex items-start gap-3 py-3">
    <StepBadge :phase="step.phase" class="mt-0.5 flex-shrink-0" />

    <div class="flex-1 min-w-0">
      <div class="flex items-center gap-3 flex-wrap">
        <span class="font-medium text-gray-900 text-sm truncate">{{ step.name }}</span>
        <span
          class="text-xs px-1.5 py-0.5 rounded font-medium"
          :class="{
            'bg-green-100 text-green-700': step.phase === 'Succeeded',
            'bg-red-100 text-red-700': step.phase === 'Failed',
            'bg-blue-100 text-blue-700': step.phase === 'Running',
            'bg-gray-100 text-gray-500': step.phase === 'Pending' || step.phase === 'Waiting' || step.phase === 'Skipped',
          }"
        >
          {{ step.phase }}
        </span>
        <span v-if="duration" class="text-xs text-gray-400 font-mono">{{ duration }}</span>
        <span v-if="step.attempts > 1" class="text-xs text-orange-500">{{ step.attempts }} attempts</span>
      </div>

      <p v-if="step.message" class="mt-1 text-xs text-gray-500 leading-relaxed">
        {{ expanded ? step.message : truncatedMessage }}
        <button
          v-if="needsExpand"
          class="ml-1 text-indigo-500 hover:text-indigo-700 underline"
          @click="expanded = !expanded"
        >
          {{ expanded ? 'less' : 'more' }}
        </button>
      </p>
    </div>
  </li>
</template>
