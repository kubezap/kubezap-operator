export interface FlowRunSummary {
  name: string
  namespace: string
  triggerName: string
  flowName: string
  phase: 'Pending' | 'Running' | 'Succeeded' | 'Failed' | 'Cancelled'
  creationTimestamp: string
  startTime?: string
  completionTime?: string
  durationSeconds?: number
  stepCount: number
  stepsSucceeded: number
  stepsFailed: number
}

export interface StepRunStatus {
  name: string
  phase: 'Pending' | 'Running' | 'Succeeded' | 'Failed' | 'Skipped' | 'Waiting'
  attempts: number
  message?: string
  startTime?: string
  completionTime?: string
  durationSeconds?: number
  results?: Record<string, string>
}

export interface FlowRunDetail extends FlowRunSummary {
  steps: StepRunStatus[]
  triggerData?: {
    eventType?: string
    body?: string
    headers?: Record<string, string>
  }
}

export interface TriggerSummary {
  name: string
  namespace: string
  type: 'webhook' | 'cron' | 'kafka' | 'amqp' | 'nats' | 'resource'
  ready: boolean
  lastFiredTime?: string
  activeFlowRuns: number
  schedule?: string
  endpoint?: string
}

export interface FlowSummary {
  name: string
  namespace: string
  stepCount: number
  ready: boolean
  lastUsedTime?: string
}

export interface NamespaceListResponse {
  namespaces: string[]
}

export interface FlowRunListResponse {
  items: FlowRunSummary[]
  total: number
}

export interface TriggerListResponse {
  items: TriggerSummary[]
}

export interface FlowListResponse {
  items: FlowSummary[]
}
