import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError } from './api/client'
import {
  loadIncidents, changeIncident, evaluateLiveness, loadIncidentEvents,
  getIncidentSettings, updateIncidentSettings, getNotificationStats, type NotificationStats,
  type Incident, type IncidentEvent, type IncidentFilter
} from './api/incidents'

const uuid=/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/
const states = { open: 'Открыт', acknowledged: 'Подтверждён', resolved: 'Разрешён' }
const actions = { opened: 'Открытие', acknowledged: 'Подтверждение', resolved: 'Разрешение',
  escalated: 'Эскалация до critical' }
function dateLabel(s: string | null): string {
  if (!s) return '—'
  const d=new Date(s)
  if (Number.isNaN(d.getTime())) return '—'
  return new Intl.DateTimeFormat('ru-RU',{dateStyle:'short',timeStyle:'medium'}).format(d)
}

export default function IncidentsPage({ canManage, onExpired }: {
  canManage: boolean; onExpired: () => void
}) {
  const [filter,setFilter]=useState<IncidentFilter>('active')
  const [agent,setAgent]=useState('')
  const [appliedAgent,setAppliedAgent]=useState('')
  const [cursorStack,setCursorStack]=useState<string[]>([])
  const [page,setPage]=useState<Incident[]>([])
  const [hasMore,setHasMore]=useState(false)
  const [nextCursor,setNextCursor]=useState('')
  const [refresh,setRefresh]=useState(0)
  const [busy,setBusy]=useState(false)
  const [loading,setLoading]=useState(true)
  const [error,setError]=useState('')
  const [notice,setNotice]=useState('')
  const [threshold,setThreshold]=useState('1m')
  const [timeline,setTimeline]=useState('')
  const [events,setEvents]=useState<IncidentEvent[]>([])
  const [eventsBusy,setEventsBusy]=useState(false)
  const [cooldown,setCooldown]=useState('300')
  const [escalation,setEscalation]=useState('900')
  const [settingsReady,setSettingsReady]=useState(false)
  const [delivery,setDelivery]=useState<NotificationStats|null>(null)

  const fail=useCallback((cause: unknown)=>{
    if (cause instanceof ApiError && cause.status===401) {onExpired();return}
    if (cause instanceof ApiError && cause.status===403) setError('Недостаточно прав для операции.')
    else if (cause instanceof ApiError && cause.status===409) {
      setError('Состояние инцидента уже изменилось. Обновите список.')
    } else if (cause instanceof ApiError && cause.status===404) {
      setError('Инцидент или зарегистрированный агент не найден.')
    } else setError('Не удалось выполнить запрос. Проверьте соединение с Go и PostgreSQL.')
  },[onExpired])

  useEffect(()=>{
    let cancelled=false
    getNotificationStats().then(stats=>{if(!cancelled)setDelivery(stats)})
      .catch(cause=>{if(!cancelled)fail(cause)})
    return ()=>{cancelled=true}
  },[refresh,fail])

  useEffect(()=>{
    let cancelled=false
    getIncidentSettings().then(value=>{
      if(cancelled)return
      setCooldown(String(value.cooldown_seconds))
      setEscalation(String(value.escalate_after_seconds))
      setSettingsReady(true)
    }).catch(cause=>{if(!cancelled)fail(cause)})
    return ()=>{cancelled=true}
  },[fail])

  useEffect(()=>{
    const controller=new AbortController()
    setLoading(true)
    loadIncidents(filter,appliedAgent,cursorStack.at(-1) ?? '',controller.signal)
      .then(data=>{
        if (controller.signal.aborted) return
        setPage(data.items)
        setHasMore(data.has_more)
        setNextCursor(data.next_cursor ?? '')
      }).catch(cause=>{if(!controller.signal.aborted) fail(cause)})
      .finally(()=>{if(!controller.signal.aborted)setLoading(false)})
    return ()=>controller.abort()
  },[filter,appliedAgent,cursorStack,refresh,fail])

  function applyAgent(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (agent && !uuid.test(agent)) {
      setError('Укажите полный UUID агента.')
      return
    }
    setError('');setNotice('');setTimeline('');setCursorStack([])
    setAppliedAgent(agent)
    setRefresh(i=>i+1)
  }

  async function perform(action:()=>Promise<unknown>,success:string) {
    setBusy(true);setError('');setNotice('')
    try {
      await action()
      setNotice(success)
      setRefresh(i=>i+1)
    } catch(cause) {fail(cause)}
    finally {setBusy(false)}
  }

  async function showEvents(id:string) {
    if (timeline===id) {setTimeline('');return}
    setTimeline(id);setEvents([]);setEventsBusy(true);setError('')
    try {
      const loaded=await loadIncidentEvents(id)
      setEvents(loaded)
    } catch(cause) {fail(cause);setTimeline('')}
    finally {setEventsBusy(false)}
  }

  return <div className="incidents-page">
    <div className="intro">
      <p className="eyebrow">ИНЦИДЕНТЫ · 5.3</p>
      <h1>Инциденты</h1>
      <p className="muted">Открытые проблемы, подтверждения операторов и восстановление. На данном этапе условия проверяются вручную.</p>
    </div>
    <section className="panel" aria-label="Фильтр инцидентов">
      <div className="panel-heading"><h2>Фильтрация</h2>
        <button type="button" disabled={loading || busy} onClick={()=>setRefresh(i=>i+1)}>Обновить</button>
      </div>
      <div className="filter-bar">
        <label htmlFor="incident-state">Состояние</label>
        <select id="incident-state" value={filter} onChange={e=>{
          setFilter(e.target.value as IncidentFilter);setCursorStack([]);setTimeline('')
        }}>
          <option value="active">Активные</option>
          <option value="resolved">Разрешённые</option>
          <option value="all">Все</option>
        </select>
      </div>
      <form className="filter-bar" onSubmit={applyAgent}>
        <label htmlFor="incident-agent">UUID агента (необязательно)</label>
        <input id="incident-agent" value={agent} onChange={e=>setAgent(e.target.value.trim())}
          placeholder="Все агенты" />
        <button type="submit" disabled={loading || busy}>Применить</button>
      </form>
    </section>
    {canManage && <section className="panel" aria-label="Проверка потери связи">
      <div className="panel-heading"><h2>Проверить доступность</h2></div>
      <p className="muted">Укажи зарегистрированный агент. Состояние определяется подтверждёнными сигналами по времени PostgreSQL, а не включением питания устройства.</p>
      <form className="filter-bar" onSubmit={e=>{
        e.preventDefault()
        if (!uuid.test(agent)) {setError('Для проверки укажите UUID агента выше.');return}
        void perform(()=>evaluateLiveness(agent,threshold),'Доступность проверена; список инцидентов обновлён.')
      }}>
        <label htmlFor="incident-threshold">Считать Offline через</label>
        <select id="incident-threshold" value={threshold} onChange={e=>setThreshold(e.target.value)}>
          <option value="1m">1 минуту</option><option value="5m">5 минут</option>
          <option value="15m">15 минут</option><option value="1h">1 час</option>
        </select>
        <button type="submit" disabled={busy}>Проверить связь</button>
      </form>
    </section>}
    <section className="panel" aria-label="Политика инцидентов">
      <div className="panel-heading"><h2>Подавление повторов и эскалация</h2></div>
      <p className="muted">После разрешения проблема не откроет новый инцидент в течение cooldown. Неподтверждённая проблема при следующей проверке повышается до critical после указанного интервала.</p>
      {settingsReady && <form className="filter-bar" onSubmit={e=>{
        e.preventDefault()
        const c=Number(cooldown),a=Number(escalation)
        if(!Number.isInteger(c)||c<0||c>86400 || !Number.isInteger(a)||a<60||a>604800){
          setError('Cooldown: 0–86400 сек.; эскалация: 60–604800 сек.')
          return
        }
        if(!canManage)return
        void perform(async ()=>{
          const result=await updateIncidentSettings(c,a)
          setCooldown(String(result.cooldown_seconds))
          setEscalation(String(result.escalate_after_seconds))
        },'Политика сохранена в PostgreSQL.')
      }}>
        <label htmlFor="incident-cooldown">Cooldown (сек., 0 — отключён)</label>
        <input id="incident-cooldown" type="number" min={0} max={86400} step={1}
          value={cooldown} disabled={!canManage || busy}
          onChange={e=>setCooldown(e.target.value)}/>
        <label htmlFor="incident-escalation">Эскалация после (сек.)</label>
        <input id="incident-escalation" type="number" min={60} max={604800} step={1}
          value={escalation} disabled={!canManage || busy}
          onChange={e=>setEscalation(e.target.value)}/>
        {canManage && <button type="submit" disabled={busy}>Сохранить политику</button>}
      </form>}
    </section>
    <section className="panel" aria-label="Доставка уведомлений">
      <div className="panel-heading"><h2>Очередь уведомлений</h2></div>
      <p className="muted">Email, Telegram и Webhook доставляются отдельным процессом Aegis notify. Состояния очереди сохранены в PostgreSQL.</p>
      {delivery ? <p role="status">Ожидают: {delivery.pending} · Повтор: {delivery.retry} · В работе: {delivery.leased} · Доставлено: {delivery.sent} · Ошибки без повторов: {delivery.dead}</p> :
        <p className="muted">Нет данных об очереди.</p>}
    </section>
    <section className="panel" aria-label="Список инцидентов">
      <div className="panel-heading"><h2>Список инцидентов</h2>
        <span className="muted small">По времени возникновения, новые первыми</span>
      </div>
      {loading && <p role="status">Загружаем инциденты…</p>}
      {!loading && page.length===0 && <p className="muted">Инциденты по выбранному фильтру не найдены.</p>}
      {page.length>0 && <div className="agent-table-wrap"><table className="agent-table">
        <thead><tr><th>Проблема</th><th>Агент</th><th>Открыт</th><th>Состояние</th><th>Действия</th></tr></thead>
        <tbody>{page.map(item=><tr key={item.id}>
          <td><strong>{item.title}</strong>
            <div className="muted small">{item.kind==='threshold'?'Пороговое правило':'Потеря связи'}</div>
          </td>
          <td className="mono">{item.agent_id}</td>
          <td>{dateLabel(item.opened_at)}</td>
          <td><span className={item.state==='resolved'?'status-pill status-online':
            item.state==='open'?'status-pill status-offline':'status-pill status-unknown'}>{states[item.state]}</span>
            <div className="muted small">Приоритет: {item.severity==='critical'?'CRITICAL':'warning'}</div>
            {item.escalated_at && <div className="muted small">Эскалация: {dateLabel(item.escalated_at)}</div>}
            {item.suppressed_checks>0 && <div className="muted small">Подавлено проверок: {item.suppressed_checks}</div>}
            {item.acknowledged_by && <div className="muted small">Принял: {item.acknowledged_by}</div>}
          </td>
          <td>
            <div className="filter-bar">
              {canManage && item.state==='open' && <button type="button" disabled={busy}
                onClick={()=>void perform(()=>changeIncident(item.id,'acknowledge'),'Инцидент подтверждён.')}>
                Подтвердить
              </button>}
              {canManage && item.state!=='resolved' && <button type="button" disabled={busy}
                onClick={()=>{
                  if (window.confirm('Разрешить вручную? Если проблема останется, следующая проверка создаст новый инцидент.')) {
                    void perform(()=>changeIncident(item.id,'resolve'),'Инцидент разрешён вручную.')
                  }
                }}>Разрешить</button>}
              <button type="button" onClick={()=>void showEvents(item.id)}>
                {timeline===item.id?'Скрыть историю':'История'}
              </button>
            </div>
            {timeline===item.id && <div className="muted small" role="status">
              {eventsBusy?'Загружаем историю…':events.length===0?'Событий пока нет':
                <ul>{events.map((event,index)=><li key={index}>
                  {actions[event.action]} · {dateLabel(event.at)} · {event.actor}
                  {event.reason ? ' · ' + event.reason : ''}
                </li>)}</ul>}
            </div>}
          </td>
        </tr>)}</tbody>
      </table></div>}
      <div className="table-controls">
        <button type="button" disabled={loading || busy || cursorStack.length===0}
          onClick={()=>setCursorStack(a=>a.slice(0,-1))}>← Назад</button>
        <span className="muted small">Страница {cursorStack.length+1}</span>
        <button type="button" disabled={loading || busy || !hasMore || !nextCursor}
          onClick={()=>setCursorStack(a=>[...a,nextCursor])}>Далее →</button>
      </div>
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
    </section>
  </div>
}
