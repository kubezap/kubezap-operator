<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { fetchNamespaces } from '../composables/useApi'

const router = useRouter()
const route = useRoute()

const namespaces = ref<string[]>([])
const currentNamespace = ref(localStorage.getItem('kubezap-namespace') || 'default')

onMounted(async () => {
  try {
    namespaces.value = await fetchNamespaces()
    if (!namespaces.value.includes(currentNamespace.value) && namespaces.value.length > 0) {
      currentNamespace.value = namespaces.value[0]
    }
  } catch {
    namespaces.value = [currentNamespace.value]
  }
})

watch(currentNamespace, ns => {
  localStorage.setItem('kubezap-namespace', ns)
  // Re-navigate to the same section with the new namespace
  const pathParts = route.path.split('/')
  if (pathParts[1] === 'runs') router.push(`/runs/${ns}`)
  else if (pathParts[1] === 'triggers') router.push(`/triggers/${ns}`)
  else if (pathParts[1] === 'flows') router.push(`/flows/${ns}`)
})

const navLinks = [
  { label: 'FlowRuns', pathPrefix: '/runs' },
  { label: 'Triggers', pathPrefix: '/triggers' },
  { label: 'Flows', pathPrefix: '/flows' },
]
</script>

<template>
  <nav class="bg-white border-b border-gray-200 px-4 py-3 flex items-center gap-6">
    <span class="font-semibold text-gray-900 text-lg">&#9889; KubeZap</span>
    <div class="flex gap-4">
      <RouterLink
        v-for="link in navLinks"
        :key="link.pathPrefix"
        :to="`${link.pathPrefix}/${currentNamespace}`"
        class="text-sm text-gray-600 hover:text-gray-900"
        :class="{ 'font-medium text-indigo-600': route.path.startsWith(link.pathPrefix) }"
      >
        {{ link.label }}
      </RouterLink>
    </div>
    <div class="ml-auto flex items-center gap-2">
      <label class="text-xs text-gray-500">Namespace</label>
      <select
        v-model="currentNamespace"
        class="text-sm border border-gray-300 rounded px-2 py-1"
      >
        <option v-for="ns in namespaces" :key="ns" :value="ns">{{ ns }}</option>
      </select>
    </div>
  </nav>
</template>
