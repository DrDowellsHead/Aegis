import { test, expect, type Page } from '@playwright/test'

const ID = '123e4567-e89b-42d3-a456-426614174000'
const TOKEN = 'dashboard-test-secret-0123456789012345'

type MockOptions = {
  signedIn?: boolean
  offline?: boolean
  failReads?: boolean
  remote?: boolean
  role?: 'admin' | 'viewer'
}
async function mockDashboard(page: Page, options: MockOptions = {}) {
  const state = {
    signedIn: options.signedIn ?? true,
    offline: options.offline ?? false,
    failReads: options.failReads ?? false,
    listRequests: 0,
    remote: options.remote ?? false,
    role: options.role ?? 'admin'
  }
  await page.route('**/api/web/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const name = url.pathname
    const respond = async (status: number, data: unknown) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    if (name === '/api/web/session') {
      await respond(200, state.remote ? {
        authenticated: state.signedIn, mode: 'remote',
        ...(state.signedIn ? { username: 'operator', role: state.role } : {})
      } : { authenticated: state.signedIn }); return
    }
    if (name === '/api/web/login') {
      const body = request.postDataJSON() as { token?: string; username?: string }
      if (body.token !== TOKEN || (state.remote && body.username !== 'operator')) { await respond(401, { error: 'unauthorized' }); return }
      state.signedIn = true
      await respond(200, { authenticated: true }); return
    }
    if (name === '/api/web/logout') {
      state.signedIn = false
      await respond(200, { authenticated: false }); return
    }
    if (!state.signedIn) { await respond(401, { error: 'unauthorized' }); return }
    if (name === '/api/web/admin/users') {
      if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
      await respond(200, { items: [
        { username: 'operator', role: 'admin' },
        { username: 'reader', role: 'viewer' }
      ] }); return
    }
    if (name === '/api/web/admin/sessions/revoke') {
      if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
      await respond(200, { revoked: 1 }); return
    }

    if (name === '/api/web/activity') {
      await respond(200, { items: [{
        id: 101, at: '2026-10-21T10:00:00Z',
        category: 'incident', action: 'opened', actor: 'operator',
        agent_id: ID, incident_id: ID, reference_id: '',
        summary: 'CPU high', details: { kind: 'threshold', severity: 'warning' }
      }], has_more: false })
      return
    }
    if (name === '/api/web/watch/status') {
      await respond(200, {
        state: 'ok', started_at: '2026-10-21T11:00:00Z',
        finished_at: '2026-10-21T11:00:01Z', last_success_at: '2026-10-21T11:00:01Z',
        progress_at: '2026-10-21T11:00:01Z',
        agents_checked: 5, rules_checked: 8, errors: 0,
        interval_seconds: 30, offline_after_seconds: 60, stale: false
      }); return
    }
    if (name === '/api/web/notifications/stats') {
      await respond(200, { pending: 3, retry: 1, leased: 0, sent: 7, dead: 0 }); return
    }
    if (name === '/api/web/incidents/settings') {
      if (request.method() === 'POST' && state.role !== 'admin') {
        await respond(403, { error: 'forbidden' }); return
      }
      const body = request.method() === 'POST' ? request.postDataJSON() as {
        cooldown_seconds: number; escalate_after_seconds: number
      } : { cooldown_seconds: 300, escalate_after_seconds: 900 }
      await respond(200, { ...body, updated_at: new Date().toISOString() }); return
    }
    if (name === '/api/web/incidents') {
      const active = url.searchParams.get('state') ?? 'active'
      const item = {
        id: ID, agent_id: ID, rule_id: ID, kind: 'threshold', title: 'CPU high',
        state: active === 'resolved' ? 'resolved' : 'open',
        opened_at: '2026-10-21T10:00:00Z',
        acknowledged_at: null, acknowledged_by: null,
        resolved_at: null, resolution: null,
        severity: 'warning', escalated_at: null, suppressed_checks: 0, last_suppressed_at: null
      }
      await respond(200, { items: [item], has_more: false }); return
    }
    if (name === '/api/web/incidents/events') {
      await respond(200, { items: [{
        action: 'opened', actor: 'system', at: '2026-10-21T10:00:00Z', reason: 'condition_detected'
      }] }); return
    }
    if (name === '/api/web/incidents/evaluate') {
      if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
      await respond(200, { status: 'offline' }); return
    }
    if (name === '/api/web/incidents/acknowledge' || name === '/api/web/incidents/resolve') {
      if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
      await respond(200, {
        id: ID, agent_id: ID, rule_id: ID, kind: 'threshold', title: 'CPU high',
        state: name.endsWith('acknowledge') ? 'acknowledged' : 'resolved',
        opened_at: '2026-10-21T10:00:00Z',
        acknowledged_at: null, acknowledged_by: null,
        resolved_at: null, resolution: null,
        severity: 'warning', escalated_at: null,
        suppressed_checks: 0, last_suppressed_at: null
      }); return
    }
    if (name === '/api/web/rules') {
      if (request.method() === 'POST') {
        if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
        await respond(201, { id: ID }); return
      }
      await respond(200, { items: [{
        id: ID, agent_id: ID, name: 'CPU high', metric: 'cpu', direction: 'above',
        fire_at: 85, recover_at: 70, window_seconds: 300, min_samples: 3,
        max_age_seconds: 120, disk_path: '', firing: false, last_status: 'not_evaluated',
        last_average: null, sample_count: 0, last_sample_at: null,
        evaluated_at: null, changed_at: null
      }] }); return
    }
    if (name === '/api/web/rules/evaluate') {
      if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
      await respond(200, { items: [{
        id: ID, agent_id: ID, name: 'CPU high', metric: 'cpu', direction: 'above',
        fire_at: 85, recover_at: 70, window_seconds: 300, min_samples: 3,
        max_age_seconds: 120, disk_path: '', firing: true, last_status: 'firing',
        last_average: 90, sample_count: 4, last_sample_at: new Date().toISOString(),
        evaluated_at: new Date().toISOString(), changed_at: new Date().toISOString()
      }] }); return
    }
    if (name === '/api/web/rules/delete') {
      if (state.role !== 'admin') { await respond(403, { error: 'forbidden' }); return }
      await respond(200, { deleted: true }); return
    }
    if (state.failReads) { await respond(503, { error: 'data_unavailable' }); return }
    if (name === '/api/web/summary') {
      await respond(200, {
        total: 1, online: state.offline ? 0 : 1, offline: state.offline ? 1 : 0,
        unknown: 0, checked_at: new Date().toISOString(),
        offline_after: url.searchParams.get('offline_after') ?? '1m'
      }); return
    }
    if (name === '/api/web/agents') {
      state.listRequests++
      await respond(200, {
        items: [{
          agent_id: ID, status: state.offline ? 'offline' : 'online',
          registered_at: '2026-10-01T12:00:00Z',
          last_contact_at: '2026-10-21T06:00:00Z',
          last_heartbeat_at: '2026-10-21T06:00:00Z',
          last_telemetry_at: null,
          last_sample_at: null,
          os: 'linux', arch: 'amd64'
        }],
        has_more: false, offline_after: url.searchParams.get('offline_after') ?? '1m'
      }); return
    }
    if (name === '/api/web/series') {
      const from = url.searchParams.get('from') ?? ''
      const to = url.searchParams.get('to') ?? ''
      const step = url.searchParams.get('step') ?? '5m'
      const at = new Date(Date.parse(from) + 300_000).toISOString()
      await respond(200, {
        agent_id: ID, from, to, step,
        disk_path: url.searchParams.get('disk_path') ?? '',
        points: [{
          at, samples: 1, cpu_samples: 1,
          cpu_avg: 45, cpu_min: 30, cpu_max: 62,
          memory_samples: 1, memory_avg: 52,
          disk_samples: 1, disk_avg: 70
        }]
      }); return
    }
    if (name === '/api/web/measurements') {
      const now = new Date().toISOString()
      await respond(200, {
        items: [{
          agent_id: ID, measurement_id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
          started_at: now, received_at: now,
          snapshot: {
            schema_version: 1, started_at: now, duration_ms: 40,
            os: 'linux', arch: 'amd64', cpu: { used_percent: 45 },
            memory: { total_bytes: 10000, available_bytes: 4800,
              used_bytes: 5200, used_percent: 52 },
            disk: { path: '/', total_bytes: 10000, free_bytes: 3000,
              used_bytes: 7000, used_percent: 70 },
            network: null, errors: {}
          }
        }], has_more: false
      }); return
    }
    await respond(404, { error: 'not_found' })
  })
  return state
}

