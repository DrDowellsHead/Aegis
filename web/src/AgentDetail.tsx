import { useEffect, useMemo, useState, type FormEvent } from 'react'
import type { MeasurementPage, Series } from './api/types'
import { fetchHistory, fetchSeries } from './api/detail'
import {
  makeWindow, makeCustomWindow, previousWindow, toUTCInput,
  formatLocal, formattedZone, type Window, type Period
} from './api/time'
import { ApiError } from './api/client'
import Trend from './Trend'

function percent(value: number | null): string {
  return value === null ? '—' : value.toFixed(1) + ' %'
}

const cpu = [
  { metric: 'cpu_avg' as const, label: 'Среднее', color: '#6dc9ee' },
  { metric: 'cpu_min' as const, label: 'Минимум', color: '#7dd8a9' },
  { metric: 'cpu_max' as const, label: 'Максимум', color: '#f1b47b' }
]

export default function AgentDetail({ id, refresh, onExpired }: {
  id: string
  refresh: number
  onExpired: () => void
}) {
  const [mode, setMode] = useState<Period>('24h')
  const [window, setWindow] = useState<Window>(() => makeWindow('24h'))
  const [utcFrom, setUTCFrom] = useState(() => toUTCInput(window.from))
  const [utcTo, setUTCTo] = useState(() => toUTCInput(window.to))
  const [rangeError, setRangeError] = useState('')
  const [compare, setCompare] = useState(false)
  const [diskPath, setDiskPath] = useState('')
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const [reload, setReload] = useState(0)
  const [history, setHistory] = useState<MeasurementPage | null>(null)
  const [series, setSeries] = useState<Series | null>(null)
  const [previous, setPrevious] = useState<Series | null>(null)
  const [seriesLoading, setSeriesLoading] = useState(true)
  const [historyLoading, setHistoryLoading] = useState(true)
  const [seriesError, setSeriesError] = useState('')
  const [historyError, setHistoryError] = useState('')
  const cursor = cursorStack.at(-1) ?? ''

  // Parent refresh is also fired by the 30-second timer. Custom fixed
  // windows never drift forward; presets deliberately advance with "now".
  useEffect(() => {
    if (mode === 'custom') {
      setReload(n => n + 1)
    } else {
      const next = makeWindow(mode)
      setWindow(next)
      setUTCFrom(toUTCInput(next.from))
      setUTCTo(toUTCInput(next.to))
      setCursorStack([])
    }
    // refresh is the trigger, not mode (handled explicitly by onPeriod).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refresh, id])

  useEffect(() => {
    const abort = new AbortController()
    setSeriesLoading(true)
    setSeriesError('')
    setSeries(null)
    setPrevious(null)
    const previousRange = previousWindow(window)
    Promise.all([
      fetchSeries(id, window, diskPath, abort.signal),
      compare ? fetchSeries(id, previousRange, diskPath, abort.signal) : Promise.resolve(null)
    ]).then(([main, older]) => {
      if (abort.signal.aborted) return
      setSeries(main)
      setPrevious(older)
    }).catch((reason: unknown) => {
      if (abort.signal.aborted) return
      if (reason instanceof ApiError && reason.status === 401) onExpired()
      else setSeriesError('Не удалось загрузить графики. Проверьте подключение к PostgreSQL.')
    }).finally(() => { if (!abort.signal.aborted) setSeriesLoading(false) })
    return () => abort.abort()
  }, [id, window, diskPath, compare, reload, onExpired])

  useEffect(() => {
    const abort = new AbortController()
    setHistoryLoading(true)
    setHistoryError('')
    setHistory(null)
    fetchHistory(id, window, cursor, abort.signal).then(page => {
      if (abort.signal.aborted) return
      setHistory(page)
      // Discover disk path from actual readings; only set an empty selection.
      const found = page.items.find(item => item.snapshot.disk?.path)?.snapshot.disk?.path
      if (found) setDiskPath(current => current || found)
    }).catch((reason: unknown) => {
      if (abort.signal.aborted) return
      if (reason instanceof ApiError && reason.status === 401) onExpired()
      else setHistoryError('Не удалось загрузить отдельные измерения. Попробуйте обновить страницу.')
    }).finally(() => { if (!abort.signal.aborted) setHistoryLoading(false) })
    return () => abort.abort()
    // History paging does not need to re-query when disk path changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, window, cursor, reload, onExpired])

  function selectPeriod(next: Period) {
    setMode(next)
    setRangeError('')
    if (next !== 'custom') {
      const chosen = makeWindow(next)
      setWindow(chosen)
      setUTCFrom(toUTCInput(chosen.from))
      setUTCTo(toUTCInput(chosen.to))
      setCursorStack([])
    }
  }

  function applyRange(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    try {
      const chosen = makeCustomWindow(utcFrom, utcTo)
      setMode('custom')
      setWindow(chosen)
      setCursorStack([])
      setRangeError('')
      setReload(i => i + 1)
    } catch (reason) {
      setRangeError(reason instanceof Error ? reason.message : 'Некорректный период.')
    }
  }

  const rows = history?.items ?? []
  const newest = rows[0]
  const disks = useMemo(() => Array.from(new Set(rows
    .map(item => item.snapshot.disk?.path)
    .filter((path): path is string => Boolean(path)))), [rows])
  const chartPoints = series?.points ?? []
  const previousPoints = previous?.points ?? null
  const prev = previousWindow(window)

  return <div className="detail-page">
    <div className="intro">
      <p className="eyebrow"><a href="/agents">АГЕНТЫ</a> / УСТРОЙСТВО</p>
      <h1>История компьютера</h1>
      <p className="muted mono long-id">{id}</p>
      {newest && <p className="muted small">Последнее измерение:
        {' '}{formatLocal(newest.started_at)} · {newest.snapshot.os} / {newest.snapshot.arch}</p>}
    </div>

    <div className="detail-toolbar">
      <label htmlFor="detail-period">Период</label>
      <select id="detail-period" value={mode}
        onChange={e => selectPeriod(e.target.value as Period)}>
        <option value="1h">Последний час</option>
        <option value="24h">24 часа</option>
        <option value="7d">7 дней</option>
        <option value="custom">Произвольный (UTC)</option>
      </select>
      <label htmlFor="detail-disk">Диск</label>
      <input id="detail-disk" list="disk-paths" placeholder="Путь диска (например, C:\\)"
        value={diskPath} onChange={e => setDiskPath(e.target.value)} maxLength={1024}/>
      <datalist id="disk-paths">{disks.map(path => <option value={path} key={path}/>)}</datalist>
      <label className="compare-toggle">
        <input type="checkbox" checked={compare} onChange={e => setCompare(e.target.checked)}/>
        Сравнить с предыдущим периодом
      </label>
      {(seriesLoading || historyLoading) && <span className="muted small" role="status">Загрузка…</span>}
    </div>

    {mode === 'custom' && <form onSubmit={applyRange} className="custom-time-form">
      <div><label htmlFor="custom-from">Начало (UTC)</label>
        <input id="custom-from" type="datetime-local" required
          value={utcFrom} onChange={e => setUTCFrom(e.target.value)}/></div>
      <div><label htmlFor="custom-to">Конец (UTC, не включительно)</label>
        <input id="custom-to" type="datetime-local" required
          value={utcTo} onChange={e => setUTCTo(e.target.value)}/></div>
      <button type="submit" className="primary-button">Применить диапазон</button>
      {rangeError && <p role="alert" className="form-error">{rangeError}</p>}
    </form>}

    <div className="time-explanation">
      <p>Выбранный интервал: <strong>{formatLocal(window.from)}</strong> —
        {' '}<strong>{formatLocal(window.to)}</strong>. Шаг агрегации:
        {' '}<strong>{window.step}</strong>, максимум 500 бакетов.</p>
      <p className="muted small">Часовой пояс отображения: {formattedZone()}.
        Произвольные границы вводятся в UTC, чтобы переход на зимнее/летнее время
        не создавал неоднозначности. Граница конца исключается из выборки.</p>
      {compare && <p className="muted small">Предыдущий равный интервал:
        {' '}{formatLocal(prev.from)} — {formatLocal(prev.to)}.
        Пунктирные линии совмещены по прошедшему времени, а не по часам на циферблате.</p>}
    </div>

    {seriesError && <div className="data-error" role="alert">{seriesError}</div>}
    {series && <div className="detail-charts">
      <Trend title="CPU" note="Загрузка процессора, % (avg / min / max)"
        current={chartPoints} previous={previousPoints} window={window} lines={cpu}/>
      <Trend title="RAM" note="Использовано оперативной памяти, %"
        current={chartPoints} previous={previousPoints} window={window}
        lines={[{ metric: 'memory_avg', label: 'Среднее', color: '#8db9f7' }]}/>
      <Trend title="Диск" note={diskPath ? `Занято, % · ${diskPath}` : 'Выберите путь диска'}
        current={chartPoints} previous={previousPoints} window={window}
        lines={[{ metric: 'disk_avg', label: 'Среднее', color: '#e4b476' }]}/>
    </div>}
    <p className="muted small">Ноль — реальное измерение; отсутствие значения — пропуск.
      Не соединяем разрывы на графиках. Сетевые счётчики не являются скоростью.</p>

    <section className="panel measurement-panel">
      <div className="panel-heading">
        <h2>Отдельные измерения</h2>
        <span className="muted small">20 записей · новые сверху · фиксированный период</span>
      </div>
      {historyError && <div className="data-error" role="alert">{historyError}</div>}
      {history && !rows.length && <p className="muted measurement-note">
        За выбранный период измерений нет. Это не означает нулевую загрузку.
      </p>}
      {rows.length > 0 && <div className="agent-table-wrap">
        <table className="agent-table">
          <thead><tr><th>Время измерения</th><th>CPU</th><th>RAM</th><th>Диск</th>
            <th>Получено сервером</th><th>Ошибки</th></tr></thead>
          <tbody>{rows.map(row => {
            const errors = Object.entries(row.snapshot.errors ?? {})
            return <tr key={row.measurement_id}>
              <td><time dateTime={row.started_at} title={row.started_at}>{formatLocal(row.started_at)}</time></td>
              <td>{percent(row.snapshot.cpu?.used_percent ?? null)}</td>
              <td>{percent(row.snapshot.memory?.used_percent ?? null)}</td>
              <td>{row.snapshot.disk ? <>{percent(row.snapshot.disk.used_percent)}
                <div className="muted small mono">{row.snapshot.disk.path}</div></> : '—'}</td>
              <td><time dateTime={row.received_at} title={row.received_at}>{formatLocal(row.received_at)}</time></td>
              <td>{errors.length ? <details><summary>{errors.length} ошибок</summary>
                {errors.map(([name, message]) => <p key={name} className="small">
                  <strong>{name}:</strong> {message}</p>)}
              </details> : '—'}</td>
            </tr>
          })}</tbody>
        </table>
      </div>}
      <div className="table-controls">
        <button disabled={historyLoading || cursorStack.length === 0}
          onClick={() => setCursorStack(a => a.slice(0, -1))}>← Назад</button>
        <span className="muted small">Страница {cursorStack.length + 1}</span>
        <button disabled={historyLoading || !history?.has_more || !history.next_cursor}
          onClick={() => { if (history?.next_cursor) setCursorStack(a => [...a, history.next_cursor!]) }}>
          Далее →</button>
      </div>
    </section>
  </div>
}
