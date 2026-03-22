<script setup lang="ts">
defineProps<{ phase: string }>()
</script>

<template>
  <span
    class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
    :class="{
      'bg-gray-100 text-gray-600': phase === 'Pending',
      'bg-blue-100 text-blue-700 animate-pulse': phase === 'Running',
      'bg-green-100 text-green-700': phase === 'Succeeded',
      'bg-red-100 text-red-700': phase === 'Failed',
      'bg-yellow-100 text-yellow-700': phase === 'Cancelled',
    }"
  >
    {{ phase }}
  </span>
</template>
