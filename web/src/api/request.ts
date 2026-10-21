/**
 * Browser read retry policy. Only idempotent GET requests are retried.
 * 401, 403, 400 and malformed JSON are terminal; never resend login POST.
 */
export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

export class RequestTimeoutError extends Error {
  constructor() {
    super('The gateway did not respond before the request deadline')
    this.name = 'RequestTimeoutError'
  }
}

const TRANSIENT = new Set([408, 429, 500, 502, 503, 504])

export function isTemporaryError(error: unknown): boolean {
  return error instanceof RequestTimeoutError ||
    error instanceof TypeError ||
    error instanceof ApiError && TRANSIENT.has(error.status)
}

function aborted(): Error {
  return new DOMException('Request cancelled', 'AbortError')
}

async function pause(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) throw aborted()
  await new Promise<void>((resolve, reject) => {
    const done = () => {
      clearTimeout(timer)
      signal?.removeEventListener('abort', onAbort)
    }
    const onAbort = () => { done(); reject(aborted()) }
    const timer = setTimeout(() => { done(); resolve() }, ms)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

async function attempt(path: string, init: RequestInit, timeoutMS: number): Promise<unknown> {
  const controller = new AbortController()
  const onAbort = () => controller.abort()
  if (init.signal?.aborted) throw aborted()
  init.signal?.addEventListener('abort', onAbort, { once: true })
  let timedOut = false
  const deadline = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMS)
  try {
    const response = await fetch(path, {
      credentials: 'same-origin',
      cache: 'no-store',
      ...init,
      signal: controller.signal
    })
    if (!response.ok) throw new ApiError(response.status, 'Gateway request failed')
    // JSON parsing is part of the deadline and must not hang indefinitely.
    return await response.json() as unknown
  } catch (error) {
    if (init.signal?.aborted) throw aborted()
    if (timedOut) throw new RequestTimeoutError()
    throw error
  } finally {
    clearTimeout(deadline)
    init.signal?.removeEventListener('abort', onAbort)
  }
}

export async function requestJSON(path: string, init: RequestInit = {}): Promise<unknown> {
  const method = (init.method ?? 'GET').toUpperCase()
  const retries = method === 'GET' ? 2 : 0
  for (let number = 0; ; number++) {
    if (init.signal?.aborted) throw aborted()
    try {
      return await attempt(path, init, 8_000)
    } catch (error) {
      if (init.signal?.aborted) throw aborted()
      if (number >= retries || !isTemporaryError(error)) throw error
      // Bounded exponential backoff: 350ms, 700ms. An aborted request
      // cancels the waiting retry too.
      await pause(350 * 2 ** number, init.signal ?? undefined)
    }
  }
}
