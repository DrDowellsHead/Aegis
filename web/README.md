# Aegis dashboard — stages 4.1–4.4

Static React + TypeScript + Vite application shell. No real monitoring
data, production login, or browser-side Bearer token exists here.

## Requirements

Node.js 22.12+ or a supported Node 24+ version and npm.
Go is NOT necessary to preview the standalone shell.

```sh
cd web
npm ci
npm run dev
```

Open http://127.0.0.1:5173 locally. Vite listens on loopback only.
The scaffold has responsive navigation and honest unavailable-data
states; it never calls or proxies `aegis history`.

```sh
npm run typecheck
npm run build
npm run preview
```

Production output: `web/dist/` (ignored by Git). Next phases add
actual data sources, graphs and an authenticated **same-origin Go gateway**
BEFORE remote/browser access to historical measurements is enabled.
Do not store `AEGIS_HISTORY_TOKEN` or a PostgreSQL DSN in frontend code
or in `VITE_*` environment variables.

Contracts: `../api/openapi/history-v1.yaml`,
`src/api/types.ts`. Design: `../docs/web-ux.md` and
`../docs/adr/0012-web-architecture.md`.

## Этап 4.2: реальные агенты через локальный Go gateway

`npm run dev` по-прежнему предназначен для верстки — он НЕ проксирует
API и НЕ предоставляет аутентифицированный вход. Для реальных данных:

```sh
cd web
npm ci
npm run build
cd ..
# Set AEGIS_DATABASE_URL and a separate AEGIS_DASHBOARD_TOKEN.
go run ./cmd/aegis dashboard --listen 127.0.0.1:8082 --static-dir web/dist
```

Перейдите на http://127.0.0.1:8082. Панель запрашивает сессию,
после входа показывает подсчёт агентов и пагинированную таблицу.
HTTP-only cookie хранит состояние на Go-сервере, а введённый секрет
не сохраняется в localStorage. Через восемь часов сессия истекает.

Это *строго локальный* HTTP сервер, не для публичного деплоя.
Подробнее: ../docs/adr/0013-local-dashboard-gateway.md.

## Этап 4.3 — графики и история одного агента

После `npm run build` и запуска `aegis dashboard` из корня
репозитория перейдите на `http://127.0.0.1:8082/agents`,
выберите зарегистрированный компьютер. Откроется страница
`/agents/UUID` с настоящими графиками CPU, RAM и занятого
пространства выбранного диска. Пустая метрика остаётся пустой,
прерывающаяся история отображается с разрывами.

История замеров — отдельная таблица с временем сбора, временем
сохранения на сервере и ошибками отдельных метрик.
Периоды 1h/24h/7d сохраняют from/to при пагинации.
Привилегированный токен history API в JavaScript не используется.

## 4.4: точные графики и сравнение

`npm test` запускает Vitest на границах UTC-дат, порогах длительности,
пропусках, изменениях часовых поясов и взвешенном среднем.
`npm run build` дополнительно проверяет TypeScript.

На странице агента можно указать UTC начало/конец (выбор календаря
не использует локальную трактовку `datetime-local`), до 90 суток.
Выбирается минимальный разрешённый шаг, вмещающий максимум 500
бакетов. На графиках при наведении отображается точное локальное и UTC
время точки; для клавиатуры есть выпадающий список.
Включаемое сравнение накладывает предыдущий равный UTC период пунктиром.
Без текущих или предыдущих измерений разница отображается прочерком.

Локальный Go gateway и сессия без изменений: не открывайте 8082 наружу.

## Этап 4.5: устойчивые запросы и браузерные тесты

`npm test` проверяет UTC-интервалы, пропуски, взвешенные агрегаты и
поведение сетевого клиента: GET делает не более двух повторов после
временных ошибок, каждый запрос имеет ограничение времени, 401 не
повторяется, отмена останавливает запрос и ожидание.

`npm run e2e` запускает Playwright Chromium (предварительно
`npx playwright install chromium`). Тестовый браузер подключается к
Vite на **127.0.0.1:5173** и перехватывает запросы `/api/web/*`,
подменяя ответы контролируемыми данными. E2E проверяет вход, переход
к агенту, осмотр графиков, ошибки, повторное подключение, истечение
сессии и мобильную навигацию. Это не полномасштабный сквозной тест
реального Go + PostgreSQL; их контракт покрывают отдельные Go
интеграционные тесты.

При обновлении текущего фильтра React сохраняет последний успешно
полученный снимок и отображает статус загрузки. При смене агента,
диапазона или курсора старые значения не представляются как новые.
Истёкшая сессия переводит на вход; серверные токены в JS не попадают.
Сетевые GET с ошибкой 503 допускают максимум 2 повтора с паузой
350/700 мс; POST входа никогда не повторяется автоматически.
