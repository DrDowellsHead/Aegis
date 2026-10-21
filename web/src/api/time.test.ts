import { describe, expect, it } from 'vitest'
import type { SeriesPoint } from './types'
import {
  MAX_RANGE_MS, MAX_BUCKETS, chooseStep, makeWindow, makeCustomWindow,
  parseUTCInput, previousWindow, nearestPoint, seriesSegments,
  stepMilliseconds, weightedAverage
} from './time'

function point(at: string, cpu: number | null, samples = 1): SeriesPoint {
  return {
    at, samples, cpu_samples: cpu === null ? 0 : samples,
    cpu_avg: cpu, cpu_min: cpu, cpu_max: cpu,
    memory_samples: 0, memory_avg: null,
    disk_samples: 0, disk_avg: null
  }
}

describe('fixed and custom UTC windows', () => {
  it('chooses an allowed step without exceeding the API limit', () => {
    for (const duration of [60_000, 3_600_000, 86_400_000, 7 * 86_400_000, MAX_RANGE_MS]) {
      const step = chooseStep(duration)
      expect(duration).toBeLessThanOrEqual(stepMilliseconds[step] * MAX_BUCKETS)
    }
    expect(chooseStep(3_600_000)).toBe('1m')
    expect(chooseStep(86_400_000)).toBe('5m')
    expect(chooseStep(7 * 86_400_000)).toBe('1h')
    expect(() => chooseStep(0)).toThrow()
    expect(() => chooseStep(MAX_RANGE_MS + 1)).toThrow()
  })

  it('rejects invalid calendar values and out-of-order custom windows', () => {
    expect(() => parseUTCInput('2026-02-30T10:00')).toThrow()
    expect(() => parseUTCInput('2026-01-01T24:00')).toThrow()
    expect(() => parseUTCInput('2026-01-01T10:00+03:00')).toThrow()
    expect(() => makeCustomWindow('2026-01-02T10:00', '2026-01-01T10:00')).toThrow()
    expect(() => makeCustomWindow('2026-01-01T10:00', '2026-01-01T10:00')).toThrow()
    expect(() => makeCustomWindow('2026-01-01T00:00', '2026-04-02T00:00')).toThrow()
  })

  it('uses exact UTC instants regardless of DST and aligns equal durations', () => {
    // Europe/Berlin changes offset around this instant, but UTC remains unique.
    const current = makeCustomWindow('2026-03-29T00:00', '2026-03-29T03:00')
    const older = previousWindow(current)
    expect(current.from).toBe('2026-03-29T00:00:00.000Z')
    expect(current.to).toBe('2026-03-29T03:00:00.000Z')
    expect(older.from).toBe('2026-03-28T21:00:00.000Z')
    expect(older.to).toBe(current.from)
    expect(current.step).toBe(older.step)
    expect(Date.parse(current.to) - Date.parse(current.from))
      .toBe(Date.parse(older.to) - Date.parse(older.from))
  })

  it('freezes preset windows to supplied clock time', () => {
    const reference = Date.parse('2026-10-21T12:00:00Z')
    const w = makeWindow('24h', reference)
    expect(w.from).toBe('2026-10-20T12:00:00.000Z')
    expect(w.to).toBe('2026-10-21T12:00:00.000Z')
    expect(w.step).toBe('5m')
  })
})

describe('time series and comparison semantics', () => {
  it('keeps null gaps separate and treats actual zero as a reading', () => {
    const points = [
      point('2026-01-01T10:00:00Z', 0),
      point('2026-01-01T10:01:00Z', 40),
      point('2026-01-01T10:02:00Z', null),
      point('2026-01-01T10:03:00Z', 60),
      point('2026-01-01T10:07:00Z', 90)
    ]
    const segments = seriesSegments(points, 'cpu_avg', 60_000)
    expect(segments.map(s => s.length)).toEqual([2, 1, 1])
    expect(segments[0]?.[0]?.cpu_avg).toBe(0)
  })

  it('weights averages by real per-metric sample counts', () => {
    const rows = [
      point('2026-01-01T10:00:00Z', 10, 1),
      point('2026-01-01T10:01:00Z', 90, 9),
      point('2026-01-01T10:02:00Z', null, 8)
    ]
    expect(weightedAverage(rows, 'cpu_avg')).toBe(82)
    expect(weightedAverage(rows, 'disk_avg')).toBeNull()
  })

  it('does not inspect a bucket that is far from the mouse', () => {
    const rows = [point('2026-01-01T10:00:00Z', 20)]
    expect(nearestPoint(rows, Date.parse('2026-01-01T10:00:20Z'), 60_000)).not.toBeNull()
    expect(nearestPoint(rows, Date.parse('2026-01-01T10:05:00Z'), 60_000)).toBeNull()
  })
})
