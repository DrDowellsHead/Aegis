/**
 * JSON DTOs derived from Go storage/metrics and api/openapi/history-v1.yaml.
 * This file describes a contract. Step 4.1 does NOT fetch protected data.
 *
 * Go uint64 byte counters exceed JS's exact integer range at 2^53-1.
 * The gateway must expose strings or safe bounded values where precision matters.
 */
export type ISO8601 = string
export type UUID = string
export type AgentStatus = 'online' | 'offline' | 'unknown'

export interface AgentInfo {
  agent_id: UUID
  status: AgentStatus
  registered_at: ISO8601
  last_contact_at: ISO8601 | null
  last_sample_at: ISO8601 | null
  os?: string
  arch?: string
}

export interface AgentPage {
  items: AgentInfo[]
  has_more: boolean
  next_cursor?: UUID
  offline_after: string
}

export interface CPU {
  used_percent: number
}

export interface Memory {
  total_bytes: number
  available_bytes: number
  used_bytes: number
  used_percent: number
}

export interface Disk {
  path: string
  total_bytes: number
  free_bytes: number
  used_bytes: number
  used_percent: number
}

export interface NetworkInterface {
  name: string
  bytes_sent: number
  bytes_received: number
}

export interface Snapshot {
  schema_version: number
  started_at: ISO8601
  duration_ms: number
  os: string
  arch: string
  cpu: CPU | null
  memory: Memory | null
  disk: Disk | null
  network: NetworkInterface[] | null
  errors?: Record<string, string>
}

export interface Measurement {
  agent_id: UUID
  measurement_id: string
  started_at: ISO8601
  received_at: ISO8601
  snapshot: Snapshot
}

export interface MeasurementPage {
  items: Measurement[]
  has_more: boolean
  next_cursor?: string
}

export interface SeriesPoint {
  at: ISO8601
  samples: number
  cpu_samples: number
  cpu_avg: number | null
  cpu_min: number | null
  cpu_max: number | null
  memory_samples: number
  memory_avg: number | null
  disk_samples: number
  disk_avg: number | null
}

export interface Series {
  agent_id: UUID
  from: ISO8601
  to: ISO8601
  step: string
  disk_path?: string
  points: SeriesPoint[]
}

export interface APIError {
  error: string
}
