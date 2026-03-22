import { onUnmounted, watch, type Ref } from 'vue'
import type { FlowRunSummary } from '../types/api'

export function useFlowRunEvents(
  namespace: Ref<string>,
  onEvent: (fr: FlowRunSummary) => void,
) {
  let es: EventSource | null = null

  function connect(ns: string) {
    es?.close()
    es = new EventSource(`/api/v1/events?namespace=${encodeURIComponent(ns)}`)
    es.addEventListener('flowrun', (e: MessageEvent) => {
      try {
        onEvent(JSON.parse(e.data) as FlowRunSummary)
      } catch {
        // ignore malformed events
      }
    })
  }

  watch(namespace, ns => connect(ns), { immediate: true })
  onUnmounted(() => es?.close())
}
