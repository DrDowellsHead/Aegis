import type { AgentInfo, AgentPage, AgentStatus } from './types'
import { requestJSON } from './request'
export { ApiError } from './request'

export interface DashboardSession {
  authenticated: boolean
  mode: 'local' | 'remote'
  username?: string
  role?: 'admin' | 'viewer'
}
export interface DashboardUser { username: string; role: 'admin' | 'viewer' }

export interface AgentSummary {
  total: number
  online: number
  offline: number
  unknown: number
  checked_at: string
  offline_after: string
}

function record(data: unknown): Record<string, unknown> {
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('Expected JSON object')
  }
  return data as Record<string, unknown>
}
function count(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    throw new Error('Expected nonnegative integer')
  }
  return value
}
function status(value: unknown): AgentStatus {
  if (value === 'online' || value === 'offline' || value === 'unknown') return value
  throw new Error('Unknown agent state')
}
function maybeString(value: unknown): string | null {
  if (value === null || typeof value === 'string') return value
  throw new Error('Expected string or null')
}
function stringValue(value: unknown): string {
  if (typeof value !== 'string') throw new Error('Expected string')
  return value
}
function agent(input: unknown): AgentInfo {
  const r = record(input)
  const value: AgentInfo = {
    agent_id: stringValue(r.agent_id),
    status: status(r.status),
    registered_at: stringValue(r.registered_at),
    last_contact_at: maybeString(r.last_contact_at),
    last_sample_at: maybeString(r.last_sample_at)
  }
  if (typeof r.os === 'string') value.os = r.os
  if (typeof r.arch === 'string') value.arch = r.arch
  return value
}
export async function checkSession(signal?: AbortSignal): Promise<DashboardSession> {
  const data = record(await requestJSON('/api/web/session', { signal }))
  if (typeof data.authenticated !== 'boolean') throw new Error('Invalid session response')
  const mode = data.mode === 'remote' ? 'remote' : 'local'
  const session: DashboardSession = { authenticated: data.authenticated, mode }
  if (data.authenticated && mode === 'remote') {
    if (typeof data.username !== 'string' || (data.role !== 'admin' && data.role !== 'viewer')) {
      throw new Error('Invalid remote session identity')
    }
    session.username = data.username
    session.role = data.role
  }
  return session
}
export async function signIn(token: string, username?: string): Promise<void> {
  const body = username === undefined ? { token } : { token, username }
  const data = record(await requestJSON('/api/web/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }))
  if (data.authenticated !== true) throw new Error('Login confirmation missing')
}

export async function loadDashboardUsers(signal?: AbortSignal): Promise<DashboardUser[]> {
  const data = record(await requestJSON('/api/web/admin/users', { signal }))
  if (!Array.isArray(data.items)) throw new Error('Invalid user listing')
  return data.items.map(item => {
    const user = record(item)
    if (typeof user.username !== 'string' || (user.role !== 'admin' && user.role !== 'viewer')) {
      throw new Error('Invalid dashboard user')
    }
    return { username: user.username, role: user.role }
  })
}

export async function revokeDashboardSessions(username: string): Promise<number> {
  const data = record(await requestJSON('/api/web/admin/sessions/revoke', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username })
  }))
  return count(data.revoked)
}
export async function signOut(): Promise<void> {
  await requestJSON('/api/web/logout', { method: 'POST' })
}
export async function loadAgents(offlineAfter: string, cursor: string, signal?: AbortSignal): Promise<AgentPage> {
  const params = new URLSearchParams({ limit: '30', offline_after: offlineAfter })
  if (cursor) params.set('cursor', cursor)
  const r = record(await requestJSON('/api/web/agents?' + params, { signal }))
  if (!Array.isArray(r.items) || typeof r.has_more !== 'boolean') throw new Error('Invalid agents page')
  const page: AgentPage = {
    items: r.items.map(agent),
    has_more: r.has_more,
    offline_after: stringValue(r.offline_after)
  }
  if (typeof r.next_cursor === 'string') page.next_cursor = r.next_cursor
  return page
}
export async function loadSummary(offlineAfter: string, signal?: AbortSignal): Promise<AgentSummary> {
  const params = new URLSearchParams({ offline_after: offlineAfter })
  const r = record(await requestJSON('/api/web/summary?' + params, { signal }))
  const summary: AgentSummary = {
    total: count(r.total),
    online: count(r.online),
    offline: count(r.offline),
    unknown: count(r.unknown),
    checked_at: stringValue(r.checked_at),
    offline_after: stringValue(r.offline_after)
  }
  if (summary.total !== summary.online + summary.offline + summary.unknown) {
    throw new Error('Inconsistent agent count')
  }
  return summary
}
