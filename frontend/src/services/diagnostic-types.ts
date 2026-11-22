export interface PersistenceDiagnostics {
  persistent: boolean
  retentionDays: number
  bufferedWrites: number
  writeFailures: number
  droppedWrites: number
  cleanupFailures: number
  lastWriteAt?: string
  lastCleanupAt?: string
  lastCleanupOK: boolean
  recordCount: number
  oldestRecordAt?: string
  queryFailed: boolean
}

export interface DiagnosticReport {
  generatedAt: string
  version: { version: string; gitCommit: string; buildTime: string }
  persistenceConfigured: boolean
  metrics: PersistenceDiagnostics
  requestHistory: PersistenceDiagnostics
  keyOperations: PersistenceDiagnostics
}
