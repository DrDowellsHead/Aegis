import { requestJSON } from './request'

export type ActivityCategory = 'all'|'incident'|'operator'|'delivery'
export interface ActivityEntry {
  id:number
  at:string
  category:'incident'|'operator'|'delivery'
  action:string
  actor:string
  agent_id:string|null
  incident_id:string|null
  reference_id:string
  summary:string
  details:Record<string,unknown>
}
export interface ActivityPageData {
  items:ActivityEntry[]
  has_more:boolean
  next_cursor?:string
}
export interface ActivityFilters {
  category:ActivityCategory
  action:string
  actor:string
  agent_id:string
  incident_id:string
  search:string
  from:string
  to:string
}
function record(value:unknown):Record<string,unknown> {
  if (!value || typeof value!=='object' || Array.isArray(value)) {
    throw new Error('Invalid activity response')
  }
  return value as Record<string,unknown>
}
function readActivity(value:unknown):ActivityEntry {
  const r=record(value)
  if (!Number.isSafeInteger(r.id) || Number(r.id)<=0 ||
    typeof r.at!=='string' ||
    !['incident','operator','delivery'].includes(String(r.category)) ||
    typeof r.action!=='string'||typeof r.actor!=='string' ||
    (r.agent_id!==null&&typeof r.agent_id!=='string') ||
    (r.incident_id!==null&&typeof r.incident_id!=='string') ||
    typeof r.reference_id!=='string'||typeof r.summary!=='string') {
    throw new Error('Malformed activity entry')
  }
  record(r.details)
  return r as unknown as ActivityEntry
}
export async function loadActivity(filters:ActivityFilters,cursor:string,signal?:AbortSignal):Promise<ActivityPageData>{
  const params=new URLSearchParams({limit:'50'})
  for(const [key,value] of Object.entries(filters)){
    if(value&&value!=='all')params.set(key,value)
  }
  if(cursor)params.set('cursor',cursor)
  const r=record(await requestJSON('/api/web/activity?'+params.toString(),{signal}))
  if(!Array.isArray(r.items)||r.items.length>50 ||
    typeof r.has_more!=='boolean' ||
    (r.has_more && typeof r.next_cursor!=='string')) {
    throw new Error('Malformed activity page')
  }
  return {
    items:r.items.map(readActivity),has_more:r.has_more,
    ...(typeof r.next_cursor==='string'?{next_cursor:r.next_cursor}:{})
  }
}
