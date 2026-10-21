import { useCallback,useEffect,useState,type FormEvent } from 'react'
import { ApiError } from './api/client'
import { loadActivity,type ActivityFilters,type ActivityEntry } from './api/activity'

const empty:ActivityFilters={
  category:'all',action:'',actor:'',agent_id:'',incident_id:'',
  search:'',from:'',to:''
}
const uuid=/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/
const actionLabels:Record<string,string>={
  opened:'Открытие',acknowledged:'Подтверждение',resolved:'Разрешение',
  escalated:'Эскалация',rule_created:'Создание правила',
  rule_deleted:'Удаление правила',rules_evaluated:'Проверка правил',
  connectivity_evaluated:'Проверка связи',policy_updated:'Политика изменена',
  routes_updated:'Изменение маршрутов',sent:'Доставлено',
  retry:'Повтор доставки',dead:'Ошибка доставки'
}
function timeLabel(raw:string){
  const value=new Date(raw)
  return Number.isNaN(value.getTime())?'—':
    new Intl.DateTimeFormat('ru-RU',{dateStyle:'short',timeStyle:'medium'}).format(value)
}
function toISO(raw:string):string {
  if(!raw)return ''
  const d=new Date(raw)
  return Number.isNaN(d.getTime())?'':d.toISOString()
}
export default function ActivityPage({onExpired}:{onExpired:()=>void}){
  const [draft,setDraft]=useState<ActivityFilters>(empty)
  const [filters,setFilters]=useState<ActivityFilters>(empty)
  const [stack,setStack]=useState<string[]>([])
  const [records,setRecords]=useState<ActivityEntry[]>([])
  const [next,setNext]=useState('')
  const [more,setMore]=useState(false)
  const [busy,setBusy]=useState(true)
  const [error,setError]=useState('')
  const [refresh,setRefresh]=useState(0)
  const fail=useCallback((e:unknown)=>{
    if(e instanceof ApiError && e.status===401)onExpired()
    else setError('Не удалось получить журнал. Проверьте Go Dashboard и PostgreSQL.')
  },[onExpired])

  useEffect(()=>{
    const ctrl=new AbortController()
    setBusy(true)
    loadActivity(filters,stack.at(-1)??'',ctrl.signal)
      .then(page=>{
        if(ctrl.signal.aborted)return
        setRecords(page.items);setMore(page.has_more);setNext(page.next_cursor??'')
      }).catch(e=>{if(!ctrl.signal.aborted)fail(e)})
      .finally(()=>{if(!ctrl.signal.aborted)setBusy(false)})
    return ()=>ctrl.abort()
  },[filters,stack,refresh,fail])

  const field=(key:keyof ActivityFilters,value:string)=>
    setDraft(prev=>({...prev,[key]:value}))

  function submit(e:FormEvent<HTMLFormElement>){
    e.preventDefault()
    if((draft.agent_id&&!uuid.test(draft.agent_id)) ||
       (draft.incident_id&&!uuid.test(draft.incident_id))) {
      setError('Идентификатор должен быть полным UUID.')
      return
    }
    if(draft.search.length===1 || draft.search.length>80 ||
       draft.actor.length>80){setError('Поисковая строка: 2–80 символов.');return}
    const from=toISO(draft.from),to=toISO(draft.to)
    if((draft.from&&!from)||(draft.to&&!to)||
       (from&&to&&Date.parse(from)>=Date.parse(to))) {
      setError('Неверный диапазон дат. Конец должен быть позже начала.')
      return
    }
    setError('');setStack([])
    setFilters({...draft,from,to})
    setRefresh(v=>v+1)
  }

  return <div className="activity-page">
    <div className="intro">
      <p className="eyebrow">АУДИТ · 5.6</p>
      <h1>Журнал событий</h1>
      <p className="muted">История инцидентов, ручных действий и попыток доставки.
        Записи хранятся в PostgreSQL. Время показывается в часовом поясе браузера.</p>
    </div>
    <section className="panel" aria-label="Поиск по журналу">
      <div className="panel-heading"><h2>Поиск и фильтры</h2></div>
      <form onSubmit={submit} className="filter-bar">
        <label htmlFor="activity-category">Категория</label>
        <select id="activity-category" value={draft.category}
          onChange={e=>field('category',e.target.value)}>
          <option value="all">Все</option>
          <option value="incident">Инциденты</option>
          <option value="operator">Операторы</option>
          <option value="delivery">Доставка</option>
        </select>
        <label htmlFor="activity-action">Действие</label>
        <select id="activity-action" value={draft.action}
          onChange={e=>field('action',e.target.value)}>
          <option value="">Все действия</option>
          {Object.entries(actionLabels).map(([k,v])=>
            <option key={k} value={k}>{v}</option>)}
        </select>
        <label htmlFor="activity-actor">Оператор или система</label>
        <input id="activity-actor" value={draft.actor}
          onChange={e=>field('actor',e.target.value)} placeholder="Например, operator"/>
        <label htmlFor="activity-search">Поиск по названию</label>
        <input id="activity-search" value={draft.search}
          onChange={e=>field('search',e.target.value)} placeholder="CPU, Agent…"/>
        <label htmlFor="activity-agent">UUID агента</label>
        <input id="activity-agent" value={draft.agent_id}
          onChange={e=>field('agent_id',e.target.value.trim())} placeholder="Необязательно"/>
        <label htmlFor="activity-incident">UUID инцидента</label>
        <input id="activity-incident" value={draft.incident_id}
          onChange={e=>field('incident_id',e.target.value.trim())} placeholder="Необязательно"/>
        <label htmlFor="activity-from">Начало периода</label>
        <input type="datetime-local" id="activity-from" value={draft.from}
          onChange={e=>field('from',e.target.value)}/>
        <label htmlFor="activity-to">Конец периода</label>
        <input type="datetime-local" id="activity-to" value={draft.to}
          onChange={e=>field('to',e.target.value)}/>
        <button disabled={busy} type="submit">Искать</button>
        <button type="button" disabled={busy} onClick={()=>{
          setDraft(empty);setFilters(empty);setStack([]);setError('')
          setRefresh(v=>v+1)
        }}>Сбросить</button>
      </form>
    </section>
    <section className="panel" aria-label="Записи журнала">
      <div className="panel-heading">
        <h2>События</h2>
        <button type="button" disabled={busy} onClick={()=>setRefresh(v=>v+1)}>Обновить</button>
      </div>
      {busy&&<p role="status">Загрузка журнала…</p>}
      {!busy&&records.length===0&&<p className="muted">События не найдены.</p>}
      {records.length>0&&<div className="agent-table-wrap">
        <table className="agent-table">
          <thead><tr><th>Время</th><th>Категория</th><th>Действие</th>
            <th>Кто</th><th>Событие и данные</th></tr></thead>
          <tbody>{records.map(e=><tr key={e.id}>
            <td>{timeLabel(e.at)}</td>
            <td>{e.category==='incident'?'Инцидент':
              e.category==='operator'?'Оператор':'Доставка'}</td>
            <td><strong>{actionLabels[e.action]??e.action}</strong></td>
            <td>{e.actor}</td>
            <td><div>{e.summary||'—'}</div>
              {e.incident_id&&<div className="muted small">Инцидент: <code>{e.incident_id}</code></div>}
              {e.agent_id&&<div className="muted small">Агент: <code>{e.agent_id}</code></div>}
              {e.reference_id&&<div className="muted small">Источник: <code>{e.reference_id}</code></div>}
              {Object.keys(e.details).length>0&&<details>
                <summary>Подробности</summary>
                <pre>{JSON.stringify(e.details,null,2)}</pre>
              </details>}
            </td>
          </tr>)}</tbody>
        </table>
      </div>}
      <div className="table-controls">
        <button type="button" disabled={busy||stack.length===0}
          onClick={()=>setStack(v=>v.slice(0,-1))}>← Назад</button>
        <span className="muted small">Страница {stack.length+1}</span>
        <button type="button" disabled={busy||!more||!next}
          onClick={()=>setStack(v=>[...v,next])}>Далее →</button>
      </div>
      {error&&<p role="alert" className="form-error">{error}</p>}
    </section>
  </div>
}
