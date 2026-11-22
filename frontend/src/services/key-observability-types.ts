import type { TimeWindowStats } from './api-types'
import type { RoutingCoverage } from './routing-types'

export interface KeyOverview {
  keyId: string
  keyMask: string
  state: 'active' | 'paused' | 'blacklisted' | 'historical' | 'missing'
  stats: TimeWindowStats
  latestFailure?: {
    gatewayRequestId: string
    attemptId: string
    occurredAt: string
    statusCode: number
    errorSummary?: string
  }
}

export interface KeyOverviewResponse {
  channelUid: string
  keys: KeyOverview[]
  failureCoverage: RoutingCoverage
}

export interface KeyOperation {
  sequence: number
  occurredAt: string
  apiType: string
  channelUid: string
  keyId: string
  keyMask: string
  action: string
  source: string
  from: string
  to: string
  outcome: string
  reason?: string
}

export interface KeyOperationsPage {
  events: KeyOperation[]
  nextCursor: number
  coverage: { persistent: boolean; retentionDays: number; startedAt: string; droppedWrites: number; limited: boolean }
}
