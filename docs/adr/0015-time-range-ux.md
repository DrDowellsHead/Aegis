# ADR 0015 — UTC intervals, exact chart points and previous-period comparison

Status: 4.4 (development), local web dashboard only.

## Ranges and timezone contract

The API always accepts RFC3339 timestamps with explicit timezone.
The frontend produces UTC ISO 8601 strings; the requested `from` is
inclusive and `to` exclusive. PostgreSQL groups with `date_bin` over
absolute instants anchored to 1970-01-01 UTC, not a user's wall clock.

Quick ranges are 1h, 24h, 7d (from the current UTC instant). The custom
range uses **UTC** `datetime-local` fields that are visibly labeled
as UTC. The browser **does not** silently use the client's local timezone
to interpret these inputs. It strictly validates format, real calendar
dates and ordering. No period over 90 days is accepted. Step is the
smallest allowed of `1m,5m,15m,1h,6h,24h` that fits within **500 buckets**.
The backend repeats these constraints independently.

The graphs and tooltips display the UTC instants in the **browser's local
timezone**, with an explicit timezone label and the exact UTC timestamp
in the point inspector. A daylight-saving transition may repeat or skip
wall-clock labels; the underlying UTC instants remain unambiguous. This
also means a 24h preset is always 86,400 elapsed seconds: it is NOT
necessarily a local calendar day during a DST change. A future calendar
view can allow choosing a named IANA timezone with a UI for ambiguous
local times.

## Comparison

The "previous period" is the **immediately preceding equal elapsed UTC
duration**: `[from-(to-from), from)`. The step is identical to the
current period. The comparison request is separate and authenticated by
the same local cookie. Previous samples are aligned on the SVG by
elapsed UTC duration, not by potentially ambiguous wall-clock hours.
Dashed strokes denote the preceding range.

Displayed overall means are **weighted by the number of actual samples
in each bucket**, per individual metric: `sum(bucketAvg * bucketCount)
 / sum(bucketCount)`. Missing values and zero-count buckets do not
contribute. Difference is in percentage points, not a percent increase.
There is no statistic if either period lacks observations; show `—`,
not a fictional zero.

## SVG pointer/keyboard access and gaps

Native SVG is used; no charting dependency is necessary. Pointer
movement selects the closest bucket only within 0.55 step of its
timestamp. Otherwise the inspector clears; it must not display an
unrelated distant value in a large gap. There is a separate accessible
select field containing exact observation times for keyboard use.
Inspector shows local time, UTC timestamp, per-series values and sample
count; the previous period includes the corresponding original instant.

A genuinely measured `0` is plotted; `null` or a missing bucket is
a **gap**. Polyline paths are broken on nulls and discontinuities greater
than 1.5 step widths. We do not extrapolate across telemetry outages.
CPU shows avg/min/max; comparison overlays only the previous avg. Disk
requires an exact disk path; snapshots from different disks are not
silently combined.

## Fetch and pagination

History and aggregates load independently. History page changes must
not re-query the full time series. The history cursor stays bound to
the same immutable `from/to`; applying a new range resets pagination.
For presets, refresh reanchors the interval at the latest clock time.
For a custom range, refresh re-fetches the same fixed timestamps, not
a sliding interval. Both requests use AbortController on unmount/changes.
The previously introduced local session remains required: no public
web access, no browser Bearer or database credentials, no additional Go
endpoints or schema migration.

## Tests and limits

Vitest unit tests check date parsing (including leap invalid dates),
UTC/DST comparison, step and 90-day limits, weighted average, null gaps,
zero readings and pointer distance. TypeScript strict compilation and
production web bundling run in CI alongside Go PostgreSQL integration.

Not included: mouse-wheel zoom, drag-to-select, client data export,
server-side user-configurable calendar timezone, fully tested visual
browser E2E/assistive-technology support, and derived network speed.
