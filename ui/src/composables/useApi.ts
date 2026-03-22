import type {
  FlowRunDetail,
  FlowRunListResponse,
  FlowSummary,
  NamespaceListResponse,
  TriggerListResponse,
} from '../types/api'

const base = '/api/v1'

export async function fetchNamespaces(): Promise<string[]> {
  const r = await fetch(`${base}/namespaces`)
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  const data: NamespaceListResponse = await r.json()
  return data.namespaces
}

export interface FlowRunListParams {
  phase?: string
  trigger?: string
  flow?: string
  limit?: number
  since?: string
}

export async function fetchFlowRuns(
  namespace: string,
  params: FlowRunListParams = {},
): Promise<FlowRunListResponse> {
  const q = new URLSearchParams()
  if (params.phase) q.set('phase', params.phase)
  if (params.trigger) q.set('trigger', params.trigger)
  if (params.flow) q.set('flow', params.flow)
  if (params.limit) q.set('limit', String(params.limit))
  if (params.since) q.set('since', params.since)
  const r = await fetch(`${base}/${namespace}/flowruns?${q}`)
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  return r.json()
}

export async function fetchFlowRun(namespace: string, name: string): Promise<FlowRunDetail> {
  const r = await fetch(`${base}/${namespace}/flowruns/${name}`)
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  return r.json()
}

export async function fetchTriggers(namespace: string): Promise<TriggerListResponse> {
  const r = await fetch(`${base}/${namespace}/triggers`)
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  return r.json()
}

export async function fetchFlows(namespace: string): Promise<FlowSummary[]> {
  const r = await fetch(`${base}/${namespace}/flows`)
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  const data = await r.json()
  return data.items
}
