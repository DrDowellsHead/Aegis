import { useEffect, useMemo, useState } from 'react'
import type { MeasurementPage, Series, SeriesPoint } from './api/types'
import {
  fetchHistory, fetchSeries, makeWindow, seriesSegments,
  type Period, type Window
} from './api/detail'
import { ApiError } from './api/client'

type Metric = 'cpu_avg' | 'cpu_min' | 'cpu_max' | 'memory_avg' | 'disk_avg'

const stepMilliseconds: Record<Window['step'], number> = {
  '1m': 60_000, '5m': 300_000, '1h': 3_600_000
}

function when(timestamp: string): string {
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return 'Неизвестное время'
  return new Intl.DateTimeFormat('ru-RU', {
    dateStyle: 'medium', timeStyle: 'medium'
  }).format(date)
}
function percent(value: number | null): string {
  return value === null ? '—' : value.toFixed(1) + ' %'
}

type Line = { metric: Metric; label: string; color: string }
const cpuLines: Line[] = [
  { metric: 'cpu_avg', label: 'Среднее', color: '#6dc9ee' },
  { metric: 'cpu_min', label: 'Минимум', color: '#7dd8a9' },
  { metric: 'cpu_max', label: 'Максимум', color: '#f1b47b' }
]

function Trend({ label, description, points, window, lines }: {
  label: string
  description: string
  points: SeriesPoint[]
  window: Window
  lines: Line[]
}) {
  const start = Date.parse(window.from)
  const end = Date.parse(window.to)
  const duration = end - start
  const width = 720
  const height = 220
  const pad = 34
  const chartWidth = width - pad * 2
  const chartHeight = height - pad * 2
  const x = (at: string) => pad + (Date.parse(at) - start) / duration * chartWidth
  const y = (value: number) => height - pad - (Math.max(0, Math.min(100, value)) / 100) * chartHeight
  const available = lines.some(line => points.some(point => point[line.metric] !== null))

  return <section className="panel chart-panel" aria-label={label}>
    <div className="panel-heading">
      <h2>{label}</h2>
      <span className="muted small">{description}</span>
    </div>
    {!available ? <div className="chart-empty">
      Нет измерений этой метрики за выбранный период.
      Отсутствующие значения не заменяются нулём.
    </div> : <>
      <svg role="img" viewBox={`0 0 ${width} ${height}`} className="trend-svg"
        aria-label={`График «${label}», проценты использования за ${window.period}`}>
        {[0, 25, 50, 75, 100].map(tick => <g key={tick}>
          <line x1={pad} x2={width - pad} y1={y(tick)} y2={y(tick)}
            stroke="#34465b" strokeWidth="1" />
          <text x={pad - 10} y={y(tick) + 4} textAnchor="end"
            fill="#93a8bd" fontSize="11">{tick}</text>
        </g>)}
        {lines.map(line =>
          seriesSegments(points, line.metric, stepMilliseconds[window.step]).map((segment, index) => {
            const coords = segment.map(point =>
              `${x(point.at).toFixed(2)},${y(point[line.metric] ?? 0).toFixed(2)}`
            )
            const info = segment.map(point =>
              `${when(point.at)}: ${percent(point[line.metric])}`
            ).join('; ')
            if (segment.length === 1) {
              return <circle key={`${line.metric}-${index}`} cx={x(segment[0]!.at)}
                cy={y(segment[0]![line.metric]!)} r="3.5" fill={line.color}>
                <title>{line.label}: {info}</title>
              </circle>
            }
            return <polyline key={`${line.metric}-${index}`} fill="none"
              stroke={line.color} strokeWidth="2" strokeLinejoin="round"
              strokeLinecap="round" points={coords.join(' ')}>
              <title>{line.label}: {info}</title>
            </polyline>
          })
        )}
        <text x={pad} y={height - 6} fontSize="11" fill="#91a6bb">
          {new Date(window.from).toLocaleDateString('ru-RU')}
        </text>
        <text x={width - pad} y={height - 6} textAnchor="end" fontSize="11" fill="#91a6bb">
          {new Date(window.to).toLocaleDateString('ru-RU')}
        </text>
      </svg>
      <div className="chart-legend">
        {lines.map(line => <span key={line.metric}>
          <i style={{ background: line.color }} />{line.label}
        </span>)}
      </div>
    </>}
  </section>
}

