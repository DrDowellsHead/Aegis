import type { MeasurementPage, Measurement, Series, SeriesPoint } from './types'
import { ApiError } from './client'

export type Period = '1h' | '24h' | '7d'
export type Window = { from: string; to: string; step: '1m' | '5m' | '1h'; period: Period }

export function makeWindow(period: Period): Window {
  const now = Date.now()
  const specs = {
    '1h': { duration: 3600000, step: '1m' },
    '24h': { duration: 86400000, step: '5m' },
    '7d': { duration: 7 * 86400000, step: '1h' }
  } as const
  return {
    period,
    from: new Date(now - specs[period].duration).toISOString(),
    to: new Date(now).toISOString(),
    step: specs[period].step
  }
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw Error('Invalid JSON object')
  return value as Record<string, unknown>
}
function str(value: unknown): string {
  if (typeof value !== 'string') throw Error('Invalid string in API response')
  return value
}
function num(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw Error('Invalid numeric value')
  return value
}
function natural(value: unknown): number {
  const n = num(value)
  if (!Number.isSafeInteger(n) || n < 0) throw Error('Invalid count')
  return n
}
function optionalNum(value: unknown): number | null {
  return value === null ? null : num(value)
}
function nullablePart<T>(value: unknown, parse: (v: unknown) => T): T | null {
  return value === null ? null : parse(value)
}
async function get(path: string, signal: AbortSignal): Promise<unknown> {
  const response = await fetch(path, { signal, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) throw new ApiError(response.status, 'History API unavailable')
  return await response.json() as unknown
}

export function checkUUID(id: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)
}

export function parseSeries(value: unknown): Series {
  const data = object(value)
  if (!Array.isArray(data.points) || data.points.length > 501) throw Error('Bad time series')
  const points: SeriesPoint[] = data.points.map(raw => {
    const p = object(raw)
    return {
      at: str(p.at),
      samples: natural(p.samples),
      cpu_samples: natural(p.cpu_samples),
      cpu_avg: optionalNum(p.cpu_avg),
      cpu_min: optionalNum(p.cpu_min),
      cpu_max: optionalNum(p.cpu_max),
      memory_samples: natural(p.memory_samples),
      memory_avg: optionalNum(p.memory_avg),
      disk_samples: natural(p.disk_samples),
      disk_avg: optionalNum(p.disk_avg)
    }
  })
  const result: Series = {
    agent_id: str(data.agent_id),
    from: str(data.from),
    to: str(data.to),
    step: str(data.step),
    points
  }
  if (data.disk_path !== undefined) result.disk_path = str(data.disk_path)
  return result
}

function measurement(raw: unknown): Measurement {
  const m = object(raw)
  const s = object(m.snapshot)
  const cpu = nullablePart(s.cpu, v => {
    const r = object(v)
    return { used_percent: num(r.used_percent) }
  })
  const memory = nullablePart(s.memory, v => {
    const r = object(v)
    return {
      // uint64 in JSON can exceed JavaScript's maximum safe integer.
      // Byte totals are not displayed until the gateway provides exact strings.
      total_bytes: num(r.total_bytes), available_bytes: num(r.available_bytes),
      used_bytes: num(r.used_bytes), used_percent: num(r.used_percent)
    }
  })
  const disk = nullablePart(s.disk, v => {
    const r = object(v)
    return {
      path: str(r.path), total_bytes: num(r.total_bytes), free_bytes: num(r.free_bytes),
      used_bytes: num(r.used_bytes), used_percent: num(r.used_percent)
    }
  })
  const errors: Record<string, string> = {}
  if (s.errors !== undefined && s.errors !== null) {
    const fields = object(s.errors)
    for (const [key, val] of Object.entries(fields)) errors[key] = str(val)
  }
  return {
    agent_id: str(m.agent_id),
    measurement_id: str(m.measurement_id),
    started_at: str(m.started_at),
    received_at: str(m.received_at),
    snapshot: {
      schema_version: natural(s.schema_version),
      started_at: str(s.started_at),
      duration_ms: natural(s.duration_ms),
      os: str(s.os),
      arch: str(s.arch),
      cpu, memory, disk,
      // This view does not convert counters to network rates.
      network: null,
      errors
    }
  }
}

export function parseHistory(value: unknown): MeasurementPage {
  const r = object(value)
  if (!Array.isArray(r.items) || r.items.length > 100 || typeof r.has_more !== 'boolean') {
    throw Error('Invalid measurements page')
  }
  const result: MeasurementPage = {
    items: r.items.map(measurement),
    has_more: r.has_more
  }
  if (r.next_cursor !== undefined) result.next_cursor = str(r.next_cursor)
  return result
}

export async function fetchSeries(id: string, window: Window, diskPath: string, signal: AbortSignal): Promise<Series> {
  if (!checkUUID(id)) throw Error('Invalid agent ID')
  const params = new URLSearchParams({
    agent_id: id, from: window.from, to: window.to, step: window.step
  })
  if (diskPath) params.set('disk_path', diskPath)
  const series = parseSeries(await get('/api/web/series?' + params, signal))
  if (series.agent_id !== id) throw Error('Unexpected agent in series response')
  return series
}

export async function fetchHistory(id: string, window: Window, cursor: string, signal: AbortSignal): Promise<MeasurementPage> {
  if (!checkUUID(id)) throw Error('Invalid agent ID')
  const params = new URLSearchParams({
    agent_id: id, from: window.from, to: window.to, limit: '20'
  })
  if (cursor) params.set('cursor', cursor)
  const page = parseHistory(await get('/api/web/measurements?' + params, signal))
  if (page.items.some(item => item.agent_id !== id)) throw Error('Unexpected agent in history response')
  return page
}

// Sparse readings stay sparse: a missing metric and an absent time bucket
// must NEVER become 0% or be bridged by a continuous chart segment.
export function seriesSegments(points: SeriesPoint[], metric: keyof Pick<SeriesPoint,
  'cpu_avg' | 'cpu_min' | 'cpu_max' | 'memory_avg' | 'disk_avg'>,
  stepMS: number): SeriesPoint[][] {
  const segments: SeriesPoint[][] = []
  let current: SeriesPoint[] = []
  let previous = Number.NaN
  for (const point of points) {
    const at = Date.parse(point.at)
    const val = point[metric]
    if (val === null || !Number.isFinite(at)) {
      if (current.length) segments.push(current)
      current = []
      previous = Number.NaN
      continue
    }
    if (current.length && at - previous > stepMS * 1.5) {
      segments.push(current)
      current = []
    }
    current.push(point)
    previous = at
  }
  if (current.length) segments.push(current)
  return segments
}
