import { useState, type PointerEvent } from 'react'
import type { SeriesPoint } from './api/types'
import {
  formatLocal, nearestPoint, seriesSegments, stepMilliseconds,
  weightedAverage, type Metric, type Window
} from './api/time'

type Line = { metric: Metric; label: string; color: string }

const WIDTH = 720
const HEIGHT = 220
const PAD = 37

function asPercent(value: number | null): string {
  return value === null ? '—' : value.toFixed(1) + ' %'
}

export default function Trend({ title, note, current, previous, window, lines }: {
  title: string
  note: string
  current: SeriesPoint[]
  previous: SeriesPoint[] | null
  window: Window
  lines: Line[]
}) {
  const [hoverAt, setHoverAt] = useState<number | null>(null)
  const [chosenAt, setChosenAt] = useState('')
  const start = Date.parse(window.from)
  const end = Date.parse(window.to)
  const duration = end - start
  const stepMS = stepMilliseconds[window.step]
  const x = (at: number) => PAD + (at - start) / duration * (WIDTH - 2 * PAD)
  const y = (value: number) => HEIGHT - PAD - Math.max(0, Math.min(100, value)) / 100 * (HEIGHT - 2 * PAD)
  const displayCurrent = current.some(p => lines.some(line => p[line.metric] !== null))
  const displayPrevious = previous?.some(p => p[lines[0]!.metric] !== null) ?? false
  const available = displayCurrent || displayPrevious
  const currentAvg = weightedAverage(current, lines[0]!.metric as 'cpu_avg' | 'memory_avg' | 'disk_avg')
  const previousAvg = previous ? weightedAverage(previous, lines[0]!.metric as 'cpu_avg' | 'memory_avg' | 'disk_avg') : null
  const difference = currentAvg !== null && previousAvg !== null ? currentAvg - previousAvg : null

  function onMove(event: PointerEvent<SVGSVGElement>) {
    const rect = event.currentTarget.getBoundingClientRect()
    if (!rect.width) return
    const coordinate = (event.clientX - rect.left) / rect.width * WIDTH
    const time = start + (coordinate - PAD) / (WIDTH - PAD * 2) * duration
    if (coordinate < PAD || coordinate > WIDTH - PAD) {
      setHoverAt(null)
      return
    }
    const match = nearestPoint(current, time, stepMS)
    const old = previous ? nearestPoint(previous, time - duration, stepMS) : null
    setHoverAt(match ? Date.parse(match.at) : old ? Date.parse(old.at) + duration : null)
  }

  const target = hoverAt ?? (chosenAt ? Date.parse(chosenAt) : null)
  const active = target !== null ? nearestPoint(current, target, stepMS) : null
  const oldActive = target !== null && previous
    ? nearestPoint(previous, target - duration, stepMS) : null
  const indicator = active ? Date.parse(active.at) : oldActive ? Date.parse(oldActive.at) + duration : null

  // Alignment uses elapsed UTC duration, not local wall-clock hours.
  function renderLines(points: SeriesPoint[], metrics: Line[], timeOffset: number, reference: boolean) {
    return metrics.flatMap(line =>
      seriesSegments(points, line.metric, stepMS).map((segment, idx) => {
        const coordinates = segment.map(point =>
          `${x(Date.parse(point.at) + timeOffset).toFixed(2)},${y(point[line.metric]!).toFixed(2)}`
        )
        const key = `${reference ? 'prev' : 'now'}-${line.metric}-${idx}`
        if (segment.length === 1) {
          return <circle key={key} cx={x(Date.parse(segment[0]!.at) + timeOffset)}
            cy={y(segment[0]![line.metric]!)} r={reference ? 2.2 : 3.3}
            fill={line.color} opacity={reference ? .62 : 1}/>
        }
        return <polyline key={key} points={coordinates.join(' ')} fill="none"
          stroke={line.color} opacity={reference ? .55 : 1}
          strokeDasharray={reference ? '6 5' : undefined}
          strokeWidth={reference ? 2 : 2.4} strokeLinejoin="round"
          strokeLinecap="round" />
      })
    )
  }
  const options = Array.from(new Set([
    ...current.map(p => p.at),
    ...(previous ?? []).map(p => new Date(Date.parse(p.at) + duration).toISOString())
  ])).sort()

  return <section className="panel chart-panel" aria-label={title}>
    <div className="panel-heading">
      <h2>{title}</h2>
      <span className="muted small">{note}</span>
    </div>
    {!available ? <p className="chart-empty">За выбранный период показаний нет.
      Отсутствующее значение не равно нулю.</p> : <>
      <svg className="trend-svg" viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        role="img" aria-label={`График ${title} — проценты по времени`}
        onPointerMove={onMove} onPointerLeave={() => setHoverAt(null)}>
        {[0, 25, 50, 75, 100].map(tick => <g key={tick}>
          <line x1={PAD} x2={WIDTH - PAD} y1={y(tick)} y2={y(tick)}
            stroke="#34465b" strokeWidth="1"/>
          <text x={PAD - 10} y={y(tick) + 4} textAnchor="end"
            fontSize="11" fill="#93a8bd">{tick}</text>
        </g>)}
        {previous && renderLines(previous, [lines[0]!], duration, true)}
        {renderLines(current, lines, 0, false)}
        {indicator !== null && <line x1={x(indicator)} x2={x(indicator)}
          y1={PAD} y2={HEIGHT - PAD} stroke="#f5faff" strokeDasharray="3 4" opacity=".8"/>}
        <text x={PAD} y={HEIGHT - 7} fontSize="11" fill="#9aafc3">{formatLocal(window.from)}</text>
        <text x={WIDTH - PAD} y={HEIGHT - 7} textAnchor="end" fontSize="11" fill="#9aafc3">
          {formatLocal(window.to)}
        </text>
      </svg>
      <div className="chart-legend">
        {lines.map(line => <span key={line.metric}>
          <i style={{ background: line.color }}/>{line.label}</span>)}
        {previous && <span>┄ Предыдущий период (среднее)</span>}
      </div>
      {previous && <p className="chart-comparison">
        Среднее текущего периода: <strong>{asPercent(currentAvg)}</strong>
        {' · '}предыдущего: <strong>{asPercent(previousAvg)}</strong>
        {' · '}разница: <strong>{difference === null ? '—' :
          `${difference > 0 ? '+' : ''}${difference.toFixed(1)} п.п.`}</strong>
      </p>}
      <div className="chart-inspection">
        <label htmlFor={`inspect-${title}`}>Точная точка</label>
        <select id={`inspect-${title}`} value={chosenAt} onChange={e => setChosenAt(e.target.value)}>
          <option value="">Выберите время</option>
          {options.map(at => <option key={at} value={at}>{formatLocal(at)}</option>)}
        </select>
        {indicator !== null && (active || oldActive) && <div className="chart-tooltip" role="status">
          <strong>{formatLocal(new Date(indicator).toISOString())}</strong>
          <span className="muted small">UTC: {new Date(indicator).toISOString()}</span>
          {lines.map(line => <span key={line.metric}>
            {line.label}: {asPercent(active?.[line.metric] ?? null)}
          </span>)}
          <span className="muted small">Измерений в бакете: {active?.samples ?? '—'}</span>
          {previous && <>
            <span>Предыдущий период: {asPercent(oldActive?.[lines[0]!.metric] ?? null)}</span>
            <span className="muted small">Фактическое время предыдущей точки:
              {' '}{oldActive ? formatLocal(oldActive.at) : '—'}</span>
          </>}
        </div>}
      </div>
    </>}
  </section>
}
