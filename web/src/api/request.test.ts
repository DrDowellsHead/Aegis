import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, requestJSON } from './request'

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('bounded, cancellable dashboard reads', () => {
  it('retries a transient 503 and succeeds without exceeding the budget', async () => {
    vi.useFakeTimers()
    const fetcher = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response('{}', { status: 503 }))
      .mockResolvedValueOnce(new Response('{"ok":true}', { status: 200 }))
    const result = requestJSON('/api/web/agents')
    await vi.runAllTimersAsync()
    await expect(result).resolves.toEqual({ ok: true })
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('never retries a 401 or unsafe POST', async () => {
    vi.useFakeTimers()
    const fetcher = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response('{}', { status: 401 }))
    await expect(requestJSON('/api/web/agents')).rejects.toMatchObject({ status: 401 })
    expect(fetcher).toHaveBeenCalledTimes(1)

    fetcher.mockResolvedValueOnce(new Response('{}', { status: 503 }))
    await expect(requestJSON('/api/web/login', { method: 'POST' }))
      .rejects.toBeInstanceOf(ApiError)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('stops pending backoff immediately on navigation abort', async () => {
    vi.useFakeTimers()
    const fetcher = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{}', { status: 503 }))
    const controller = new AbortController()
    const task = requestJSON('/api/web/agents', { signal: controller.signal })
    await vi.advanceTimersByTimeAsync(0)
    controller.abort()
    await expect(task).rejects.toMatchObject({ name: 'AbortError' })
    await vi.runAllTimersAsync()
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('stops after at most three GET attempts', async () => {
    vi.useFakeTimers()
    const fetcher = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{}', { status: 503 }))
    const task = requestJSON('/api/web/summary')
    const rejected = expect(task).rejects.toMatchObject({ status: 503 })
    await vi.runAllTimersAsync()
    await rejected
    expect(fetcher).toHaveBeenCalledTimes(3)
  })
})
