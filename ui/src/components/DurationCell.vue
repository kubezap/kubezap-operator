<script setup lang="ts">
const props = defineProps<{ seconds?: number }>()

function format(s: number | undefined): string {
  if (s === undefined || s === null) return '—'
  if (s < 1) return '<1s'
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  if (m < 60) return rem > 0 ? `${m}m ${rem}s` : `${m}m`
  const h = Math.floor(m / 60)
  const remM = m % 60
  return remM > 0 ? `${h}h ${remM}m` : `${h}h`
}
</script>

<template>
  <span class="text-gray-600 text-sm tabular-nums">{{ format(props.seconds) }}</span>
</template>
