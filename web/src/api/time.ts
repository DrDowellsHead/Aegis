import type { SeriesPoint } from './types'

export type Step = '1m' | '5m' | '15m' | '1h' | '6h' | '24h'
export type Period = '1h' | '24h' | '7d' | 'custom'
export type Window = { from: string; to: string; step: Step; period: Period }
export type Metric = 'cpu_avg' | 'cpu_min' | 'cpu_max' | 'memory_avg' | 'disk_avg'

export const MAX_RANGE_MS = 90 * 24 * 60 * 60 * 1000
export const MAX_BUCKETS = 500
export const stepMilliseconds: Record<Step, number> = {
  '1m': 60_000, '5m': 300_000, '15m': 900_000,
  '1h': 3_600_000, '6h': 21_600_000, '24h': 86_400_000
}

export function chooseStep(durationMS: number): Step {
  if (!Number.isFinite(durationMS) || durationMS <= 0 || durationMS > MAX_RANGE_MS) {
    throw new RangeError('Период должен быть больше нуля и не длиннее 90 суток.')
  }
  const step = (Object.keys(stepMilliseconds) as Step[])
    .find(candidate => durationMS <= MAX_BUCKETS * stepMilliseconds[candidate])
  if (!step) throw new RangeError('Невозможно подобрать шаг агрегации.')
  return step
}

export function makeWindow(period: Exclude<Period, 'custom'>, now = Date.now()): Window {
  const duration: Record<Exclude<Period, 'custom'>, number> = {
    '1h': 3_600_000, '24h': 86_400_000, '7d': 7 * 86_400_000
  }
  const span = duration[period]
  return {
    period,
    from: new Date(now - span).toISOString(),
    to: new Date(now).toISOString(),
    step: chooseStep(span)
  }
}

// An HTML datetime-local string carries NO offset. To avoid interpreting an
// ambiguous DST transition as the wrong instant, the inputs are explicitly UTC.
export function parseUTCInput(input: string): Date {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(input)) {
    throw new RangeError('Укажите дату и время в формате ГГГГ-ММ-ДД ЧЧ:ММ (UTC).')
  }
  const date = new Date(input + ':00.000Z')
  if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 16) !== input) {
    throw new RangeError('Недопустимая календарная дата или время UTC.')
  }
  return date
}

export function toUTCInput(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) throw new RangeError('Неверное время.')
  return date.toISOString().slice(0, 16)
}

export function makeCustomWindow(fromUTC: string, toUTC: string): Window {
  const from = parseUTCInput(fromUTC)
  const to = parseUTCInput(toUTC)
  const span = to.getTime() - from.getTime()
  return {
    period: 'custom',
    from: from.toISOString(),
    to: to.toISOString(),
    step: chooseStep(span)
  }
}

// Compare equal durations in absolute UTC elapsed time, not "yesterday at
// the same local wall clock", which is ambiguous across DST transitions.
export function previousWindow(current: Window): Window {
  const start = Date.parse(current.from)
  const end = Date.parse(current.to)
  const duration = end - start
  if (!Number.isFinite(start) || !Number.isFinite(end) || duration <= 0) {
    throw new RangeError('Неправильный интервал сравнения.')
  }
  return {
    period: 'custom',
    from: new Date(start - duration).toISOString(),
    to: new Date(start).toISOString(),
    step: current.step
  }
}

export function formatLocal(value: string): string {
  const instant = new Date(value)
  if (!Number.isFinite(instant.getTime())) return 'Неизвестное время'
  return new Intl.DateTimeFormat('ru-RU', {
    year: 'numeric', month: 'short', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', timeZoneName: 'short'
  }).format(instant)
}

export function formattedZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone ?? 'локальный часовой пояс'
}

// Count-weighted, not an unweighted average of per-bucket averages.
// This matters when the number of measurements varies across buckets.
export function weightedAverage(points: SeriesPoint[], metric: 'cpu_avg' | 'memory_avg' | 'disk_avg'): number | null {
  const sampleCount: Record<typeof metric, keyof SeriesPoint> = {
    cpu_avg: 'cpu_samples', memory_avg: 'memory_samples', disk_avg: 'disk_samples'
  }
  let sum = 0
  let count = 0
  for (const point of points) {
    const value = point[metric]
    const samples = point[sampleCount[metric]]
    if (value !== null && typeof samples === 'number' && Number.isFinite(value) &&
        Number.isSafeInteger(samples) && samples > 0) {
      sum += value * samples
      count += samples
    }
  }
  return count ? sum / count : null
}

export function nearestPoint(points: SeriesPoint[], targetMS: number, stepMS: number): SeriesPoint | null {
  if (!Number.isFinite(targetMS)) return null
  let best: SeriesPoint | null = null
  let distance = Infinity
  for (const point of points) {
    const diff = Math.abs(Date.parse(point.at) - targetMS)
    if (diff < distance) { best = point; distance = diff }
  }
  return best && distance <= stepMS * 0.55 ? best : null
}

// Null values and absent buckets break SVG lines; zeros are genuine readings.
export function seriesSegments(points: SeriesPoint[], metric: Metric, stepMS: number): SeriesPoint[][] {
  const segments: SeriesPoint[][] = []
  let current: SeriesPoint[] = []
  let previous = Number.NaN
  for (const point of points) {
    const at = Date.parse(point.at)
    const val = point[metric]
    if (val === null || !Number.isFinite(at) || !Number.isFinite(val)) {
      if (current.length) segments.push(current)
      current = []
      previous = Number.NaN
      continue
    }
    if (current.length && (at <= previous || at - previous > stepMS * 1.5)) {
      segments.push(current)
      current = []
    }
    current.push(point)
    previous = at
  }
  if (current.length) segments.push(current)
  return segments
}
