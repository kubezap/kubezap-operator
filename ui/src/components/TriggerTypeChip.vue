<script setup lang="ts">
const props = defineProps<{ type: string }>()

const colourMap: Record<string, string> = {
  webhook: 'bg-indigo-100 text-indigo-800',
  cron: 'bg-purple-100 text-purple-800',
  kafka: 'bg-orange-100 text-orange-800',
  amqp: 'bg-yellow-100 text-yellow-800',
  nats: 'bg-teal-100 text-teal-800',
  resource: 'bg-gray-100 text-gray-700',
}

const classes = colourMap[props.type] ?? 'bg-gray-100 text-gray-700'
</script>

<template>
  <span
    :class="['inline-flex items-center px-2 py-0.5 rounded text-xs font-medium', classes]"
  >
    {{ type }}
  </span>
</template>
