# Aegis

Aegis — учебно-практическая платформа мониторинга инфраструктуры на Go.
Разработка идёт в `develop`; `main` предназначена для проверенных версий.

## Текущий этап

Работают диагностический HTTP-сервер, HTTP-проверки, локальный агент метрик и gRPC-передача с TLS и проверкой токенов. Полная платформа (агенты, API истории, уведомления)
разрабатывается поэтапно. Реализованные возможности и ограничения описываются
по мере появления кода — план не является списком готовых функций.

## План

1. CLI, жизненный цикл сервера, HTTP-проверки доступности.
2. Агент со сбором CPU, RAM, диска и сети на Windows, macOS и Linux.
3. Защищённая передача измерений через gRPC.
4. PostgreSQL, миграции и локальный API истории.
5. Инциденты, уведомления и наблюдаемость самой системы.
6. Проверка отказов, производительности и развёртывание на одном VPS.

Архитектура: [ADR 0001](docs/adr/0001-foundation.md).
Рабочий процесс: [CONTRIBUTING.md](CONTRIBUTING.md).

## Реализовано: первый рабочий этап

- `aegis version` — версия исполняемого файла.
- `aegis serve` — локальный HTTP-сервер, `/healthz`, JSON-журналы,
  завершение по Ctrl+C/SIGTERM с ограниченным временем ожидания.
- `aegis check --url http://127.0.0.1:8080/healthz` — однократная
  HTTP-проверка с таймаутом, JSON-результатом и кодом завершения.
- CI: тесты с обнаружением гонок на Windows/macOS/Linux; сборка шести архивов.
- Процесс подготовки черновика релиза по тегу из истории main.

[Установка и запуск](docs/installation.md) · [Как выпускать релизы](docs/releases.md)

```sh
go run ./cmd/aegis serve
# В другом терминале:
go run ./cmd/aegis check --url http://127.0.0.1:8080/healthz
```

gRPC-передача защищена TLS и токенами. PostgreSQL сохраняет измерения с проверкой
повторов. API истории реализован для локального доступа; уведомления ещё не реализованы.

## Локальный агент метрик

```sh
go run ./cmd/aegis collect --disk .
go run ./cmd/aegis agent --disk . --interval 5s --count 3
```

`collect` выводит один JSON-объект, `agent` — по объекту на строку.
Измеряются загрузка CPU, память, файловая система выбранного пути и счётчики
байтов отдельных сетевых интерфейсов. Счётчики сети не являются скоростью.
При ошибке одной группы другие сохраняются; отсутствующие значения — `null`,
описание ошибки — в `errors`. Подробности: [метрики](docs/metrics.md).

## Защищённая отправка метрик (этап 3.2)

Для локальной демонстрации запустите следующие команды в каталоге проекта.
Команда `cert` выпускает **демонстрационные** сертификаты для адреса 127.0.0.1.
Команда `provision` создаёт пару: закрытые данные агента и реестр разрешённых агентов.

```sh
go run ./cmd/aegis cert --host 127.0.0.1 --ca-cert local-ca.crt --tls-cert local-server.crt --tls-key local-server.key
go run ./cmd/aegis provision --agents-file local-agents.json --credentials local-agent.json
```

После настройки PostgreSQL и выполнения миграций (см. этап 3.4)
в первом терминале запустите сервер:

```sh
go run ./cmd/aegis receive --listen 127.0.0.1:9090 --tls-cert local-server.crt --tls-key local-server.key --agents-file local-agents.json
```

Во втором терминале отправьте одно измерение:

```sh
go run ./cmd/aegis send --server 127.0.0.1:9090 --credentials local-agent.json --ca-cert local-ca.crt --disk .
```

Ожидаемый ответ включает `"received": true`, `"durable": true` и
уникальный `measurement_id`. Данные сохраняются в PostgreSQL.
Команды `collect` и `agent` по-прежнему работают локально, без подключения.

Для нескольких компьютеров копируйте ТОЛЬКО соответствующий закрытый
`agent.json` и публичный CA-сертификат на агент, оставляя `agents.json`
и закрытый TLS-ключ на сервере. По умолчанию сервер слушает loopback;
публикация порта требует настройки firewall, прав доступа и аудита.
Адрес клиента обязан совпадать с именем/IP в сертификате (SAN).
Регистрация следующего агента требует перезапуска сервера для перечитывания реестра.
Не запускайте две регистрации одновременно в один файл.

