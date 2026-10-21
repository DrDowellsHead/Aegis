import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, loadAgents } from './api/client'
import { listRules, createRule, deleteRule, evaluateRules, type Metric, type Direction, type RuleRecord } from './api/rules'

const uuidPattern = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/
const statuses: Record<string, string> = {
  not_evaluated: 'Не проверено',
  normal: 'Норма',
  firing: 'Порог сработал',
  insufficient_data: 'Недостаточно данных'
}

export default function RulesPage({ canManage, onExpired }: { canManage: boolean; onExpired: () => void }) {
  const [agentID, setAgentID] = useState('')
  const [loadedID, setLoadedID] = useState('')
  const [knownAgents, setKnownAgents] = useState<string[]>([])
  const [rules, setRules] = useState<RuleRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [name, setName] = useState('Высокая загрузка CPU')
  const [metric, setMetric] = useState<Metric>('cpu')
  const [direction, setDirection] = useState<Direction>('above')
  const [fire, setFire] = useState('85')
  const [recover, setRecover] = useState('70')
  const [windowSeconds, setWindowSeconds] = useState('300')
  const [minSamples, setMinSamples] = useState('3')
  const [ageSeconds, setAgeSeconds] = useState('120')
  const [diskPath, setDiskPath] = useState('/')

  const fail = useCallback((cause: unknown) => {
    if (cause instanceof ApiError && cause.status === 401) {
      onExpired()
      return
    }
    if (cause instanceof ApiError && cause.status === 403) {
      setError('Недостаточно прав: нужен администратор.')
    } else if (cause instanceof ApiError && cause.status === 409) {
      setError('Правило с таким именем уже существует или превышен лимит 50 правил.')
    } else if (cause instanceof ApiError && cause.status === 404) {
      setError('Не найден зарегистрированный агент или правило.')
    } else {
      setError('Не удалось выполнить запрос. Проверьте подключение к PostgreSQL и миграцию 008.')
    }
  }, [onExpired])

  useEffect(() => {
    const controller = new AbortController()
    loadAgents('1m', '', controller.signal).then(page => {
      if (!controller.signal.aborted) setKnownAgents(page.items.map(a => a.agent_id))
    }).catch(cause => {
      if (!controller.signal.aborted) fail(cause)
    })
    return () => controller.abort()
  }, [fail])

  useEffect(() => {
    if (!loadedID) return
    const controller = new AbortController()
    setLoading(true)
    listRules(loadedID, controller.signal).then(setRules).catch(cause => {
      if (!controller.signal.aborted) fail(cause)
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [loadedID, fail])

  async function load(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError(''); setMessage('')
    if (!uuidPattern.test(agentID)) { setError('Укажите UUID агента.'); return }
    if (loadedID === agentID) {
      setLoading(true)
      try { setRules(await listRules(agentID)) } catch (cause) { fail(cause) }
      finally { setLoading(false) }
    } else {
      setRules([])
      setLoadedID(agentID)
    }
  }

  async function perform(operation: () => Promise<RuleRecord[] | void>, success: string) {
    setBusy(true); setError(''); setMessage('')
    try {
      const result = await operation()
      if (Array.isArray(result)) setRules(result)
      else setRules(await listRules(loadedID))
      setMessage(success)
    } catch (cause) { fail(cause) }
    finally { setBusy(false) }
  }

  function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const input = {
      agent_id: loadedID,
      name: name.trim(),
      metric, direction,
      fire_at: Number(fire), recover_at: Number(recover),
      window_seconds: Number(windowSeconds),
      min_samples: Number(minSamples),
      max_age_seconds: Number(ageSeconds),
      disk_path: metric === 'disk' ? diskPath : ''
    }
    if (!Number.isFinite(input.fire_at) || !Number.isFinite(input.recover_at) ||
      input.fire_at < 0 || input.fire_at > 100 || input.recover_at < 0 || input.recover_at > 100 ||
      (direction === 'above' ? input.recover_at >= input.fire_at : input.recover_at <= input.fire_at) ||
      input.max_age_seconds > input.window_seconds || input.min_samples < 1 ||
      (metric === 'disk' && !input.disk_path)) {
      setError('Проверьте пороги восстановления, путь диска и допустимую свежесть измерения.')
      return
    }
    void perform(() => createRule(input), 'Правило создано. Запустите проверку для оценки текущего окна.')
  }

  function remove(rule: RuleRecord) {
    if (!window.confirm('Удалить правило «' + rule.name + '»?')) return
    void perform(() => deleteRule(rule.id), 'Правило удалено.')
  }

  return <div className="rules-page">
    <div className="intro"><p className="eyebrow">ПРАВИЛА МОНИТОРИНГА · 5.1</p>
      <h1>Пороговые правила</h1>
      <p className="muted">Проверка среднего значения за окно времени с гистерезисом. Пока без инцидентов, фоновой проверки и уведомлений.</p>
    </div>
    <section className="panel" aria-label="Выбор агента">
      <div className="panel-heading"><h2>Агент</h2></div>
      <form className="filter-bar" onSubmit={e => void load(e)}>
        <label htmlFor="rule-agent">UUID компьютера</label>
        <input id="rule-agent" list="rule-known-agents" value={agentID}
          onChange={e => setAgentID(e.target.value)} placeholder="UUID агента" required />
        <datalist id="rule-known-agents">{knownAgents.map(id => <option value={id} key={id}/>)}</datalist>
        <button type="submit" disabled={loading}>Открыть правила</button>
      </form>
    </section>
    {loadedID && <section className="panel" aria-label="Пороговые правила агента">
      <div className="panel-heading"><h2>Правила агента</h2>
        {canManage && <button type="button" disabled={busy || loading}
          onClick={() => void perform(() => evaluateRules(loadedID), 'Правила проверены на текущем окне.')}>
          Проверить сейчас
        </button>}
      </div>
      {loading ? <p role="status">Загружаем правила…</p> :
        rules.length === 0 ? <p className="muted">Пока нет правил для этого агента.</p> :
        <div className="agent-table-wrap"><table className="agent-table">
          <thead><tr><th>Правило</th><th>Порог / возврат</th><th>Среднее / выборка</th><th>Состояние</th>{canManage && <th>Действие</th>}</tr></thead>
          <tbody>{rules.map(r => <tr key={r.id}>
            <td><strong>{r.name}</strong><div className="muted small">{r.metric}{r.disk_path ? ' · ' + r.disk_path : ''} · {r.window_seconds / 60} мин</div></td>
            <td>{r.direction === 'above' ? '≥' : '≤'} {r.fire_at}% / {r.recover_at}%</td>
            <td>{r.last_average === null ? '—' : r.last_average.toFixed(1) + '%'} / {r.sample_count}</td>
            <td><span className={r.firing ? 'status-pill status-offline' : 'status-pill status-unknown'}>{statuses[r.last_status] ?? r.last_status}</span>
              {r.last_status === 'insufficient_data' && r.firing && <div className="muted small">Предыдущее срабатывание сохраняется</div>}</td>
            {canManage && <td><button type="button" disabled={busy} onClick={() => remove(r)}>Удалить</button></td>}
          </tr>)}</tbody>
        </table></div>}
      {error && <p className="form-error" role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
    </section>}
    {loadedID && canManage && <section className="panel" aria-label="Создать пороговое правило">
      <div className="panel-heading"><h2>Создать правило</h2></div>
      <form className="login-form" onSubmit={add}>
        <label htmlFor="rule-name">Название</label>
        <input id="rule-name" value={name} maxLength={80} required onChange={e => setName(e.target.value)}/>
        <label htmlFor="rule-metric">Метрика</label>
        <select id="rule-metric" value={metric} onChange={e => setMetric(e.target.value as Metric)}>
          <option value="cpu">CPU (%)</option><option value="memory">RAM (%)</option><option value="disk">Диск (%)</option>
        </select>
        {metric === 'disk' && <><label htmlFor="rule-path">Точный путь файловой системы</label>
          <input id="rule-path" value={diskPath} required onChange={e => setDiskPath(e.target.value)}/></>}
        <label htmlFor="rule-direction">Условие</label>
        <select id="rule-direction" value={direction} onChange={e => setDirection(e.target.value as Direction)}>
          <option value="above">Выше или равно</option><option value="below">Ниже или равно</option>
        </select>
        <label htmlFor="rule-fire">Порог срабатывания (%)</label>
        <input id="rule-fire" type="number" min={0} max={100} step="0.1" value={fire} required onChange={e => setFire(e.target.value)}/>
        <label htmlFor="rule-recover">Порог восстановления (%)</label>
        <input id="rule-recover" type="number" min={0} max={100} step="0.1" value={recover} required onChange={e => setRecover(e.target.value)}/>
        <label htmlFor="rule-window">Окно измерений</label>
        <select id="rule-window" value={windowSeconds} onChange={e => {
          setWindowSeconds(e.target.value)
          if (Number(ageSeconds) > Number(e.target.value)) setAgeSeconds(e.target.value)
        }}>
          <option value="60">1 минута</option><option value="300">5 минут</option>
          <option value="900">15 минут</option><option value="3600">1 час</option>
        </select>
        <label htmlFor="rule-samples">Минимум измерений</label>
        <input id="rule-samples" type="number" min={1} max={10000} step={1}
          value={minSamples} required onChange={e => setMinSamples(e.target.value)}/>
        <label htmlFor="rule-age">Максимальный возраст последнего измерения (сек.)</label>
        <input id="rule-age" type="number" min={1} max={Number(windowSeconds)} step={1}
          value={ageSeconds} required onChange={e => setAgeSeconds(e.target.value)}/>
        <button className="primary-button" type="submit" disabled={busy}>Добавить правило</button>
      </form>
    </section>}
  </div>
}
