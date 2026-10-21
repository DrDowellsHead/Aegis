# Docker Compose: серверная часть Aegis — фаза 7.3

Контейнерный стек предназначен для **одного доверенного Linux VPS**.
Он не заменяет агенты Windows/macOS/Linux: агенты устанавливаются отдельно
и подключаются к gRPC-порту сервера. Фаза 7.3 даёт воспроизводимую сборку,
схему сети, постоянный том PostgreSQL и изолированные процессы. Это не
доказательство готовности VPS к production или успешного восстановления
после физического отказа — данные проверки остаются в фазе 7.8.

## Архитектура

```text
Агенты ──TLS/Bearer──► host:9090 ──► receiver ──┐
                                                │
                                 private backend network
                                                │
                             migrate ──► PostgreSQL 17 (volume)
                                                ▲
                            watch ──────────────┤
                            notify ─────────────┤ (optional profile)
                            dashboard ──────────┘ (optional HTTPS profile)
                                   ▲
                    host 127.0.0.1:8443 / local TLS reverse proxy
```

| Компонент | Режим | Сетевой доступ |
| --- | --- | --- |
| `postgres` | обязательный | только внутренняя сеть `backend`; без host-портов |
| `migrate` | одноразовый обязательный | `backend`, завершается с кодом 0 |
| `receiver` | обязательный | `backend` + `edge`; внешние агенты по TLS/gRPC |
| `watch` | обязательный | только `backend`; периодическая оценка правил |
| `notify` | профиль `notifications` | `backend` + `outbound`; Telegram |
| `dashboard` | профиль `dashboard` | `backend` + `edge`; HTTPS/RBAC |

Нет открытого PostgreSQL-порта, нет Docker socket в контейнерах.
Сеть `backend` использует `internal: true`. Процесс Aegis работает
с UID/GID 10001 и без Linux capabilities, с read-only корневой файловой
системой и ограничением PID. **PostgreSQL** использует штатный entrypoint
официального образа; БД хранится в Docker named volume `aegis_postgres_data`.
Права к данным определяет пользователь PostgreSQL внутри контейнера.

## Первоначальная подготовка VPS

Понадобятся Docker Engine, современный Docker Compose v2, доступ к
публичному DNS/TLS, `openssl` и shell. Выполняйте команды из директории
`deploy/compose`. Никаких секретов в `.env`: он предназначен только
для сетевых адресов, портов, тега образа и Telegram chat ID.

```bash
cd deploy/compose
sudo bash ./init-private.sh
docker compose --env-file .env config --quiet
```

Скрипт **откажется менять существующие PostgreSQL-пароли**. Он создаёт:
`private/postgres-password` (только PostgreSQL), `private/db-url`
(только Go-процессы), каталоги `private/receiver`,
`private/dashboard` и `private/notify`. Права файлов — 0600, каталогов
— 0700; владелец Go-файлов — UID 10001. UID пользователя PostgreSQL
определяется из образа, не предполагается равным 999.

Для реального TLS-приёмника подготовьте **до запуска Compose**:

- `private/receiver/tls.crt` — действующий сертификат TLS с SAN
  для адреса, к которому подключаются агенты;
- `private/receiver/tls.key` — приватный ключ;
- `private/receiver/agents.json` — реестр разрешённых агентов,
  созданный командой `aegis provision` (а не пустой самодельный JSON).

Создание новых сертификатов и регистрация агента описаны в
[configuration.md](../../docs/configuration.md) и
[services.md](../../docs/services.md). Публичный сертификат CA
передаётся агентам отдельно. Не создавайте production TLS с
`--host 127.0.0.1`, если агенты подключаются по публичному DNS.
После копирования файлов на VPS выставьте им владельца 10001 и
права 0600, каталоги уже предоставляются приложению только для чтения.

Для развёртывания обязательного ядра:

```bash
docker compose --env-file .env up -d --build postgres migrate receiver watch
docker compose --env-file .env ps
docker compose --env-file .env logs --tail=100 receiver watch migrate
```

Compose ожидает healthcheck PostgreSQL, затем успешное завершение
`aegis migrate`, и только после этого запускает зависимые сервисы.
Это гарантирует порядок запуска, но не заменяет внешнюю проверку
доступности gRPC-приёмника. Не меняйте PostgreSQL major-version
простым обновлением образа: для этого требуется отдельная процедура
миграции БД и проверенное восстановление из резервной копии.

