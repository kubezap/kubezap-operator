<script setup lang="ts">
defineProps<{ phase: string }>()
</script>

<template>
  <span
    class="inline-flex items-center justify-center w-5 h-5 text-sm font-mono font-bold select-none"
    :class="{
      'text-green-600': phase === 'Succeeded',
      'text-red-600': phase === 'Failed',
      'text-blue-500 animate-pulse': phase === 'Running',
      'text-gray-400': phase === 'Pending' || phase === 'Waiting' || phase === 'Skipped',
    }"
  >
    <template v-if="phase === 'Succeeded'">&#10003;</template>
    <template v-else-if="phase === 'Failed'">&#10007;</template>
    <template v-else-if="phase === 'Running'">&#9679;</template>
    <template v-else-if="phase === 'Skipped'">&#8212;</template>
    <template v-else>&#9675;</template>
  </span>
</template>
