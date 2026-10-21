package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

type NotificationRoute struct {
	Channel string
	Target string
}
type NotificationJob struct {
	ID string
	EventID int64
	Channel string
	Target string
	IncidentID string
	Kind string
	Action string
	Title string
	AgentID string
	Severity string
	Attempt int
	LeaseToken string
}
type NotificationStats struct {
	Pending int64 `json:"pending"`
	Retry int64 `json:"retry"`
	Leased int64 `json:"leased"`
	Sent int64 `json:"sent"`
	Dead int64 `json:"dead"`
}

func validateNotificationRoute(r NotificationRoute) error {
	if len(r.Target)==0 || len(r.Target)>2048 ||
		strings.ContainsAny(r.Target,"\r\n\x00") || strings.TrimSpace(r.Target)!=r.Target {
		return fmt.Errorf("invalid notification destination")
	}
	switch r.Channel {
	case "email":
		a,err:=mail.ParseAddress(r.Target)
		if err!=nil || a.Address!=r.Target {return fmt.Errorf("invalid email destination")}
	case "telegram":
		// Telegram supports positive user IDs and negative group/chat IDs.
		start:=0
		if strings.HasPrefix(r.Target,"-") {start=1}
		if start==len(r.Target) || len(r.Target)>20 {return fmt.Errorf("invalid Telegram chat ID")}
		for _,x:=range r.Target[start:] {if x<'0'||x>'9' {return fmt.Errorf("invalid Telegram chat ID")}}
	case "webhook":
		u,err:=url.Parse(r.Target)
		if err!=nil || u.Scheme!="https" || u.Hostname()=="" ||
			u.User!=nil || u.RawQuery!="" || u.Fragment!="" ||
			u.Opaque!="" {return fmt.Errorf("webhook must be an HTTPS URL without userinfo, query or fragment")}
	default:
		return fmt.Errorf("unknown notification channel")
	}
	return nil
}

// ConfigureNotificationRoutes atomically replaces the enabled channels.
// Targets must be non-secret: credentials remain in worker environment.
// Invoke before incident evaluations; historic events are not backfilled.
func (p *Postgres) ConfigureNotificationRoutes(ctx context.Context, routes []NotificationRoute) error {
	if p==nil || p.db==nil {return fmt.Errorf("database unavailable")}
	if len(routes)==0 || len(routes)>3 {return fmt.Errorf("configure 1..3 routes")}
	seen:=make(map[string]bool)
	for _,r:=range routes {
		if seen[r.Channel] {return fmt.Errorf("duplicate notification channel")}
		if err:=validateNotificationRoute(r);err!=nil{return err}
		seen[r.Channel]=true
	}
	tx,err:=p.db.BeginTx(ctx,nil)
	if err!=nil{return err}
	defer tx.Rollback()
	// Serializes competing configurations; a single active fleet
	// configuration is assumed across workers.
	if _,err=tx.ExecContext(ctx,"SELECT pg_advisory_xact_lock(176341, 55)");err!=nil{return err}
	if _,err=tx.ExecContext(ctx,"UPDATE aegis_notification_routes SET enabled=FALSE");err!=nil{return err}
	for _,r:=range routes {
		_,err=tx.ExecContext(ctx,`INSERT INTO aegis_notification_routes(channel,target,enabled)
			VALUES($1,$2,TRUE) ON CONFLICT(channel) DO UPDATE
			SET target=EXCLUDED.target,enabled=TRUE,updated_at=clock_timestamp()`,r.Channel,r.Target)
		if err!=nil{return err}
	}
	return tx.Commit()
}

