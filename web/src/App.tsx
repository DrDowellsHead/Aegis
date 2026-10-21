import { useEffect, useState, type ReactNode, type MouseEvent } from 'react'

type Page = 'overview' | 'agents' | 'agent-detail' | 'incidents' | 'settings' | 'not-found'

type NavItem = {
  path: string
  text: string
  description: string
  symbol: string
  upcoming?: boolean
}

const navigation: NavItem[] = [
  { path: '/', text: 'Обзор', description: 'Состояние инфраструктуры', symbol: '◉' },
  { path: '/agents', text: 'Агенты', description: 'Устройства и метрики', symbol: '▦' },
  { path: '/incidents', text: 'Инциденты', description: 'Будущий этап', symbol: '◇', upcoming: true },
  { path: '/settings', text: 'Настройки', description: 'Будущий этап', symbol: '⚙', upcoming: true }
]

function readRoute(path: string): Page {
  if (path === '/') return 'overview'
  if (path === '/agents') return 'agents'
  if (/^\/agents\/[0-9a-fA-F-]{36}$/.test(path)) return 'agent-detail'
  if (path === '/incidents') return 'incidents'
  if (path === '/settings') return 'settings'
  return 'not-found'
}

function routeTitle(page: Page): string {
  switch (page) {
    case 'overview': return 'Обзор инфраструктуры'
    case 'agents': return 'Агенты'
    case 'agent-detail': return 'Карточка агента'
    case 'incidents': return 'Инциденты'
    case 'settings': return 'Настройки'
    case 'not-found': return 'Страница не найдена'
  }
}

function Card({ title, subtitle, children }: {
  title: string
  subtitle?: string
  children: ReactNode
}) {
  return (
    <section className="panel" aria-label={title}>
      <div className="panel-heading">
        <h2>{title}</h2>
        {subtitle && <span className="muted small">{subtitle}</span>}
      </div>
      {children}
    </section>
  )
}

function EmptyState({ title, description }: { title: string; description: string }) {
  return (
    <div className="empty-state">
      <div className="empty-glyph" aria-hidden="true">⌁</div>
      <h3>{title}</h3>
      <p>{description}</p>
    </div>
  )
}

function StatCard({ label, hint }: { label: string; hint: string }) {
  return (
    <section className="stat-card" aria-label={label}>
      <span className="muted stat-label">{label}</span>
      <strong className="stat-value" aria-label="Значение пока неизвестно">—</strong>
      <span className="muted small">{hint}</span>
    </section>
  )
}

function Overview() {
  return (
    <>
      <div className="intro">
        <p className="eyebrow">МОНИТОРИНГ ИНФРАСТРУКТУРЫ</p>
        <h1>Обзор инфраструктуры</h1>
        <p className="muted">Единая панель для устройств, состояния агентов и истории производительности.</p>
      </div>
      <div className="notice" role="status">
        <span className="notice-indicator" aria-hidden="true" />
        <div>
          <strong>Данные ещё не подключены</strong>
          <p>Это архитектурный каркас этапа 4.1. Ни одна цифра здесь не подменяется демонстрационными значениями.</p>
        </div>
      </div>
      <div className="stats-grid">
        <StatCard label="Всего агентов" hint="Ожидаем данные реестра" />
        <StatCard label="Online" hint="Недавний подтверждённый контакт" />
        <StatCard label="Offline" hint="Нет контакта в пределах порога" />
        <StatCard label="Unknown" hint="Контактов пока не было" />
      </div>
      <Card title="Активность агентов" subtitle="Список и фильтрация — этап 4.2">
        <EmptyState title="Нет подключённого источника"
          description="После реализации защищённого серверного шлюза здесь появятся зарегистрированные агенты, их статусы и время последнего контакта." />
      </Card>
    </>
  )
}

function Agents() {
  return (
    <>
      <div className="intro">
        <p className="eyebrow">УСТРОЙСТВА</p>
        <h1>Агенты</h1>
        <p className="muted">Список компьютеров под наблюдением и момент последнего контакта с сервером.</p>
      </div>
      <Card title="Реестр агентов" subtitle="GET /api/v1/agents">
        <div className="table-header" aria-hidden="true">
          <span>Агент</span><span>Состояние</span><span>Последний контакт</span>
        </div>
        <EmptyState title="Реестр ещё не загружен"
          description="На этапе 4.2 появится постраничная загрузка агентов. Статус offline обозначает отсутствие недавнего контакта, а не выключенный компьютер." />
      </Card>
    </>
  )
}

