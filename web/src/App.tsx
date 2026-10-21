import { useCallback, useEffect, useState, type FormEvent, type MouseEvent, type ReactNode } from 'react'
import AgentDetail from './AgentDetail'
import AdminSettings from './AdminSettings'
import type { AgentInfo, AgentPage, AgentStatus } from './api/types'
import {
  ApiError, checkSession, signIn, signOut, loadAgents, loadSummary,
  type AgentSummary, type DashboardSession
} from './api/client'

type Page = 'overview' | 'agents' | 'agent-detail' | 'incidents' | 'settings' | 'not-found'
type AuthState = 'checking' | 'authenticated' | 'anonymous' | 'unavailable'

const navigation = [
  { path: '/', label: 'Обзор', symbol: '◉' },
  { path: '/agents', label: 'Агенты', symbol: '▦' },
  { path: '/incidents', label: 'Инциденты', symbol: '◇', future: true },
  { path: '/settings', label: 'Настройки', symbol: '⚙' }
]

function route(path: string): Page {
  if (path === '/') return 'overview'
  if (path === '/agents') return 'agents'
  if (/^\/agents\/[0-9a-fA-F-]{36}$/.test(path)) return 'agent-detail'
  if (path === '/incidents') return 'incidents'
  if (path === '/settings') return 'settings'
  return 'not-found'
}

function Panel({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return <section className="panel" aria-label={title}>
    <div className="panel-heading"><h2>{title}</h2>{note && <span className="muted small">{note}</span>}</div>
    {children}
  </section>
}
function Empty({ title, text }: { title: string; text: string }) {
  return <div className="empty-state">
    <span className="empty-glyph" aria-hidden="true">⌁</span>
    <h3>{title}</h3><p>{text}</p>
  </div>
}
function timeLabel(input: string | null): ReactNode {
  if (input === null) return 'Ни одного контакта'
  const time = new Date(input)
  if (Number.isNaN(time.getTime())) return 'Неизвестное время'
  return <time title={input} dateTime={input}>{new Intl.DateTimeFormat('ru-RU', {
    dateStyle: 'medium', timeStyle: 'short'
  }).format(time)}</time>
}
function Status({ value }: { value: AgentStatus }) {
  const labels: Record<AgentStatus, string> = {
    online: 'Online', offline: 'Offline', unknown: 'Unknown'
  }
  return <span className={`status-pill status-${value}`}><span aria-hidden="true">●</span> {labels[value]}</span>
}
function Stats({ summary, loading }: { summary: AgentSummary | null; loading: boolean }) {
  const stats = [
    { key: 'total', title: 'Всего агентов', note: 'Зарегистрировано' },
    { key: 'online', title: 'Online', note: 'Недавний контакт' },
    { key: 'offline', title: 'Offline', note: 'Контакт устарел' },
    { key: 'unknown', title: 'Unknown', note: 'Контактов ещё не было' }
  ] as const
  return <div className="stats-grid">
    {stats.map(({ key, title, note }) => <section className="stat-card" key={key} aria-label={title}>
      <span className="muted stat-label">{title}</span>
      <strong className="stat-value">{summary ? summary[key].toLocaleString('ru-RU') : '—'}</strong>
      <span className="muted small">{loading ? 'Загружаем…' : note}</span>
    </section>)}
  </div>
}
function AgentRows({ agents }: { agents: AgentInfo[] }) {
  return <div className="agent-table-wrap"><table className="agent-table">
    <thead><tr><th>Агент</th><th>Состояние</th><th>Последний контакт</th><th>Система</th></tr></thead>
    <tbody>{agents.map(agent => <tr key={agent.agent_id}>
      <td><a className="agent-link mono" href={`/agents/${agent.agent_id}`}>{agent.agent_id}</a></td>
      <td><Status value={agent.status} /></td>
      <td>{timeLabel(agent.last_contact_at)}</td>
      <td>{agent.os ? `${agent.os} · ${agent.arch ?? '—'}` : '—'}</td>
    </tr>)}</tbody>
  </table></div>
}
function Login({ onLogin, busy, error, mode }: {
  onLogin: (username: string, token: string) => Promise<void>; busy: boolean; error: string
  mode: 'local' | 'remote'
}) {
  const [username, setUsername] = useState('')
  const [token, setToken] = useState('')
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const value = token
    setToken('')
    await onLogin(username, value)
  }
  return <main className="login-page">
    <div className="login-card">
      <div className="brand-name">AEGIS<span className="brand-dot">.</span></div>
      <p className="eyebrow">ЛОКАЛЬНАЯ ПАНЕЛЬ МОНИТОРИНГА</p>
      <h1>Вход в Aegis</h1>
      <p className="muted">{mode === 'remote' ? 'Введите имя пользователя и пароль. Пароль не сохраняется в браузере.' :
          'Введите отдельный токен панели, настроенный на Go-сервере. Он используется только для входа и не сохраняется браузером.'}</p>
      <form onSubmit={submit} className="login-form">
        {mode === 'remote' && <><label htmlFor="dashboard-username">Пользователь</label>
          <input id="dashboard-username" type="text" autoComplete="username" required
            minLength={3} maxLength={32} value={username} onChange={e => setUsername(e.target.value)} /></>}
        <label htmlFor="dashboard-token">{mode === 'remote' ? 'Пароль' : 'Токен панели'}</label>
        <input id="dashboard-token" type="password" autoComplete={mode === 'remote' ? 'current-password' : 'off'} value={token}
          onChange={e => setToken(e.target.value)} minLength={mode === 'remote' ? 16 : 32} maxLength={4096}
          required aria-invalid={error ? true : undefined} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="primary-button" type="submit" disabled={busy}>
          {busy ? 'Проверяем…' : 'Войти'}
        </button>
      </form>
      <p className="muted small">{mode === 'remote' ? 'HTTPS · защищённая серверная сессия' : 'Доступен только с самого компьютера сервера.'}</p>
    </div>
  </main>
}

