// Package observability exposes bounded, low-cardinality Prometheus metrics.
// It intentionally does not register agent IDs, URLs or other user-controlled labels.
package observability

import (
	"database/sql"
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"
	"time"
)

var latencyBounds = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}

type histogram struct {
	count   uint64
	sum     float64
	buckets [len(latencyBounds)]uint64
}

func (h *histogram) observe(seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	h.count++
	h.sum += seconds
	for i, upper := range latencyBounds {
		if seconds <= upper {
			h.buckets[i]++
		}
	}
}

type rpcKey struct{ method, code string }
type dbKey struct{ operation, result string }

// Registry is one receiver process's in-memory metrics, not global state.
type Registry struct {
	mu           sync.Mutex
	started      time.Time
	inflight     int64
	rpcCount     map[rpcKey]uint64
	rpcDurations map[string]*histogram
	dbCount      map[dbKey]uint64
	dbDurations  map[string]*histogram
}

func New() *Registry {
	return &Registry{
		started:      time.Now(),
		rpcCount:     make(map[rpcKey]uint64),
		rpcDurations: make(map[string]*histogram),
		dbCount:      make(map[dbKey]uint64),
		dbDurations:  make(map[string]*histogram),
	}
}

func safeMethod(s string) string {
	switch s {
	case "send", "heartbeat":
		return s
	default:
		return "other"
	}
}

func safeCode(s string) string {
	switch s {
	case "OK", "Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded",
		"NotFound", "AlreadyExists", "PermissionDenied", "ResourceExhausted",
		"FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented", "Internal",
		"Unavailable", "DataLoss", "Unauthenticated":
		return s
	default:
		return "Unknown"
	}
}

// BeginRPC and EndRPC are safe to call concurrently from gRPC goroutines.
func (r *Registry) BeginRPC() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.inflight++
	r.mu.Unlock()
}

func (r *Registry) EndRPC(method, code string, elapsed time.Duration) {
	if r == nil {
		return
	}
	method, code = safeMethod(method), safeCode(code)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	r.rpcCount[rpcKey{method, code}]++
	h := r.rpcDurations[method]
	if h == nil {
		h = &histogram{}
		r.rpcDurations[method] = h
	}
	h.observe(elapsed.Seconds())
}