Порт `9090` опубликован на `0.0.0.0` **по явному умолчанию** для
агентов. Внешний firewall VPS должен допускать только нужные сети/IP;
gRPC-сервер требует TLS и индивидуальные Bearer-токены. При желании
укажите `AEGIS_GRPC_BIND_IP` в `.env` для более строгого binding.

## Необязательные уведомления

По умолчанию Telegram worker не запускается. Для него создайте
`private/notify/telegram-token` (UID 10001, mode 0600), задайте
`AEGIS_TELEGRAM_CHAT_ID` в `.env` и выполните:

```bash
docker compose --env-file .env --profile notifications up -d notify
```

Это **Telegram-профиль** по умолчанию. SMTP и Webhook уже поддерживаются
командой Aegis `notify`, но не активируются готовым профилем. Для
использования других каналов требуется отдельный пересмотренный Compose
override с передачей секретов через `*_FILE` и ограниченным egress.

## Необязательная удалённая веб-панель

Запуск `dashboard` **требует** сертификата для точного
`AEGIS_DASHBOARD_ORIGIN`, соответствующего приватного ключа и
`users.json` с bcrypt-учётными записями. Поместите их в
`private/dashboard/{tls.crt,tls.key,users.json}`, задайте владельца
10001 и права 0600. Реестр аккаунтов создаётся командой
`aegis dashboard-user --file ... --password-stdin` на доверенной машине.

```bash
docker compose --env-file .env --profile dashboard up -d dashboard
```

По умолчанию HTTPS-панель проброшена **только на loopback хоста**
`127.0.0.1:8443`. Доступ с другой машины требует явно настроенного
TLS reverse proxy, который сохраняет нужный Host/Origin и не обходит
проверки HTTPS приложения, или административного SSH-туннеля.
Публичная публикация после реальной проверки DNS и сертификатов,
RBAC, firewall, rate limiting и сертификатной цепочки — отдельный шаг.

Сервис `history` намеренно не выставляется как отдельный HTTP-порт:
удалённая панель уже реализует необходимые защищённые API.
Диагностический порт pprof/Prometheus не включён и не публикуется.

## Секреты и жизненный цикл

**Почему используется bind mount закрытых файлов, а не простые
Compose `secrets: mode: 0600`?** На локальном Docker Compose
атрибуты `uid`, `gid`, `mode` для secrets из файла могут
не применяться, а `internal/secrets.Resolve` требует Unix 0600.
Bind mounts сохраняют проверяемого владельца/режим на Linux VPS.
Ни пароли, ни токены не попадают в Docker command arguments или
`.env`. Файл URL PostgreSQL включает пароль, поэтому содержимое
`docker compose config` может содержать **путь**, но не сам секрет.

`private/` и `.env` исключены из Git и Docker build context.
У каждого компонента свой mount с необходимыми ключами.
`docker compose down` не удаляет том БД, но
**`docker compose down --volumes` удаляет его — не выполняйте!**

### Обновление без потери данных

1. Сделайте резервную копию PostgreSQL 17 с проверкой SHA-256
   и проверьте восстановление в **отдельной** базе. Простой
   `docker compose down` не считается резервной копией.
2. Проверьте `git status`, фиксированный commit/tag и
   `docker compose config --quiet`.
3. `docker compose --env-file .env build` — собирает новый образ.
   `docker compose --env-file .env up -d postgres migrate receiver watch`
   — применяет идемпотентные миграции до пересоздания процессов.
   Для реальной миграции с изменением схемы предусмотрите окно
   обслуживания и обратную совместимость, не рассчитывайте на
   безопасный downgrade PostgreSQL.
4. После запуска проверьте `docker compose ps`, логи и внешний
   TLS gRPC smoke-test действительным агентом. При сбое
   останавливайте Aegis без удаления БД и расследуйте причину.

**Backup/restore:** стадия 6.6 имеет защищённые CLI-команды, которым
нужны `pg_dump`/`pg_restore` версии PostgreSQL 17. Runtime-образ
данного Compose намеренно не содержит PostgreSQL-клиент. Используйте
операторский контейнер `postgres:17-bookworm` с минимальными правами
и безопасной передачей credentials либо выполняйте CLI-команду на
административном хосте с установленными утилитами. Автоматическое
расписание backup и аварийное восстановление **здесь не настроены**,
поэтому запрещено считать одни лишь named volumes защитой данных.

## Контроль качества

В CI: статическая проверка структуры Compose, изоляции сети,
профилей, доступных портов, ограничений контейнеров, утечки токенов,
а также сборка серверного образа и проверка CLI внутри него.
Настоящий VPS, firewall, CA, backup/restore и эксплуатационная
нагрузка проверяются отдельно в фазе 7.8.