На Unix файлы с токенами и приватными ключами создаются с правами 0600.
На Windows дополнительно проверьте ACL, чтобы секреты были доступны
только вашей учётной записи/службе. Никогда не публикуйте их в GitHub.
Самоподписанный демонстрационный CA не предназначен для серьёзного развёртывания.
Передача защищена; отдельная команда `forward` реализует очередь и повторы.
Ротация и отзыв токенов, а также административный интерфейс ещё не реализованы.

Контракт: [telemetry.proto](api/proto/aegis/telemetry/v1/telemetry.proto).
Документы: [ADR 0003](docs/adr/0003-telemetry-contract.md),
[ADR 0004](docs/adr/0004-agent-identity-and-tls.md).

## Очередь и повторная отправка (экспериментальный этап 3.3)

Вместо одноразовой команды `send` можно запустить длительный режим `forward`:

```sh
go run ./cmd/aegis forward --server 127.0.0.1:9090 --credentials local-agent.json --ca-cert local-ca.crt --queue-dir /path/to/private/spool --interval 5s --max-queue-mb 64
```

На Windows замените `/path/to/private/spool` на подходящий путь либо вообще
опустите `--queue-dir`: тогда очередь будет храниться в директории конфигурации
пользователя, отдельно для каждого UUID агента. Для демонстрации можно указать
`--count 3`, чтобы собрать три измерения и попытаться отправить все три.

**Важно:** теперь только `durable=true` после записи в PostgreSQL разрешает
удалить подтверждённую запись из локальной очереди. Потерянное подтверждение
вызывает повтор с тем же ID; сервер не создаёт дубликат.
При переполнении агент сообщает об ошибке и останавливает сбор; старые записи
не стираются. Не запускайте два процесса с одной очередью одновременно.

[ADR 0005: дисковая очередь, повторные попытки и ограничения](docs/adr/0005-disk-queue-and-retry.md).

## PostgreSQL: миграции и устойчивое подтверждение (этап 3.4)

Для локального ознакомления нужен PostgreSQL 17+ (проверяем CI на 17).
Например, с Docker (пароль исключительно демонстрационный):

```sh
docker run --name aegis-postgres -d -e POSTGRES_USER=aegis -e POSTGRES_PASSWORD=dev-only-change-this -e POSTGRES_DB=aegis -p 127.0.0.1:5432:5432 -v aegis-pgdata:/var/lib/postgresql/data postgres:17
```

В Windows PowerShell укажите строку подключения через переменную окружения
терминала (не храните реальный пароль в git):

```powershell
$env:AEGIS_DATABASE_URL = "postgres://aegis:dev-only-change-this@127.0.0.1:5432/aegis?sslmode=disable"
go run ./cmd/aegis migrate
go run ./cmd/aegis receive --listen 127.0.0.1:9090 --tls-cert local-server.crt --tls-key local-server.key --agents-file local-agents.json
```

`sslmode=disable` допускается только для локального демонстрационного PostgreSQL
на loopback. К удалённой базе нужно подключаться по TLS с проверкой сервера.
На машине агента PostgreSQL не требуется. Отдельная команда `forward`
сохраняет измерения на диск, отправляет их и удаляет **только после**
подтверждения фиксации в PostgreSQL. Миграции выполняются вручную;
при старте `receive` проверяет наличие схемы и настройки записи.

В PostgreSQL метрики сохраняются как protobuf BYTEA, а первичный ключ
`(agent_id, measurement_id)` защищает от дубликатов.
Узнать число измерений можно SQL-запросом:

```sql
SELECT agent_id, count(*) FROM aegis_measurements GROUP BY agent_id;
```

Полное описание: [ADR 0006](docs/adr/0006-postgresql-durable-ack.md).

## История измерений через HTTP (этап 3.5)

Агент по-прежнему отправляет метрики gRPC-серверу `receive`.
Отдельная команда `history` даёт доступ к сохранённым измерениям
только через локальный интерфейс и **отдельный операторский токен**.
Нельзя использовать для этого токен агента.