// ClaimNotification leases one due job. Row locks with SKIP LOCKED avoid
// overlapping claims by multiple workers. A crash eventually expires the lease.
// Attempts are capped; a worker that loses a lease cannot mark the job sent.
func (p *Postgres) ClaimNotification(ctx context.Context, channels []string) (*NotificationJob,error) {
	if p==nil || p.db==nil{return nil,fmt.Errorf("database unavailable")}
	if len(channels)==0{return nil,nil}
	for _,c:=range channels {if c!="email"&&c!="telegram"&&c!="webhook" {return nil,fmt.Errorf("invalid channel")}}
	// pgx stdlib accepts text[] through a PostgreSQL array literal.
	parts:=make([]string,len(channels))
	for i,c:=range channels {parts[i]=c}
	set:="{"+strings.Join(parts,",")+"}"
	tx,err:=p.db.BeginTx(ctx,nil)
	if err!=nil{return nil,err}
	defer tx.Rollback()
	var job NotificationJob
	err=tx.QueryRowContext(ctx,`
		WITH candidate AS (
			SELECT j.id FROM aegis_notification_jobs j
			WHERE j.channel=ANY($1::text[])
			  AND j.attempt_count<8
			  AND ((j.status IN ('pending','retry') AND j.next_attempt_at<=clock_timestamp())
			    OR (j.status='leased' AND j.leased_until<=clock_timestamp()))
			ORDER BY j.next_attempt_at,j.created_at,j.id
			FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE aegis_notification_jobs j SET
				status='leased', attempt_count=j.attempt_count+1,
				lease_token=gen_random_uuid(),leased_until=clock_timestamp()+interval '45 seconds'
			FROM candidate WHERE j.id=candidate.id
			RETURNING j.id::text,j.event_id,j.channel,j.target,
				j.attempt_count,j.lease_token::text
		)
		SELECT c.id,c.event_id,c.channel,c.target,c.attempt_count,c.lease_token,
			e.incident_id::text,e.action,i.kind,i.title,i.agent_id::text,i.severity
		FROM claimed c JOIN aegis_incident_events e ON e.id=c.event_id
		JOIN aegis_incidents i ON i.id=e.incident_id
	`,set).Scan(&job.ID,&job.EventID,&job.Channel,&job.Target,&job.Attempt,
		&job.LeaseToken,&job.IncidentID,&job.Action,&job.Kind,&job.Title,&job.AgentID,&job.Severity)
	if errors.Is(err,sql.ErrNoRows) {return nil,nil}
	if err!=nil{return nil,fmt.Errorf("claim notification: %w",err)}
	if err=tx.Commit();err!=nil{return nil,err}
	return &job,nil
}

// FinishNotification commits one attempt result and job transition together.
// errorCode MUST be a controlled classification without URLs, tokens or bodies.
func (p *Postgres) FinishNotification(ctx context.Context,job NotificationJob,delivered,permanent bool,errorCode string) error {
	if p==nil || p.db==nil{return fmt.Errorf("database unavailable")}
	if job.Attempt<1 || job.Attempt>8 || len(errorCode)>64 ||
		!safeErrorCode(errorCode) {return fmt.Errorf("invalid notification result")}
	status:="retry"
	if delivered {status="sent";errorCode=""} else if permanent || job.Attempt>=8 {status="dead"}
	delay:=notificationRetryDelay(job.Attempt)
	tx,err:=p.db.BeginTx(ctx,nil)
	if err!=nil{return err}
	defer tx.Rollback()
	result,err:=tx.ExecContext(ctx,`UPDATE aegis_notification_jobs SET
		status=$4,last_error_code=$5,
		lease_token=NULL,leased_until=NULL,
		next_attempt_at=clock_timestamp()+($6::bigint*interval '1 second'),
		sent_at=CASE WHEN $4='sent' THEN clock_timestamp() ELSE sent_at END
		WHERE id=$1 AND lease_token=$2::uuid AND attempt_count=$3 AND status='leased'`,
		job.ID,job.LeaseToken,job.Attempt,status,errorCode,int64(delay/time.Second))
	if err!=nil{return fmt.Errorf("finish notification: %w",err)}
	affected,err:=result.RowsAffected()
	if err!=nil{return err}
	if affected!=1 {return fmt.Errorf("notification lease lost")}
	_,err=tx.ExecContext(ctx,`INSERT INTO aegis_notification_attempts
		(job_id,attempt_number,outcome,error_code) VALUES($1,$2,$3,$4)`,
		job.ID,job.Attempt,status,errorCode)
	if err!=nil{return err}
	return tx.Commit()
}
func safeErrorCode(code string) bool {
	for _,r:=range code {
		if !(r>='a'&&r<='z'||r>='0'&&r<='9'||r=='_') {return false}
	}
	return true
}
func notificationRetryDelay(attempt int) time.Duration {
	if attempt<1 {attempt=1}
	if attempt>8 {attempt=8}
	// 15s, 30s, 60s ... maximum 1h (bounded exponential backoff).
	seconds:=int64(15) << (attempt-1)
	if seconds>3600 {seconds=3600}
	return time.Duration(seconds)*time.Second
}
func (p *Postgres) NotificationStats(ctx context.Context)(NotificationStats,error) {
	if p==nil || p.db==nil{return NotificationStats{},fmt.Errorf("database unavailable")}
	var v NotificationStats
	err:=p.db.QueryRowContext(ctx,`SELECT
		count(*) FILTER (WHERE status='pending'),
		count(*) FILTER (WHERE status='retry'),
		count(*) FILTER (WHERE status='leased'),
		count(*) FILTER (WHERE status='sent'),
		count(*) FILTER (WHERE status='dead')
		FROM aegis_notification_jobs`).Scan(&v.Pending,&v.Retry,&v.Leased,&v.Sent,&v.Dead)
	return v,err
}