test('local login, real navigation and exact agent chart inspection', async ({ page }) => {
  await mockDashboard(page, { signedIn: false })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Вход в Aegis' })).toBeVisible()
  await page.locator('#dashboard-token').fill(TOKEN)
  await page.getByRole('button', { name: 'Войти' }).click()
  await expect(page.getByRole('heading', { name: 'Обзор инфраструктуры' })).toBeVisible()
  await expect(page.getByRole('link', { name: ID })).toBeVisible()

  await page.getByRole('link', { name: ID }).click()
  await expect(page.getByRole('heading', { name: 'История компьютера' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'CPU', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Отдельные измерения' })).toBeVisible()
  await page.getByLabel('Точная точка').first().selectOption({ index: 1 })
  await expect(page.getByText('UTC:', { exact: false }).first()).toBeVisible()
  await page.getByLabel('Период', { exact: true }).selectOption('custom')
  await page.locator('#custom-from').fill('2026-10-20T08:00')
  await page.locator('#custom-to').fill('2026-10-20T12:00')
  await page.getByRole('button', { name: 'Применить диапазон' }).click()
  await expect(page.getByText('максимум 500 бакетов', { exact: false })).toBeVisible()
  await page.getByLabel('Сравнить с предыдущим периодом').check()
  await expect(page.getByText('Предыдущий равный интервал', { exact: false })).toBeVisible()
})

test('keeps the last successful agent data during a failed refresh, then recovers', async ({ page }) => {
  const state = await mockDashboard(page)
  await page.goto('/agents')
  await expect(page.getByRole('link', { name: ID })).toBeVisible()
  state.failReads = true
  await page.getByRole('button', { name: 'Обновить' }).click()
  await expect(page.getByText('Обновляем список, предыдущие данные остаются видимыми…')).toBeVisible()
  await expect(page.getByRole('link', { name: ID })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText('Показаны последние успешно полученные данные.', {
    timeout: 12_000
  })
  state.failReads = false
  await page.getByRole('button', { name: 'Повторить' }).click()
  await expect(page.getByRole('alert')).toHaveCount(0)
  await expect(page.getByRole('link', { name: ID })).toBeVisible()
})

test('on expired session shows login and does not leave private fleet data', async ({ page }) => {
  const state = await mockDashboard(page)
  await page.goto('/agents')
  await expect(page.getByRole('link', { name: ID })).toBeVisible()
  state.signedIn = false
  await page.getByRole('button', { name: 'Обновить' }).click()
  await expect(page.getByRole('heading', { name: 'Вход в Aegis' })).toBeVisible()
  await expect(page.getByRole('link', { name: ID })).toHaveCount(0)
})

test('mobile navigation stays usable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockDashboard(page)
  await page.goto('/')
  await expect(page.getByRole('navigation', { name: 'Главная навигация' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Обзор инфраструктуры' })).toBeVisible()
  await page.getByRole('link', { name: 'Агенты', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Агенты', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: ID })).toBeVisible()
})

test('HTTPS operator login and administrator session tools', async ({ page }) => {
  await mockDashboard(page, { remote: true, signedIn: false, role: 'admin' })
  await page.goto('/')
  await expect(page.getByLabel('Пользователь')).toBeVisible()
  await page.getByLabel('Пользователь').fill('operator')
  await page.locator('#dashboard-token').fill(TOKEN)
  await page.getByRole('button', { name: 'Войти' }).click()
  await expect(page.getByRole('heading', { name: 'Обзор инфраструктуры' })).toBeVisible()
  await page.getByRole('link', { name: 'Настройки' }).click()
  await expect(page.getByText('reader', { exact: true })).toBeVisible()
  page.once('dialog', dialog => void dialog.accept())
  await page.getByRole('row', { name: /reader viewer/ }).getByRole('button', { name: 'Отозвать все' }).click()
  await expect(page.getByText('Сессии пользователя reader отозваны: 1')).toBeVisible()
})

test('viewer has no administrator controls', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/settings')
  await expect(page.getByText('управление пользователями доступно только администратору', { exact: false })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Отозвать все' })).toHaveCount(0)
})

test('threshold rules page shows real API states and admin evaluation', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'admin' })
  await page.goto('/rules')
  await expect(page.getByRole('heading', { name: 'Пороговые правила' })).toBeVisible()
  await page.getByLabel('UUID компьютера').fill(ID)
  await page.getByRole('button', { name: 'Открыть правила' }).click()
  await expect(page.getByText('CPU high')).toBeVisible()
  await page.getByRole('button', { name: 'Проверить сейчас' }).click()
  await expect(page.getByText('Порог сработал')).toBeVisible()
  await expect(page.getByLabel('Создать пороговое правило')).toBeVisible()
})

test('viewer can read rules but cannot change or evaluate them', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/rules')
  await page.getByLabel('UUID компьютера').fill(ID)
  await page.getByRole('button', { name: 'Открыть правила' }).click()
  await expect(page.getByText('CPU high')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Проверить сейчас' })).toHaveCount(0)
  await expect(page.getByLabel('Создать пороговое правило')).toHaveCount(0)
})

test('heartbeat alone shows agent online without fabricated measurements', async ({ page }) => {
  await mockDashboard(page)
  await page.goto('/agents')
  await expect(page.getByRole('link', { name: ID })).toBeVisible()
  const row = page.getByRole('row', { name: new RegExp(ID) })
  await expect(row.getByText('Нет измерений')).toBeVisible()
  await expect(row.getByText('Online')).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Heartbeat' })).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Телеметрия' })).toBeVisible()
})

test('incidents page shows active incidents and operator actions', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'admin' })
  await page.goto('/incidents')
  await expect(page.getByRole('heading', { name: 'Инциденты', exact: true })).toBeVisible()
  await expect(page.getByText('CPU high')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Подтвердить' })).toBeVisible()
  await page.getByRole('button', { name: 'Подтвердить' }).click()
  await expect(page.getByText('Инцидент подтверждён.')).toBeVisible()
  await page.getByRole('button', { name: 'История' }).click()
  await expect(page.getByText('Открытие', { exact: false })).toBeVisible()
  await page.getByLabel('UUID агента (необязательно)').fill(ID)
  await page.getByRole('button', { name: 'Проверить связь' }).click()
  await expect(page.getByText('Доступность проверена', { exact: false })).toBeVisible()
})