Для Windows PowerShell (после настройки PostgreSQL из этапа 3.4):

```powershell
$env:AEGIS_DATABASE_URL = "postgres://aegis:dev-only-change-this@127.0.0.1:5432/aegis?sslmode=disable"
go run ./cmd/aegis migrate
$env:AEGIS_HISTORY_TOKEN = "demo-only-replace-with-a-random-32byte-secret"
go run ./cmd/aegis history --listen 127.0.0.1:8081
```

Лучше использовать отдельную SQL-учётную запись с правами только на SELECT
по таблицам `aegis_measurements` и `aegis_schema_migrations`, а не роль,
которой `receive` пишет в базу. В отдельном PowerShell-терминале укажите
те же `AEGIS_HISTORY_TOKEN` и UUID из `local-agent.json`:

```powershell
$agentId = (Get-Content local-agent.json | ConvertFrom-Json).agent_id
$headers = @{Authorization = "Bearer $env:AEGIS_HISTORY_TOKEN"}
Invoke-RestMethod -Headers $headers -Uri "http://127.0.0.1:8081/api/v1/measurements?agent_id=$agentId&limit=5"
```

Если запрос вернул `next_cursor`, следующую страницу можно получить так:

```powershell
$page = Invoke-RestMethod -Headers $headers -Uri "http://127.0.0.1:8081/api/v1/measurements?agent_id=$agentId&limit=5"
if ($page.has_more) {
    $cursor = [uri]::EscapeDataString($page.next_cursor)
    Invoke-RestMethod -Headers $headers -Uri "http://127.0.0.1:8081/api/v1/measurements?agent_id=$agentId&limit=5&cursor=$cursor"
}
```

Поддерживаются `from` (включительно), `to` (исключительно) в RFC 3339,
`limit` (1..100) и курсорная пагинация. Старые снимки из PostgreSQL
преобразуются из Protobuf в JSON, сохраняя структуру CPU, памяти,
диска и сети и сообщения о частичных ошибках.

**Безопасность:** текущее HTTP API слушает только 127.0.0.1/::1.
Не ставьте перед ним публичный прокси и не публикуйте порт 8081
в интернет: для удалённого доступа понадобятся TLS, надёжная
административная аутентификация и политика авторизации.

Подробнее: [ADR 0007](docs/adr/0007-history-api.md).

## Временные ряды и агрегаты для графиков (этап 3.6)

Сначала на сервере выполните миграцию 004 и **возобновляемое заполнение
проекций старых записей**, иначе графики до обновления схемы будут пустыми:

```powershell
go run ./cmd/aegis migrate
go run ./cmd/aegis backfill --batch-size 100
```

В обоих случаях требуется `AEGIS_DATABASE_URL` с полномочиями на запись.
Команда backfill поддерживает `--max-records N` для проверки небольшими
порциями и безопасно повторяется после прерывания.

С уже запущенной командой `aegis history` запросите временной ряд:

```powershell
$agentId = (Get-Content local-agent.json | ConvertFrom-Json).agent_id
$headers = @{ Authorization = "Bearer $env:AEGIS_HISTORY_TOKEN" }
$from = [uri]::EscapeDataString("2026-10-20T12:00:00Z")
$to = [uri]::EscapeDataString("2026-10-20T13:00:00Z")
Invoke-RestMethod -Headers $headers -Uri "http://127.0.0.1:8081/api/v1/series?agent_id=$agentId&from=$from&to=$to&step=5m"
```

`step`: 1m, 5m, 15m, 1h, 6h, 24h; максимум 500 интервалов
и 90 дней за запрос. Ответ содержит `points`, их время `at`,
`cpu_avg/min/max`, `memory_avg`, `disk_avg`, а также отдельные счётчики
измерений. Для диска **обязательно указывайте** `disk_path`, например
`&disk_path=C%3A%5C` для `C:\\` или `&disk_path=%2F` для `/`.
Без него `disk_avg` будет `null` — усреднять разные файловые системы нельзя.

Пустые интервалы не заполняются нулями. Пока API доступен только локально.

[ADR 0008 — временные ряды](docs/adr/0008-time-series.md) ·
[Полный план Aegis](docs/roadmap.md).