// ObserveDatabase times the complete durable persistence operation, including
// SQL pool wait time, transactions, DB queries and COMMIT where applicable.
func (r *Registry) ObserveDatabase(operation string, err error, elapsed time.Duration) {
	if r == nil {
		return
	}
	if operation != "save" && operation != "heartbeat" {
		operation = "other"
	}
	result := "success"
	if err != nil {
		result = "error"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dbCount[dbKey{operation, result}]++
	h := r.dbDurations[operation]
	if h == nil {
		h = &histogram{}
		r.dbDurations[operation] = h
	}
	h.observe(elapsed.Seconds())
}

// WritePrometheus writes the Prometheus text exposition format (counters,
// gauges and cumulative histograms) from a coherent snapshot.
func (r *Registry) WritePrometheus(w io.Writer, dbStats func() sql.DBStats) {
	if r == nil {
		return
	}
	r.mu.Lock()
	inflight := r.inflight
	uptime := time.Since(r.started).Seconds()
	rpcs := make(map[rpcKey]uint64, len(r.rpcCount))
	for k, v := range r.rpcCount {
		rpcs[k] = v
	}
	rpcDur := make(map[string]histogram, len(r.rpcDurations))
	for k, v := range r.rpcDurations {
		rpcDur[k] = *v
	}
	dbs := make(map[dbKey]uint64, len(r.dbCount))
	for k, v := range r.dbCount {
		dbs[k] = v
	}
	dbDur := make(map[string]histogram, len(r.dbDurations))
	for k, v := range r.dbDurations {
		dbDur[k] = *v
	}
	r.mu.Unlock()

	fmt.Fprintln(w, "# HELP aegis_process_uptime_seconds Time since telemetry receiver metrics were initialized.")
	fmt.Fprintln(w, "# TYPE aegis_process_uptime_seconds gauge")
	fmt.Fprintf(w, "aegis_process_uptime_seconds %.6f\n", uptime)
	fmt.Fprintln(w, "# HELP aegis_grpc_inflight_requests Active gRPC requests.")
	fmt.Fprintln(w, "# TYPE aegis_grpc_inflight_requests gauge")
	fmt.Fprintf(w, "aegis_grpc_inflight_requests %d\n", inflight)
	fmt.Fprintln(w, "# HELP aegis_grpc_requests_total Completed gRPC requests by method and status.")
	fmt.Fprintln(w, "# TYPE aegis_grpc_requests_total counter")
	keys := make([]rpcKey, 0, len(rpcs))
	for k := range rpcs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].code < keys[j].code
	})
	for _, k := range keys {
		fmt.Fprintf(w, "aegis_grpc_requests_total{method=%q,code=%q} %d\n", k.method, k.code, rpcs[k])
	}
	writeHistograms(w, "aegis_grpc_request_duration_seconds", "Duration of completed gRPC requests in seconds.", rpcDur)
	fmt.Fprintln(w, "# HELP aegis_db_operations_total Completed SQL-backed storage operations by result.")
	fmt.Fprintln(w, "# TYPE aegis_db_operations_total counter")
	dbKeys := make([]dbKey, 0, len(dbs))
	for k := range dbs {
		dbKeys = append(dbKeys, k)
	}
	sort.Slice(dbKeys, func(i, j int) bool {
		if dbKeys[i].operation != dbKeys[j].operation {
			return dbKeys[i].operation < dbKeys[j].operation
		}
		return dbKeys[i].result < dbKeys[j].result
	})
	for _, k := range dbKeys {
		fmt.Fprintf(w, "aegis_db_operations_total{operation=%q,result=%q} %d\n", k.operation, k.result, dbs[k])
	}
	writeHistograms(w, "aegis_db_operation_duration_seconds", "Duration of SQL-backed persistence including pool waits and COMMIT.", dbDur)
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintln(w, "# HELP aegis_go_heap_alloc_bytes Live Go heap allocations in bytes.")
	fmt.Fprintln(w, "# TYPE aegis_go_heap_alloc_bytes gauge")
	fmt.Fprintf(w, "aegis_go_heap_alloc_bytes %d\n", mem.HeapAlloc)
	fmt.Fprintln(w, "# HELP aegis_go_goroutines Current number of Go goroutines.")
	fmt.Fprintln(w, "# TYPE aegis_go_goroutines gauge")
	fmt.Fprintf(w, "aegis_go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintln(w, "# HELP aegis_go_gc_pause_seconds_total Total stop-the-world GC pause time.")
	fmt.Fprintln(w, "# TYPE aegis_go_gc_pause_seconds_total counter")
	fmt.Fprintf(w, "aegis_go_gc_pause_seconds_total %.9f\n", float64(mem.PauseTotalNs)/1e9)
	if dbStats != nil {
		stats := dbStats()
		fmt.Fprintln(w, "# HELP aegis_sql_pool_open_connections Current open SQL connections.")
		fmt.Fprintln(w, "# TYPE aegis_sql_pool_open_connections gauge")
		fmt.Fprintf(w, "aegis_sql_pool_open_connections %d\n", stats.OpenConnections)
		fmt.Fprintln(w, "# HELP aegis_sql_pool_in_use_connections Current borrowed SQL connections.")
		fmt.Fprintln(w, "# TYPE aegis_sql_pool_in_use_connections gauge")
		fmt.Fprintf(w, "aegis_sql_pool_in_use_connections %d\n", stats.InUse)
		fmt.Fprintln(w, "# HELP aegis_sql_pool_idle_connections Current idle SQL connections.")
		fmt.Fprintln(w, "# TYPE aegis_sql_pool_idle_connections gauge")
		fmt.Fprintf(w, "aegis_sql_pool_idle_connections %d\n", stats.Idle)
		fmt.Fprintln(w, "# HELP aegis_sql_pool_wait_count_total Number of times SQL operations waited for a connection.")
		fmt.Fprintln(w, "# TYPE aegis_sql_pool_wait_count_total counter")
		fmt.Fprintf(w, "aegis_sql_pool_wait_count_total %d\n", stats.WaitCount)
		fmt.Fprintln(w, "# HELP aegis_sql_pool_wait_duration_seconds_total Total SQL pool wait time.")
		fmt.Fprintln(w, "# TYPE aegis_sql_pool_wait_duration_seconds_total counter")
		fmt.Fprintf(w, "aegis_sql_pool_wait_duration_seconds_total %.9f\n", stats.WaitDuration.Seconds())
	}
}

func writeHistograms(w io.Writer, name, help string, values map[string]histogram) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, method := range keys {
		h := values[method]
		for i, upper := range latencyBounds {
			fmt.Fprintf(w, "%s_bucket{operation=%q,le=%q} %d\n", name, method, fmt.Sprint(upper), h.buckets[i])
		}
		fmt.Fprintf(w, "%s_bucket{operation=%q,le=\"+Inf\"} %d\n", name, method, h.count)
		fmt.Fprintf(w, "%s_sum{operation=%q} %.9f\n", name, method, h.sum)
		fmt.Fprintf(w, "%s_count{operation=%q} %d\n", name, method, h.count)
	}
}
