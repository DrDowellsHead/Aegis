import { useEffect,useState,type FormEvent } from 'react'
import { ApiError } from './api/request'
import { loadSecurityAudit,type SecurityAuditEntry } from './api/security'

const actions:Record<string,string>={
 login_ok:'Успешный вход',login_failed:'Неудачный вход',
 login_throttled:'Ограничение попыток',logout:'Выход',
 sessions_revoked:'Отзыв сессий',admin_denied:'Отказ в доступе'
}
function localTime(value:string):string{
 const d=new Date(value)
 return Number.isNaN(d.getTime())?'—':new Intl.DateTimeFormat('ru-RU',
   {dateStyle:'short',timeStyle:'medium'}).format(d)
}
export default function SecurityAuditPanel({onExpired}:{onExpired:()=>void}){
 const [draftAction,setDraftAction]=useState('')
 const [draftActor,setDraftActor]=useState('')
 const [action,setAction]=useState('')
 const [actor,setActor]=useState('')
 const [cursors,setCursors]=useState<string[]>([])
 const [rows,setRows]=useState<SecurityAuditEntry[]>([])
 const [next,setNext]=useState('')
 const [more,setMore]=useState(false)
 const [loading,setLoading]=useState(true)
 const [error,setError]=useState('')
 const [refresh,setRefresh]=useState(0)
 useEffect(()=>{
   const ctrl=new AbortController()
   setLoading(true)
   loadSecurityAudit(action,actor,cursors.at(-1)??'',ctrl.signal)
     .then(page=>{
       if(ctrl.signal.aborted)return
       setRows(page.items);setMore(page.has_more);setNext(page.next_cursor??'')
       setError('')
     }).catch(cause=>{
       if(ctrl.signal.aborted)return
       if(cause instanceof ApiError && cause.status===401)onExpired()
       else setError('Не удалось загрузить аудит безопасности.')
     }).finally(()=>{if(!ctrl.signal.aborted)setLoading(false)})
   return ()=>ctrl.abort()
 },[action,actor,cursors,refresh,onExpired])
 function submit(e:FormEvent){
   e.preventDefault()
   if(draftActor && !/^[a-z0-9_.-]{1,32}$/.test(draftActor)){
     setError('Некорректное имя оператора.')
     return
   }
   setCursors([]);setAction(draftAction);setActor(draftActor)
   setRefresh(x=>x+1)
 }
 return <section className="panel" aria-label="Аудит безопасности">
   <div className="panel-heading"><h2>Аудит безопасности</h2>
     <button type="button" disabled={loading} onClick={()=>setRefresh(x=>x+1)}>Обновить журнал</button>
   </div>
   <p className="muted small">Попытки входа, отказ в доступе, выход и отзыв сессий.
     Пароли, токены, IP-адреса и cookie в журнале не сохраняются.</p>
   <form className="filter-bar" onSubmit={submit}>
     <label htmlFor="security-action">Действие</label>
     <select id="security-action" value={draftAction} onChange={e=>setDraftAction(e.target.value)}>
       <option value="">Все</option>
       {Object.entries(actions).map(([k,v])=><option key={k} value={k}>{v}</option>)}
     </select>
     <label htmlFor="security-actor">Пользователь</label>
     <input id="security-actor" value={draftActor}
       onChange={e=>setDraftActor(e.target.value)} maxLength={32}
       placeholder="operator или unknown"/>
     <button type="submit" disabled={loading}>Применить фильтры</button>
   </form>
   {loading&&<p role="status">Загрузка аудита…</p>}
   {!loading&&rows.length===0&&<p className="muted">Записей нет.</p>}
   {rows.length>0&&<div className="agent-table-wrap"><table className="agent-table">
     <thead><tr><th>Время</th><th>Действие</th><th>Пользователь</th><th>Объект</th><th>Роль / сессии</th></tr></thead>
     <tbody>{rows.map(e=><tr key={e.id}>
       <td>{localTime(e.at)}</td><td>{actions[e.action]??e.action}</td>
       <td>{e.actor}</td><td>{e.subject||'—'}</td>
       <td>{e.role||'—'}{e.action==='sessions_revoked'? ` · ${e.affected_sessions}`:''}</td>
     </tr>)}</tbody>
   </table></div>}
   <div className="table-controls">
     <button type="button" disabled={loading||cursors.length===0}
       onClick={()=>setCursors(x=>x.slice(0,-1))}>← Назад</button>
     <span className="muted small">Страница {cursors.length+1}</span>
     <button type="button" disabled={loading||!more||!next}
       onClick={()=>setCursors(x=>[...x,next])}>Далее →</button>
   </div>
   {error&&<p role="alert" className="form-error">{error}</p>}
 </section>
}
