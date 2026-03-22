<script setup lang="ts">
import type { StepRunStatus } from '../types/api'
import StepRow from './StepRow.vue'

defineProps<{ steps: StepRunStatus[] }>()
</script>

<template>
  <div>
    <p v-if="steps.length === 0" class="text-sm text-gray-400 italic py-4">No steps recorded.</p>
    <ul
      v-else
      class="relative ml-2 border-l-2 border-gray-200 pl-4 divide-y divide-gray-100"
    >
      <StepRow v-for="step in steps" :key="step.name" :step="step" />
    </ul>
  </div>
</template>
