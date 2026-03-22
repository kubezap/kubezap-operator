<script setup lang="ts">
defineProps<{
  phase: string
  trigger: string
  flow: string
  since: string
}>()

const emit = defineEmits<{
  (e: 'update:phase', value: string): void
  (e: 'update:trigger', value: string): void
  (e: 'update:flow', value: string): void
  (e: 'update:since', value: string): void
}>()

const phases = ['', 'Pending', 'Running', 'Succeeded', 'Failed', 'Cancelled']
const sinceOptions = [
  { value: '30m', label: '30 min' },
  { value: '1h', label: '1 hour' },
  { value: '6h', label: '6 hours' },
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
]
</script>

<template>
  <div class="flex flex-wrap items-center gap-3 py-3">
    <div class="flex items-center gap-1.5">
      <label class="text-xs text-gray-500 whitespace-nowrap">Phase</label>
      <select
        :value="phase"
        class="text-sm border border-gray-300 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-indigo-400"
        @change="emit('update:phase', ($event.target as HTMLSelectElement).value)"
      >
        <option value="">All</option>
        <option v-for="p in phases.slice(1)" :key="p" :value="p">{{ p }}</option>
      </select>
    </div>

    <div class="flex items-center gap-1.5">
      <label class="text-xs text-gray-500 whitespace-nowrap">Trigger</label>
      <input
        :value="trigger"
        type="text"
        placeholder="filter…"
        class="text-sm border border-gray-300 rounded px-2 py-1 w-36 focus:outline-none focus:ring-1 focus:ring-indigo-400"
        @input="emit('update:trigger', ($event.target as HTMLInputElement).value)"
      />
    </div>

    <div class="flex items-center gap-1.5">
      <label class="text-xs text-gray-500 whitespace-nowrap">Flow</label>
      <input
        :value="flow"
        type="text"
        placeholder="filter…"
        class="text-sm border border-gray-300 rounded px-2 py-1 w-36 focus:outline-none focus:ring-1 focus:ring-indigo-400"
        @input="emit('update:flow', ($event.target as HTMLInputElement).value)"
      />
    </div>

    <div class="flex items-center gap-1.5">
      <label class="text-xs text-gray-500 whitespace-nowrap">Since</label>
      <select
        :value="since"
        class="text-sm border border-gray-300 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-indigo-400"
        @change="emit('update:since', ($event.target as HTMLSelectElement).value)"
      >
        <option v-for="opt in sinceOptions" :key="opt.value" :value="opt.value">
          {{ opt.label }}
        </option>
      </select>
    </div>
  </div>
</template>
