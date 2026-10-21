import { requestJSON } from './request'

export type Metric = 'cpu' | 'memory' | 'disk'
export type Direction = 'above' | 'below'
export type RuleStatus = 'not_evaluated' | 'normal' | 'firing' | 'insufficient_data'

export interface RuleInput {
  agent_id: string
  name: string
  metric: Metric
  direction: Direction
  fire_at: number
  recover_at: number
  window_seconds: number
  min_samples: number
  max_age_seconds: number
  disk_path: string
}

export interface RuleRecord extends RuleInput {
  id: string
  firing: boolean
  last_status: RuleStatus
  last_average: number | null
  sample_count: number
  last_sample_at: string | null
  evaluated_at: string | null
  changed_at: string | null
}

function object(data: unknown): Record<string, unknown> {
  if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('Invalid rule JSON object')
  return data as Record<string, unknown>
}

function parseRules(data: unknown): RuleRecord[] {
  const root = object(data)
  if (!Array.isArray(root.items) || root.items.length > 50) throw new Error('Invalid rule list')
  return root.items.map(raw => {
    const r = object(raw)
    if (typeof r.id !== 'string' || typeof r.agent_id !== 'string' ||
      typeof r.name !== 'string' ||
      (r.metric !== 'cpu' && r.metric !== 'memory' && r.metric !== 'disk') ||
      (r.direction !== 'above' && r.direction !== 'below') ||
      !['not_evaluated', 'normal', 'firing', 'insufficient_data'].includes(String(r.last_status)) ||
      typeof r.firing !== 'boolean' ||
      typeof r.fire_at !== 'number' || typeof r.recover_at !== 'number' ||
      typeof r.window_seconds !== 'number' || typeof r.min_samples !== 'number' ||
      typeof r.max_age_seconds !== 'number' || typeof r.disk_path !== 'string' ||
      typeof r.sample_count !== 'number' ||
      (r.last_average !== null && typeof r.last_average !== 'number')) {
      throw new Error('Invalid rule record')
    }
    return r as unknown as RuleRecord
  })
}

export async function listRules(agentID: string, signal?: AbortSignal): Promise<RuleRecord[]> {
  return parseRules(await requestJSON('/api/web/rules?agent_id=' + encodeURIComponent(agentID), { signal }))
}

export async function createRule(input: RuleInput): Promise<void> {
  await requestJSON('/api/web/rules', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input)
  })
}

export async function deleteRule(id: string): Promise<void> {
  await requestJSON('/api/web/rules/delete', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id })
  })
}

export async function evaluateRules(agentID: string): Promise<RuleRecord[]> {
  return parseRules(await requestJSON('/api/web/rules/evaluate', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: agentID })
  }))
}