export default function App() {
  const [path, setPath] = useState(() => window.location.pathname)
  const page = route(path)
  const [auth, setAuth] = useState<AuthState>('checking')
  const [session, setSession] = useState<DashboardSession>({authenticated: false, mode: 'local'})
  const [loginBusy, setLoginBusy] = useState(false)
  const [loginError, setLoginError] = useState('')
  const [offlineAfter, setOfflineAfter] = useState('1m')
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const [refresh, setRefresh] = useState(0)
  const [summary, setSummary] = useState<AgentSummary | null>(null)
  const [agents, setAgents] = useState<AgentPage | null>(null)
  const [loading, setLoading] = useState(false)
  const [dataError, setDataError] = useState('')
  const [loadedKey, setLoadedKey] = useState('')
  const queryKey = offlineAfter + '|' + cursorStack.join('|')
  const onExpired = useCallback(() => { setAuth('anonymous'); setAgents(null); setSummary(null); setLoadedKey('') }, [])
  const cursor = cursorStack.length ? cursorStack[cursorStack.length - 1] ?? '' : ''

  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    checkSession(controller.signal).then(info => {
      if (controller.signal.aborted) return
      setSession(info)
      setAuth(info.authenticated ? 'authenticated' : 'anonymous')
    })
      .catch(() => { if (!controller.signal.aborted) setAuth('unavailable') })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (auth !== 'authenticated') return
    const controller = new AbortController()
    setLoading(true)
    setDataError('')
    Promise.all([
      loadSummary(offlineAfter, controller.signal),
      loadAgents(offlineAfter, cursor, controller.signal)
    ]).then(([nextSummary, nextAgents]) => {
      if (controller.signal.aborted) return
      setSummary(nextSummary)
      setAgents(nextAgents)
      setLoadedKey(queryKey)
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return
      if (error instanceof ApiError && error.status === 401) {
        setAuth('anonymous')
        setSummary(null); setAgents(null)
      } else {
        setDataError('Не удалось загрузить данные агентов. Проверьте соединение с PostgreSQL.')
      }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [auth, offlineAfter, cursor, queryKey, refresh])

  useEffect(() => {
    if (auth !== 'authenticated') return
    const updateIfVisible = () => {
      if (document.visibilityState === 'visible') setRefresh(i => i + 1)
    }
    const interval = window.setInterval(updateIfVisible, 30_000)
    document.addEventListener('visibilitychange', updateIfVisible)
    window.addEventListener('online', updateIfVisible)
    return () => {
      window.clearInterval(interval)
      document.removeEventListener('visibilitychange', updateIfVisible)
      window.removeEventListener('online', updateIfVisible)
    }
  }, [auth])

  async function onLogin(username: string, token: string) {
    setLoginBusy(true); setLoginError('')
    try {
      await signIn(token, session.mode === 'remote' ? username : undefined)
      const next = await checkSession()
      if (!next.authenticated) throw new Error('Session was not established')
      setSession(next)
      setAuth('authenticated')
      setCursorStack([])
      setLoadedKey('')
      setRefresh(i => i + 1)
    } catch (error) {
      if (error instanceof ApiError && error.status === 429) {
        setLoginError('Слишком много попыток. Подождите минуту.')
      } else {
        setLoginError('Не удалось войти. Проверьте токен и локальный Go-сервер.')
      }
    } finally {
      setLoginBusy(false)
    }
  }

  async function onLogout() {
    try { await signOut() } finally {
      setAuth('anonymous')
      setAgents(null); setSummary(null); setCursorStack([]); setLoadedKey('')
    }
  }
  function navigate(e: MouseEvent<HTMLAnchorElement>, href: string) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    e.preventDefault()
    window.history.pushState({}, '', href)
    setPath(href)
    window.scrollTo(0, 0)
  }

  if (auth === 'checking' || auth === 'unavailable') {
    return <div className="login-page"><div className="login-card">
      <h1>AEGIS.</h1>
      <p role="status">{auth === 'checking' ? 'Проверяем сессию…' :
        'Нет соединения с Go Dashboard. Соберите web и запустите aegis dashboard на 127.0.0.1:8082.'}</p>
      {auth === 'unavailable' && <button className="primary-button" onClick={() => window.location.reload()}>Повторить</button>}
    </div></div>
  }
  if (auth === 'anonymous') return <Login mode={session.mode} onLogin={onLogin} busy={loginBusy} error={loginError} />

  const currentPage = loadedKey === queryKey ? agents : null
  const currentSummary = loadedKey === queryKey ? summary : null
  const listing = currentPage?.items ?? []
  const title = page === 'agents' ? 'Агенты' : page === 'agent-detail' ? 'Карточка агента' :
    page === 'incidents' ? 'Инциденты' : page === 'settings' ? 'Настройки' : 'Обзор'

  function listPanel() {
    return <Panel title="Зарегистрированные агенты" note="Данные PostgreSQL · обновление каждые 30 секунд">
      {dataError && <div className="data-error" role="alert">
        {dataError} {currentPage && 'Показаны последние успешно полученные данные.'}
        <button type="button" onClick={() => setRefresh(i => i + 1)}
          disabled={loading}>Повторить</button>
      </div>}
      {loading && currentPage && <p className="refresh-hint" role="status">
        Обновляем список, предыдущие данные остаются видимыми…
      </p>}
      {loading && !currentPage ? <Empty title="Получаем список…" text="Проверяем зарегистрированные устройства." /> :
        listing.length ? <AgentRows agents={listing} /> :
        <Empty title={dataError ? 'Данные недоступны' : 'Нет зарегистрированных агентов'}
          text={dataError ? 'Используйте кнопку обновления для повтора.' :
            'Добавьте агента в реестр и перезапустите receive для синхронизации с PostgreSQL.'} />}
      <div className="table-controls">
        <button type="button" disabled={loading || cursorStack.length === 0}
          onClick={() => setCursorStack(a => a.slice(0, -1))}>← Назад</button>
        <span className="muted small">Страница {cursorStack.length + 1}</span>
        <button type="button" disabled={loading || !currentPage?.has_more || !currentPage.next_cursor}
          onClick={() => { if (currentPage?.next_cursor) setCursorStack(a => [...a, currentPage.next_cursor!]) }}>Далее →</button>
      </div>
    </Panel>
  }

  return <div className="app-layout">
    <aside className="sidebar">
      <a href="/" className="brand" aria-label="Aegis — обзор" onClick={e => navigate(e, '/')}>
        <span className="brand-mark" aria-hidden="true">A</span>
        <span className="brand-name">AEGIS<span className="brand-dot">.</span><small>MONITORING</small></span>
      </a>
      <p className="nav-label">РАБОЧЕЕ ПРОСТРАНСТВО</p>
      <nav className="navigation" aria-label="Главная навигация">
        {navigation.map(item => {
          const active = item.path === '/' ? path === '/' : item.path === '/agents'
            ? path.startsWith('/agents') : path === item.path
          return <a href={item.path} key={item.path} className={active ? 'nav-link active' : 'nav-link'}
            aria-current={active ? 'page' : undefined} onClick={e => navigate(e, item.path)}>
            <span className="nav-symbol" aria-hidden="true">{item.symbol}</span>
            <span className="nav-text">{item.label}</span>
            {item.future && <span className="nav-future">скоро</span>}
          </a>
        })}
      </nav>
      <div className="sidebar-footer"><div><strong>{session.mode === 'remote' ? 'HTTPS-подключение' : 'Локальный доступ'}</strong><span>{session.username ? `${session.username} · ${session.role}` : 'Защищённая сессия'} · 4.6</span></div></div>
    </aside>
    <div className="main-layout">
      <header className="topbar">
        <div className="breadcrumb"><span>AEGIS</span><span aria-hidden="true">/</span><strong>{title}</strong></div>
        <div className="toolbar">
          <span className="environment-pill"><span aria-hidden="true">●</span> Локальный сервер</span>
          <button onClick={() => setRefresh(i => i + 1)} disabled={loading}>Обновить</button>
          <button onClick={() => void onLogout()}>Выход</button>
        </div>
      </header>
      <main id="main-content" className="content">
        {(page === 'overview' || page === 'agents') && <>
          <div className="intro">
            <p className="eyebrow">МОНИТОРИНГ ИНФРАСТРУКТУРЫ</p>
            <h1>{page === 'overview' ? 'Обзор инфраструктуры' : 'Агенты'}</h1>
            <p className="muted">Состояние определяется последним подтверждённым контактом, а не проверкой питания компьютера.</p>
          </div>
          <div className="filter-bar">
            <label htmlFor="offline-threshold">Считать Offline через</label>
            <select id="offline-threshold" value={offlineAfter} onChange={e => {
              setOfflineAfter(e.target.value)
              setCursorStack([])
              setDataError('')
            }}>
              <option value="1m">1 минуту</option>
              <option value="5m">5 минут</option>
              <option value="15m">15 минут</option>
              <option value="1h">1 час</option>
            </select>
            {currentSummary?.checked_at && <span className="muted small">Обновлено: {timeLabel(currentSummary.checked_at)}</span>}
          </div>
          <Stats summary={currentSummary} loading={loading} />
          {listPanel()}
        </>}
        {page === 'agent-detail' && <AgentDetail
          id={path.slice('/agents/'.length)} refresh={refresh} onExpired={onExpired} />}
        {page === 'settings' && <AdminSettings session={session} onExpired={onExpired} />}
        {page === 'incidents' && <Panel title={title} note="Будущий этап">
          <Empty title="В разработке" text="Этот раздел появится на следующем этапе Aegis." />
        </Panel>}
        {page === 'not-found' && <Panel title="Страница не найдена">
          <Empty title="Неизвестный адрес" text="Выберите раздел в навигации." />
        </Panel>}
      </main>
      <footer className="footer">
        <span>Aegis · Apache-2.0</span>
        <span>Разработчик: <a href="https://github.com/DrDowellsHead" target="_blank" rel="noopener noreferrer">DrDowellsHead</a> · <a href="https://github.com/DrDowellsHead/Aegis/issues/new" target="_blank" rel="noopener noreferrer">Предложить сотрудничество</a></span>
      </footer>
    </div>
  </div>
}
