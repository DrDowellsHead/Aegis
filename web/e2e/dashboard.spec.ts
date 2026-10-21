import { test, expect, type Page } from '@playwright/test'

const ID = '123e4567-e89b-42d3-a456-426614174000'
const TOKEN = 'dashboard-test-secret-0123456789012345'

type MockOptions = {
  signedIn?: boolean
  offline?: boolean
  failReads?: boolean
}
async function mockDashboard(page: Page, options: MockOptions = {}) {
  const state = {
    signedIn: options.signedIn ?? true,
    offline: options.offline ?? false,
    failReads: options.failReads ?? false,
    listRequests: 0
  }
  await page.route('**/api/web/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const name = url.pathname
    const respond = async (status: number, data: unknown) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    if (name === '/api/web/session') {
      await respond(200, { authenticated: state.signedIn }); return
    }
    if (name === '/api/web/login') {
      const body = request.postDataJSON() as { token?: string }
      if (body.token !== TOKEN) { await respond(401, { error: 'unauthorized' }); return }
      state.signedIn = true
      await respond(200, { authenticated: true }); return
    }
    if (name === '/api/web/logout') {
      state.signedIn = false
      await respond(200, { authenticated: false }); return
    }
    if (!state.signedIn) { await respond(401, { error: 'unauthorized' }); return }
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
          last_sample_at: '2026-10-21T06:00:00Z',
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
  await page.getByLabel('Период').selectOption('custom')
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
