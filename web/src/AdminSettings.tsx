import { useEffect, useState } from 'react'
import {
  ApiError, loadDashboardUsers, revokeDashboardSessions,
  type DashboardUser, type DashboardSession
} from './api/client'

type Props = {
  session: DashboardSession
  onExpired: () => void
}

export default function AdminSettings({ session, onExpired }: Props) {
  const [users, setUsers] = useState<DashboardUser[]>([])
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [info, setInfo] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (session.mode !== 'remote' || session.role !== 'admin') {
      setLoading(false)
      return
    }
    const controller = new AbortController()
    loadDashboardUsers(controller.signal).then(data => {
      if (!controller.signal.aborted) setUsers(data)
    }).catch((cause: unknown) => {
      if (controller.signal.aborted) return
      if (cause instanceof ApiError && cause.status === 401) onExpired()
      else setError('Не удалось загрузить список учётных записей.')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [session.mode, session.role, onExpired])

  async function revoke(username: string) {
    if (!window.confirm(`Отозвать все активные сессии пользователя ${username}?`)) return
    setBusy(username)
    setError('')
    setInfo('')
    try {
      const count = await revokeDashboardSessions(username)
      setInfo(`Сессии пользователя ${username} отозваны: ${count}`)
      if (username === session.username) onExpired()
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) onExpired()
      else setError('Не удалось отозвать сессии. Попробуйте ещё раз.')
    } finally {
      setBusy('')
    }
  }

  return <div className="settings-section">
    <div className="intro"><p className="eyebrow">БЕЗОПАСНОСТЬ</p><h1>Настройки доступа</h1>
      <p className="muted">Учётные записи и активные сессии Go-сервера.</p>
    </div>
    <section className="panel" aria-label="Управление доступом">
      <div className="panel-heading"><h2>Управление доступом</h2></div>
      {session.mode === 'local' ? <p className="muted">Для управления пользователями запустите удалённый HTTPS-режим. Локальный режим остаётся однопользовательским.</p> :
        <>
          <p>Вы вошли как <strong>{session.username}</strong> (роль: <strong>{session.role}</strong>).</p>
          {session.role !== 'admin' ? <p className="muted">Просмотр метрик разрешён, управление пользователями доступно только администратору.</p> :
            <>
              <p className="muted small">Добавление, смена роли и пароля выполняются на сервере через aegis dashboard-user. После изменения файла учётных записей перезапустите панель: это также завершит все прежние сессии.</p>
              {loading ? <p role="status">Загружаем пользователей…</p> :
                <div className="agent-table-wrap"><table className="agent-table">
                  <thead><tr><th>Пользователь</th><th>Роль</th><th>Сессии</th></tr></thead>
                  <tbody>{users.map(user => <tr key={user.username}>
                    <td className="mono">{user.username}</td>
                    <td>{user.role}</td>
                    <td><button type="button" disabled={busy !== ''}
                      onClick={() => void revoke(user.username)}>
                      {busy === user.username ? 'Отзываем…' : 'Отозвать все'}
                    </button></td>
                  </tr>)}</tbody>
                </table></div>}
              {error && <p role="alert" className="form-error">{error}</p>}
              {info && <p role="status">{info}</p>}
            </>}
        </>}
    </section>
  </div>
}
