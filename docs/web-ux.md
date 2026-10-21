# Aegis Dashboard — UX specification (step 4.1)

## Navigation

Desktop: narrow permanent left sidebar with brand, Overview, Agents,
and future Incidents / Settings. Mobile: top branding and a horizontally
scrollable, keyboard-focusable navigation row. Active route exposes
`aria-current=page`.

| Route | Purpose | Initial state in 4.1 | Next implementation |
|---|---|---|---|
| `/` | Overview and fleet status | No live data; explanatory empty state | 4.2 |
| `/agents` | List/filter agents, relative contact time | No live data; explanatory empty state | 4.2 |
| `/agents/:id` | Trends and per-agent measurements | No live data; UI structure | 4.3 |
| `/incidents` | Rule alerts and history | Clearly marked planned | 5.x |
| `/settings` | Operator preferences | Clearly marked planned | 4.6 |

## Overview page (4.2)

Header: `Monitoring overview`, data freshness indicator and last updated time.
Summary: total agents, online, offline, unknown (always with freshness
metadata, not a fabricated "0" before first fetch).
Fleet list: agent UUID or operator-assigned alias in the future,
status badge, OS, last contact, action to view.
States: `loading`, `empty`, `unauthorized`, `unavailable`, `ready`.
Filters/thresholds must not imply a live heartbeat is implemented.

## Agent detail page (4.3–4.4)

Breadcrumb: Overview / Agents / agent short UUID.
Header: status, last contact, last sample, OS, architecture.
Timeline choices: 1h, 24h, 7d, custom range. Client chooses a supported
step so `(to-from)/step <= 500`; server validates independently.
Charts: CPU avg/min/max, RAM avg, disk avg *for a selected disk_path*.
`null` shows a gap. Empty time bucket shows a gap, not a zero.
Metric card labels specify percent used. Network cumulative bytes are
shown only in raw history until a correct rate API exists.
History table: keyset cursor, bounded pages, link to measurement detail
and partial error information.

## Wireframes (structure, not fake metrics)

Desktop overview:

```text
┌────────────────┬────────────────────────────────────────────────────┐
│ AEGIS          │ Overview                        Data not connected │
│                │                                                    │
│ Overview       │ [Agents: —] [Online: —] [Offline: —] [Unknown: —]  │
│ Agents         │                                                    │
│ Incidents *    │ Registered agents                                  │
│ Settings *     │ No data source connected.                          │
│                │                                                    │
└────────────────┴────────────────────────────────────────────────────┘
* planned
```

Desktop detail:

```text
┌────────────────┬────────────────────────────────────────────────────┐
│ sidebar        │ Agents / agent-id                    [24h] [7d]     │
│                │ Last contact: —   OS: —   Disk path: —             │
│                │ ┌──────────────────────────┐ ┌─────────────────┐ │
│                │ │ CPU (avg/min/max)        │ │ RAM / disk      │ │
│                │ │ no series loaded         │ │ no series       │ │
│                │ └──────────────────────────┘ └─────────────────┘ │
│                │ History: no entries                               │
└────────────────┴────────────────────────────────────────────────────┘
```

## Principles

- Dark palette as initial visual direction; supports CSS
  `prefers-reduced-motion`, semantic HTML and keyboard interaction.
- Empty state NEVER pretends to be a real zero; unknown/offline are
  not power-state guarantees.
- No browser storage or display of server operator tokens.
- No charts with invented telemetry in the running app.
- Source display times may differ from receiver timestamps;
  preserve both in API model and label them clearly.
