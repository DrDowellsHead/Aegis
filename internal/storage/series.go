package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// SeriesQuery defines a fixed UTC range and width of aggregation buckets.
// DiskPath must be specified to compare disk usage on the SAME filesystem.
type SeriesQuery struct{
	AgentID string
	From time.Time
	To time.Time
	Step time.Duration
	DiskPath string
}

type SeriesPoint struct{
	At time.Time `json:"at"`
	Samples int64 `json:"samples"`
	CPUSamples int64 `json:"cpu_samples"`
	CPUAvg *float64 `json:"cpu_avg"`
	CPUMin *float64 `json:"cpu_min"`
	CPUMax *float64 `json:"cpu_max"`
	MemorySamples int64 `json:"memory_samples"`
	MemoryAvg *float64 `json:"memory_avg"`
	DiskSamples int64 `json:"disk_samples"`
	DiskAvg *float64 `json:"disk_avg"`
}

type SeriesResult struct{
	AgentID string `json:"agent_id"`
	From time.Time `json:"from"`
	To time.Time `json:"to"`
	Step string `json:"step"`
	DiskPath string `json:"disk_path,omitempty"`
	Points []SeriesPoint `json:"points"`
}

// Public API supports a finite allowlist, not arbitrary SQL intervals.
func ValidStep(d time.Duration)bool {
	switch d {
	case time.Minute,5*time.Minute,15*time.Minute,time.Hour,6*time.Hour,24*time.Hour:
		return true
	}
	return false
}

func ValidateSeriesQuery(q SeriesQuery)error{
	if !identity.ValidID(q.AgentID)||q.From.IsZero()||q.To.IsZero()||!q.From.Before(q.To)||
		!ValidStep(q.Step)||q.To.Sub(q.From)>90*24*time.Hour||
		q.To.Sub(q.From)>500*q.Step||len(q.DiskPath)>1024 {
		return fmt.Errorf("invalid series query: UUID, time range, step or disk path")
	}
	return nil
}

const seriesSQL=`
SELECT
	date_bin($4::bigint * INTERVAL '1 second', started_at,
		TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
	COUNT(*) AS samples,
	COUNT(cpu_used_percent) AS cpu_samples,
	AVG(cpu_used_percent), MIN(cpu_used_percent), MAX(cpu_used_percent),
	COUNT(memory_used_percent) AS memory_samples,
	AVG(memory_used_percent),
	COUNT(CASE WHEN $5::text <> '' AND disk_path = $5::text
		THEN disk_used_percent END) AS disk_samples,
	AVG(CASE WHEN $5::text <> '' AND disk_path = $5::text
		THEN disk_used_percent END) AS disk_avg
FROM aegis_measurements
WHERE agent_id=$1 AND started_at >= $2 AND started_at < $3
  AND projected=TRUE
GROUP BY bucket
ORDER BY bucket ASC
`

func floatPointer(f sql.NullFloat64)*float64{
	if !f.Valid{return nil}
	v:=f.Float64
	return &v
}

// ListSeries aggregates in PostgreSQL: only one bounded row per populated
// bucket crosses the network, never a collection of raw protobuf payloads.
func(p *Postgres)ListSeries(ctx context.Context,q SeriesQuery)(SeriesResult,error){
	if err:=ValidateSeriesQuery(q);err!=nil{return SeriesResult{},err}
	if p==nil||p.db==nil{return SeriesResult{},fmt.Errorf("database unavailable")}
	out:=SeriesResult{
		AgentID:q.AgentID,From:q.From.UTC(),To:q.To.UTC(),Step:q.Step.String(),
		DiskPath:q.DiskPath,Points:make([]SeriesPoint,0),
	}
	rows,err:=p.db.QueryContext(ctx,seriesSQL,q.AgentID,q.From.UTC(),q.To.UTC(),
		int64(q.Step/time.Second),q.DiskPath)
	if err!=nil{return SeriesResult{},fmt.Errorf("query time series: %w",err)}
	defer rows.Close()
	for rows.Next(){
		var row SeriesPoint
		var cpuAvg,cpuMin,cpuMax,memAvg,diskAvg sql.NullFloat64
		if err:=rows.Scan(&row.At,&row.Samples,&row.CPUSamples,&cpuAvg,&cpuMin,&cpuMax,
			&row.MemorySamples,&memAvg,&row.DiskSamples,&diskAvg);err!=nil{
			return SeriesResult{},fmt.Errorf("scan time series: %w",err)
		}
		row.At=row.At.UTC()
		row.CPUAvg=floatPointer(cpuAvg)
		row.CPUMin=floatPointer(cpuMin)
		row.CPUMax=floatPointer(cpuMax)
		row.MemoryAvg=floatPointer(memAvg)
		row.DiskAvg=floatPointer(diskAvg)
		out.Points=append(out.Points,row)
	}
	if err:=rows.Err();err!=nil{return SeriesResult{},fmt.Errorf("iterate time series: %w",err)}
	return out,nil
}
