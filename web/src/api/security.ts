import { requestJSON } from './request'

export interface SecurityAuditEntry {
  id: number
  at: string
  action: string
  actor: string
  subject: string
  role: string
  affected_sessions: number
}
export interface SecurityAuditPage {
  items: SecurityAuditEntry[]
  has_more: boolean
  next_cursor?: string
}
export async function loadSecurityAudit(
  action:string,actor:string,cursor:string,signal?:AbortSignal
):Promise<SecurityAuditPage>{
  const params=new URLSearchParams({limit:'25'})
  if(action)params.set('action',action)
  if(actor)params.set('actor',actor)
  if(cursor)params.set('cursor',cursor)
  const response:unknown=await requestJSON('/api/web/admin/security-events?'+params.toString(),{signal})
  if(!response||typeof response!=='object'||Array.isArray(response))throw Error('Invalid security audit')
  const page=response as Record<string,unknown>
  if(!Array.isArray(page.items)||page.items.length>25||typeof page.has_more!=='boolean'||
    (page.has_more&&typeof page.next_cursor!=='string')){
    throw Error('Malformed security audit page')
  }
  const items:SecurityAuditEntry[]=page.items.map(value=>{
    if(!value||typeof value!=='object'||Array.isArray(value))throw Error('Malformed security event')
    const event=value as Record<string,unknown>
    if(!Number.isSafeInteger(event.id)||typeof event.at!=='string'||
      typeof event.action!=='string'||typeof event.actor!=='string'||
      typeof event.subject!=='string'||typeof event.role!=='string'||
      !Number.isSafeInteger(event.affected_sessions))throw Error('Malformed security audit event')
    return event as unknown as SecurityAuditEntry
  })
  return {items,has_more:page.has_more,
    ...(typeof page.next_cursor==='string'?{next_cursor:page.next_cursor}:{})}
}