export default function AgentDetail({ id, refresh, onExpired }: {
  id: string
  refresh: number
  onExpired: () => void
}) {
  const [period, setPeriod] = useState<Period>('24h')
  const [window, setWindow] = useState<Window>(() => makeWindow('24h'))
  const [diskPath, setDiskPath] = useState('')
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const [history, setHistory] = useState<MeasurementPage | null>(null)
  const [series, setSeries] = useState<Series | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const cursor = cursorStack.at(-1) ?? ''

  // Refresh fixes a NEW UTC window. Pagination keeps the SAME window
  // and therefore cannot silently invalidate the time-bound cursor.
  useEffect(() => {
    setWindow(makeWindow(period))
    setCursorStack([])
  }, [period, refresh, id])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    setHistory(null)
    setSeries(null)

    Promise.all([
      fetchHistory(id, window, cursor, controller.signal),
      fetchSeries(id, window, diskPath, controller.signal)
    ]).then(([nextHistory, nextSeries]) => {
      if (controller.signal.aborted) return
      setHistory(nextHistory)
      setSeries(nextSeries)
      if (!diskPath) {
        const discovered = nextHistory.items.find(item => item.snapshot.disk?.path)?.snapshot.disk?.path
        if (discovered) setDiskPath(discovered)
      }
    }).catch((reason: unknown) => {
      if (controller.signal.aborted) return
      if (reason instanceof ApiError && reason.status === 401) {
        onExpired()
      } else {
        setError('Не удалось получить графики или историю. Проверьте PostgreSQL и попробуйте обновить страницу.')
      }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [id, window, cursor, diskPath, onExpired])

  const rows = history?.items ?? []
  const diskPaths = useMemo(() => Array.from(new Set(rows
    .map(row => row.snapshot.disk?.path).filter((s): s is string => Boolean(s)))), [rows])
  const points = series?.points ?? []
  const newest = rows[0]
  return <div className="detail-page">
    <div className="intro">
      <p className="eyebrow"><a href="/agents">АГЕНТЫ</a> / УСТРОЙСТВО</p>
      <h1>История компьютера</h1>
      <p className="muted mono long-id">{id}</p>
      {newest && <p className="muted small">Последнее измерение в выбранном периоде: {when(newest.started_at)} · {newest.snapshot.os} / {newest.snapshot.arch}</p>}
    </div>
    <div className="detail-toolbar">
      <label htmlFor="detail-period">Период</label>
      <select id="detail-period" value={period} onChange={e => setPeriod(e.target.value as Period)}>
        <option value="1h">Последний час</option>
        <option value="24h">24 часа</option>
        <option value="7d">7 дней</option>
      </select>
      <label htmlFor="detail-disk">Диск</label>
      <input id="detail-disk" list="disk-paths" placeholder="Путь диска (например, C:\\)"
        value={diskPath} onChange={e => setDiskPath(e.target.value)} maxLength={1024} />
      <datalist id="disk-paths">
        {diskPaths.map(path => <option value={path} key={path} />)}
      </datalist>
      {loading && <span className="muted small" role="status">Загрузка…</span>}
    </div>
    <p className="muted small">Период фиксируется на момент запроса. Время — местный часовой пояс браузера.
      Диск агрегируется только по точному совпадению пути; показания разных дисков не смешиваются.</p>
    {error && <div className="data-error" role="alert">{error}</div>}
    {series && <>
      <div className="detail-charts">
        <Trend label="CPU" description="Загрузка процессора, % (avg/min/max)"
          points={points} window={window} lines={cpuLines} />
        <Trend label="RAM" description="Использовано оперативной памяти, %"
          points={points} window={window}
          lines={[{ metric: 'memory_avg', label: 'Среднее', color: '#8db9f7' }]} />
        <Trend label="Диск" description={diskPath ? `Занято, % · ${diskPath}` : 'Выберите путь диска'}
          points={points} window={window}
          lines={[{ metric: 'disk_avg', label: 'Среднее', color: '#e4b476' }]} />
      </div>
      <p className="muted small">
        Графики строятся по агрегатам PostgreSQL. Разрывы данных не соединяются линией.
        Сетевые счётчики пока не показываются как скорость: для неё потребуются корректные дельты между измерениями.
      </p>
    </>}
    <section className="panel measurement-panel">
      <div className="panel-heading">
        <h2>Отдельные измерения</h2>
        <span className="muted small">По 20 записей · новые сверху</span>
      </div>
      {!history && !loading && !error &&
        <p className="muted measurement-note">Нет загруженных данных.</p>}
      {history && rows.length === 0 && <p className="muted measurement-note">
        За выбранный период измерений не найдено. Это не означает, что все показатели равны нулю.
      </p>}
      {rows.length > 0 && <div className="agent-table-wrap">
        <table className="agent-table">
          <thead><tr><th>Время измерения</th><th>CPU</th><th>RAM</th><th>Диск</th><th>Получено сервером</th><th>Ошибки</th></tr></thead>
          <tbody>{rows.map(row => {
            const errs = Object.entries(row.snapshot.errors ?? {})
            return <tr key={row.measurement_id}>
              <td><time title={row.started_at} dateTime={row.started_at}>{when(row.started_at)}</time></td>
              <td>{percent(row.snapshot.cpu?.used_percent ?? null)}</td>
              <td>{percent(row.snapshot.memory?.used_percent ?? null)}</td>
              <td>{row.snapshot.disk ? <>{percent(row.snapshot.disk.used_percent)}
                <div className="muted small mono">{row.snapshot.disk.path}</div></> : '—'}</td>
              <td><time title={row.received_at} dateTime={row.received_at}>{when(row.received_at)}</time></td>
              <td>{errs.length ? <details>
                <summary>{errs.length} недоступных метрик</summary>
                {errs.map(([name, message]) => <p key={name} className="small">
                  <strong>{name}:</strong> {message}
                </p>)}
              </details> : '—'}</td>
            </tr>
          })}</tbody>
        </table>
      </div>}
      <div className="table-controls">
        <button disabled={loading || cursorStack.length === 0}
          onClick={() => setCursorStack(stack => stack.slice(0, -1))}>← Назад</button>
        <span className="muted small">Страница {cursorStack.length + 1}</span>
        <button disabled={loading || !history?.has_more || !history.next_cursor}
          onClick={() => { if (history?.next_cursor) setCursorStack(stack => [...stack, history.next_cursor!]) }}>
          Далее →
        </button>
      </div>
    </section>
  </div>
}
