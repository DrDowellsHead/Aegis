# ADR 0014 — Agent time-series dashboard and read-only history (step 4.3)

Status: implemented locally in develop, pending final CI.

## Real data over existing local session

The `aegis dashboard` Go handler adds two **cookie-authenticated,
loopback-only** read endpoints:
- `GET /api/web/series`: same query contract as
  `GET /api/v1/series` (`agent_id,from,to,step,disk_path`).
- `GET /api/web/measurements`: same query contract as
  `GET /api/v1/measurements` (`agent_id,from,to,limit,cursor`).

Both routes reuse the existing history package's request validators,
so time intervals, UUIDs, allowed steps (1m, 5m, 15m, 1h, 6h, 24h),
90-day/500-bin limits, opaque keyset cursor and duplicate query
parameter checks stay the same. Neither route accepts a browser Bearer
token nor opens the privileged history HTTP port. The backend Reader
interface is extended with ListHistory and ListSeries, which are already
implemented by the existing PostgreSQL storage. Errors are logged
server-side and answered to the browser with generic 503 status.

The app supports three preset, snapshot-frozen UTC time windows:
- 1 hour at 1-minute resolution;
- 24 hours at 5-minute resolution;
- 7 days at 1-hour resolution.

Changing time period or refreshing creates a new time window and clears
the history cursor; the "next page" operation retains the identical
from/to range, so the cursor remains bound to its filters. A new custom
range picker is still 4.4, not included in 4.3.

## Null and gap semantics

A snapshot can have CPU, memory or disk absent; this means no reading,
not 0% usage. Aggregation yields nullable cpu_avg/min/max, memory_avg,
disk_avg. We draw a native, accessible SVG for each metric; segment
construction breaks paths for nulls and for gaps greater than
1.5 * the bucket width. Never interpolate over an empty bucket.
CPU shows avg/min/max separately. Disk average is calculated only for
an explicit exact disk_path from a recent sample or manual input; never
combine different filesystems. Each chart displays 0–100% bounds.

The bounded history page (20 entries) displays started_at vs received_at,
CPU/RAM/disk percentages and partial measurement errors. It never
interprets network cumulative byte counters as bytes/s or speed. A
genuine network transfer-rate chart needs consecutive counter deltas,
a monotonic interval and reset/overflow handling, so it is deliberately
deferred. 64-bit byte counters are not displayed from JSON numbers
until an exact decimal string wire representation is available.

## Frontend and security

The page is a React component `AgentDetail` with its own abortable
fetch lifecycle and a runtime JSON parser. Two concurrent API reads
retrieve series and history; a 401 redirects to login. A malformed
response yields a visible error rather than a fabricated zero.
Manual/periodic refresh in the parent is reused.

No additional chart runtime dependencies are required; SVG uses React.
No client token or database URL is stored in JavaScript.
No public dashboard, production TLS or per-user access policy is
introduced: **the entire dashboard stays numeric-loopback-only**.

## Author and commercial contact

Apache-2.0 + NOTICE remain the source licensing policy. Public
`CONTACT.md` and a GitHub Issue template invite collaboration, jobs
and commercial enquiries using GitHub. The footer links to the author
and to a new public issue. GitHub Issues are public; never post secrets.
No private email is exposed without the author's explicit choice.

## Remaining work

- 4.4 custom time ranges, detailed chart interactions, date/time UX.
- 4.5 dynamic background refresh, cache/error UX, browser E2E testing.
- Network speed derived from counter changes, with reset detection.
- 4.6 HTTPS, RBAC, Secure-cookie sessions and controlled remote access.
