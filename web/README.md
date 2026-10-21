# Aegis dashboard — step 4.1

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