test('viewer can inspect incidents but has no write controls', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/incidents')
  await expect(page.getByText('CPU high')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Подтвердить' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Разрешить' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Проверить связь' })).toHaveCount(0)
  await page.getByRole('button', { name: 'История' }).click()
  await expect(page.getByText('Открытие', { exact: false })).toBeVisible()
})

test('admin edits persistent incident policy and reads incident severity', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'admin' })
  await page.goto('/incidents')
  await expect(page.getByLabel('Политика инцидентов')).toBeVisible()
  await expect(page.getByText('Приоритет: warning')).toBeVisible()
  await page.getByLabel('Cooldown (сек., 0 — отключён)').fill('120')
  await page.getByLabel('Эскалация после (сек.)').fill('600')
  await page.getByRole('button', { name: 'Сохранить политику' }).click()
  await expect(page.getByText('Политика сохранена в PostgreSQL.')).toBeVisible()
})

test('viewer sees policy values but cannot edit the incident policy', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/incidents')
  await expect(page.getByLabel('Cooldown (сек., 0 — отключён)')).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Сохранить политику' })).toHaveCount(0)
})

test('incident page displays aggregate delivery stats without secret data', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/incidents')
  await expect(page.getByLabel('Доставка уведомлений')).toBeVisible()
  await expect(page.getByText('Ожидают: 3', { exact: false })).toBeVisible()
})