function AgentDetail({ path }: { path: string }) {
  const id = path.slice('/agents/'.length)
  return (
    <>
      <div className="intro">
        <p className="eyebrow">АГЕНТ / {id.slice(0, 8)}</p>
        <h1>История устройства</h1>
        <p className="muted mono long-id">{id}</p>
      </div>
      <div className="stats-grid">
        <StatCard label="CPU" hint="Средняя загрузка" />
        <StatCard label="RAM" hint="Использовано памяти" />
        <StatCard label="Диск" hint="Требуется выбор пути" />
        <StatCard label="Последний контакт" hint="Время сервера" />
      </div>
      <div className="two-columns">
        <Card title="Временные ряды" subtitle="GET /api/v1/series">
          <EmptyState title="Графики пока не доступны"
            description="После подключения безопасного API здесь будут графики CPU, RAM и выбранной файловой системы. Пропуски не станут нулевыми значениями." />
        </Card>
        <Card title="История измерений" subtitle="GET /api/v1/measurements">
          <EmptyState title="Нет загруженных измерений"
            description="Здесь появится таблица отдельных замеров, частичных ошибок и курсорная пагинация." />
        </Card>
      </div>
    </>
  )
}

function Planned({ kind }: { kind: 'incidents' | 'settings' }) {
  const incidents = kind === 'incidents'
  return (
    <>
      <div className="intro">
        <p className="eyebrow">В ПЛАНЕ РАЗРАБОТКИ</p>
        <h1>{incidents ? 'Инциденты' : 'Настройки'}</h1>
        <p className="muted">{incidents
          ? 'Правила мониторинга, подтверждение инцидентов и уведомления появятся в следующей фазе.'
          : 'Безопасные сессии администратора и настройка интерфейса появятся позднее.'}</p>
      </div>
      <Card title={incidents ? 'Журнал инцидентов' : 'Администрирование'}>
        <EmptyState title="Раздел запланирован"
          description={incidents
            ? 'Этот раздел ещё не подключён и не генерирует оповещения.'
            : 'Серверные токены нельзя размещать в браузере. Сначала необходим защищённый Go gateway.'} />
      </Card>
    </>
  )
}

export default function App() {
  const [path, setPath] = useState(() => window.location.pathname)
  const page = readRoute(path)

  useEffect(() => {
    const handlePopState = () => setPath(window.location.pathname)
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  function navigate(event: MouseEvent<HTMLAnchorElement>, href: string) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    window.history.pushState({}, '', href)
    setPath(href)
    window.scrollTo(0, 0)
  }

  return (
    <div className="app-layout">
      <aside className="sidebar">
        <a href="/" className="brand" aria-label="Aegis — на главную" onClick={(e) => navigate(e, '/')}>
          <span className="brand-mark" aria-hidden="true">A</span>
          <span className="brand-name">AEGIS<span className="brand-dot">.</span><small>MONITORING</small></span>
        </a>
        <div className="nav-label">РАБОЧЕЕ ПРОСТРАНСТВО</div>
        <nav className="navigation" aria-label="Главная навигация">
          {navigation.map((item) => {
            const active = item.path === '/'
              ? page === 'overview'
              : item.path === '/agents'
                ? page === 'agents' || page === 'agent-detail'
                : path === item.path
            return (
              <a href={item.path} key={item.path}
                className={active ? 'nav-link active' : 'nav-link'}
                aria-current={active ? 'page' : undefined}
                onClick={(e) => navigate(e, item.path)}>
                <span className="nav-symbol" aria-hidden="true">{item.symbol}</span>
                <span className="nav-text">{item.text}</span>
                {item.upcoming && <span className="nav-future">скоро</span>}
              </a>
            )
          })}
        </nav>
        <div className="sidebar-footer">
          <span className="side-indicator" aria-hidden="true" />
          <div><strong>Режим проектирования</strong><span>Этап 4.1 · Только интерфейс</span></div>
        </div>
      </aside>

      <div className="main-layout">
        <header className="topbar">
          <div className="breadcrumb"><span>AEGIS</span><span aria-hidden="true">/</span><strong>{routeTitle(page)}</strong></div>
          <span className="environment-pill"><span aria-hidden="true">●</span> Нет подключения</span>
        </header>
        <main id="main-content" className="content" tabIndex={-1}>
          {page === 'overview' && <Overview />}
          {page === 'agents' && <Agents />}
          {page === 'agent-detail' && <AgentDetail path={path} />}
          {page === 'incidents' && <Planned kind="incidents" />}
          {page === 'settings' && <Planned kind="settings" />}
          {page === 'not-found' && (
            <Card title="Неизвестный адрес">
              <EmptyState title="Страница не найдена" description="Выберите раздел в навигации." />
            </Card>
          )}
        </main>
        <footer className="footer">
          <span>Aegis Dashboard · Архитектурный прототип 4.1</span>
          <span>Без подключений к PostgreSQL и истории</span>
        </footer>
      </div>
    </div>
  )
}
