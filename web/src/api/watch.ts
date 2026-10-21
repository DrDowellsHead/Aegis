import { requestJSON } from './request'

export interface WatchStatus {
  state: 'never' | 'running' | 'ok' | 'partial_failure' | 'failed'
  started_at: string | null
  finished_at: string | null
  last_success_at: string | null
  progress_at: string | null
  agents_checked: number
  rules_checked: number
  errors: number
  interval_seconds: number
  offline_after_seconds: number
  stale: boolean
}

export async function getWatchStatus(signal?: AbortSignal): Promise<WatchStatus> {
  const raw: unknown = await requestJSON('/api/web/watch/status', { signal })
  if (!raw || Array.isArray(raw) || typeof raw !== 'object') {
    throw new Error('Invalid watch status')
  }
  const obj = raw as Record<string, unknown>
  if (!['never', 'running', 'ok', 'partial_failure', 'failed'].includes(String(obj.state)) ||
    typeof obj.stale !== 'boolean' ||
    !['agents_checked', 'rules_checked', 'errors', 'interval_seconds', 'offline_after_seconds']
      .every(key => typeof obj[key] === 'number' && Number.isSafeInteger(obj[key]) && (obj[key] as number) >= 0) ||
    !['started_at', 'finished_at', 'last_success_at', 'progress_at']
      .every(key => obj[key] === null || typeof obj[key] === 'string')) {
    throw new Error('Malformed watch status')
  }
  return obj as unknown as WatchStatus
}