test('journal is searchable and accessible to viewers without mutation controls', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/activity')
  await expect(page.getByRole('heading', { name: 'Журнал событий' })).toBeVisible()
  await expect(page.getByText('CPU high')).toBeVisible()
  await expect(page.getByRole('cell', { name: 'Открытие' })).toBeVisible()
  await page.getByLabel('Категория').selectOption('incident')
  await page.getByLabel('Оператор или система').fill('operator')
  await page.getByRole('button', { name: 'Искать' }).click()
  await expect(page.getByText('CPU high')).toBeVisible()
  await page.getByText('Подробности').click()
  await expect(page.getByText('threshold', { exact: false })).toBeVisible()
})
test('journal validates UUID and search length before API request', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'admin' })
  await page.goto('/activity')
  await page.getByLabel('UUID агента').fill('wrong-id')
  await page.getByRole('button', { name: 'Искать' }).click()
  await expect(page.getByRole('alert')).toContainText('UUID')
  await page.getByLabel('UUID агента').fill('')
  await page.getByLabel('Поиск по названию').fill('x')
  await page.getByRole('button', { name: 'Искать' }).click()
  await expect(page.getByRole('alert')).toContainText('2–80')
})

test('incidents view shows latest automatic monitoring health', async ({ page }) => {
  await mockDashboard(page, { remote: true, role: 'viewer' })
  await page.goto('/incidents')
  const status = page.getByLabel('Автоматические проверки')
  await expect(status).toContainText('Последняя проверка успешна')
  await expect(status).toContainText('Проверено агентов: 5')
  await expect(status).toContainText('правил: 8')
  await expect(status).toContainText('Интервал: 30 сек.')
})
