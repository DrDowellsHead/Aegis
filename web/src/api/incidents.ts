import { requestJSON } from './request'

export type IncidentState = 'open' | 'acknowledged' | 'resolved'
export type IncidentKind = 'threshold' | 'agent_offline'
export type IncidentFilter = 'active' | 'resolved' | 'all'
export interface Incident {
  id: string
  agent_id: string
  rule_id: string | null
  kind: IncidentKind
  title: string
  state: IncidentState
  opened_at: string
  acknowledged_at: string | null
  acknowledged_by: string | null
  resolved_at: string | null
  resolution: string | null
}
export interface IncidentPage {
  items: Incident[]
  has_more: boolean
  next_cursor?: string
}
export interface IncidentEvent {
  action: 'opened' | 'acknowledged' | 'resolved'
  actor: string
  at: string
  reason: string
}
function obj(v: unknown): Record<string, unknown> {
  if (!v || Array.isArray(v) || typeof v !== 'object') throw new Error('Invalid JSON object')
  return v as Record<string, unknown>
}
function optionalString(v: unknown): v is string | null {
  return v === null || typeof v === 'string'
}
function readIncident(raw: unknown): Incident {
  const v = obj(raw)
  if (typeof v.id !== 'string' || typeof v.agent_id !== 'string' ||
      !optionalString(v.rule_id) || typeof v.title !== 'string' ||
      (v.kind !== 'threshold' && v.kind !== 'agent_offline') ||
      !['open', 'acknowledged', 'resolved'].includes(String(v.state)) ||
      typeof v.opened_at !== 'string' ||
      !optionalString(v.acknowledged_at) || !optionalString(v.acknowledged_by) ||
      !optionalString(v.resolved_at) || !optionalString(v.resolution)) {
    throw new Error('Invalid incident response')
  }
  return v as unknown as Incident
}
export async function loadIncidents(filter: IncidentFilter, agentID: string, cursor: string,
  signal?: AbortSignal): Promise<IncidentPage> {
  const params = new URLSearchParams({ state: filter, limit: '50' })
  if (agentID) params.set('agent_id', agentID)
  if (cursor) params.set('cursor', cursor)
  const v = obj(await requestJSON('/api/web/incidents?' + params, { signal }))
  if (!Array.isArray(v.items) || typeof v.has_more !== 'boolean' || v.items.length > 50 ||
    (v.has_more && typeof v.next_cursor !== 'string')) throw new Error('Invalid incident page')
  return {
    items: v.items.map(readIncident), has_more: v.has_more,
    ...(typeof v.next_cursor === 'string' ? { next_cursor: v.next_cursor } : {})
  }
}
export async function loadIncidentEvents(id: string): Promise<IncidentEvent[]> {
  const v = obj(await requestJSON('/api/web/incidents/events?id=' + encodeURIComponent(id)))
  if (!Array.isArray(v.items) || v.items.length > 100) throw new Error('Invalid incident events')
  return v.items.map(raw => {
    const e = obj(raw)
    if (!['opened', 'acknowledged', 'resolved'].includes(String(e.action)) ||
      typeof e.actor !== 'string' || typeof e.at !== 'string' || typeof e.reason !== 'string') {
      throw new Error('Invalid incident event')
    }
    return e as unknown as IncidentEvent
  })
}
export async function changeIncident(id: string, action: 'acknowledge' | 'resolve'): Promise<Incident> {
  return readIncident(await requestJSON('/api/web/incidents/' + action, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id })
  }))
}
export async function evaluateLiveness(agentID: string, offlineAfter: string): Promise<string> {
  const v = obj(await requestJSON('/api/web/incidents/evaluate', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: agentID, offline_after: offlineAfter })
  }))
  if (!['online', 'offline', 'unknown'].includes(String(v.status))) {
    throw new Error('Invalid liveness status')
  }
  return String(v.status)
}
