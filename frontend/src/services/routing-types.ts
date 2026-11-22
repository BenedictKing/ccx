import type { ChannelKind } from './api-types'
export type RoutingStatus = 'success' | 'failed' | 'cancelled' | 'in_progress' | 'unknown'
export type RoutingBasis = 'successful_requests' | 'all_attempts'
export type StatsDuration = '1h' | '6h' | '24h' | 'today' | '7d' | '30d' | '90d' | '180d' | '365d' | 'thisyear'
export interface RoutingAttempt {
  keyId?: string
  keyMask?: string
  details?: RoutingAttemptDetails
  transport: string
  auxiliary?: boolean
  attemptId: string
  attemptSequence: number
  effectiveModel: string
  mappedModel: string
  sentModel: string
  channelUid: string
  channelName: string
  channelDeleted?: boolean
  serviceType: string
  configRevision: string
  mappingOutcome: string
  mappingSource?: string
  mappingTarget?: string
  matchType?: string
  transformReason?: string
  modelChanged: boolean
  transformSteps?: { kind: string; from: string; to: string }[]
  selectionTraceSummary?: string
  selectionReason?: string
  status: RoutingStatus
  statusCode: number
  startedAt: string
  completedAt?: string
}
export interface RoutingAttemptDetails {
  errorSummary?: string
  usageObserved: boolean
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheCreationTokens: number
  firstByteLatencyMs?: number
  firstContentLatencyMs?: number
  durationMs?: number
  maxStreamIdleMs?: number
  maxToolCallIdleMs?: number
}
export interface RoutingRequest {
  gatewayRequestId: string
  apiType: ChannelKind
  requestedModel: string
  operation?: string
  requestSource?: string
  receivedAt: string
  completedAt?: string
  status: RoutingStatus
  failureStage?: string
  attempts: RoutingAttempt[]
}
export interface RoutingPath {
  requestedModel: string
  channelUid: string
  channelName: string
  channelDeleted?: boolean
  sentModel: string
  count: number
  share: number
  mappingOutcomes: Record<string, number>
  statuses: Record<string, number>
}
export interface RoutingCoverage {
  retentionDays?: number
  startedAt: string
  persistent: boolean
  limited: boolean
  droppedWrites: number
  unknownRequests: number
}
export interface RoutingDistribution {
  from: string
  to: string
  basis: RoutingBasis
  paths: RoutingPath[]
  coverage: RoutingCoverage
  summary: {
    requests: number
    success: number
    failed: number
    cancelled: number
    inProgress: number
    unknown: number
    attempts: number
    retriedRequests: number
    noAttemptRequests: number
    matchedCount: number
  }
}
export interface RoutingRequestPage {
  keyOptions?: { keyId: string; keyMask: string }[]
  requests: RoutingRequest[]
  nextCursor: string
  from: string
  to: string
  coverage: RoutingCoverage
}
export type RoutingHistoryDuration = '15m' | '1h' | '6h' | '24h' | '7d' | '30d'
export interface RoutingHistorySelection {
  apiType: ChannelKind
  channelUid: string
  channelName: string
  duration: RoutingHistoryDuration
}
export interface RoutingQuery {
  keyId?: string
  duration?: string
  timezone?: string
  from?: string
  to?: string
  basis: RoutingBasis
  requestedModel?: string
  channelUid?: string
  sentModel?: string
  status?: RoutingStatus | ''
  q?: string
  cursor?: string
  limit?: number
}
