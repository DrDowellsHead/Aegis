package storage

import (
	"context"
	"sync"
	"testing"
	"time"
)

func setupNotifyRoutes(t *testing.T,p *Postgres,ctx context.Context) {
	t.Helper()
	routes:=[]NotificationRoute{
		{Channel:"email",Target:"ops@example.com"},
		{Channel:"telegram",Target:"-123456"},
		{Channel:"webhook",Target:"https://alerts.example.org/aegis"},
	}
	if err:=p.ConfigureNotificationRoutes(ctx,routes);err!=nil {t.Fatal(err)}
	t.Cleanup(func(){
		_,err:=p.db.ExecContext(context.Background(),
			"UPDATE aegis_notification_routes SET enabled=FALSE")
		if err!=nil {t.Errorf("disable notification routes: %v",err)}
	})
}

func TestNotificationRouteValidation(t *testing.T) {
	for _,r:=range []NotificationRoute{
		{Channel:"email",Target:"ops@example.com"},
		{Channel:"telegram",Target:"-123456"},
		{Channel:"webhook",Target:"https://example.org/hooks"},
	} {
		if err:=validateNotificationRoute(r);err!=nil {t.Fatal(err)}
	}
	for _,r:=range []NotificationRoute{
		{Channel:"email",Target:"hello\r\nBCC:bad@example.com"},
		{Channel:"telegram",Target:"abc"},
		{Channel:"webhook",Target:"http://example.org/path"},
		{Channel:"webhook",Target:"https://user:password@example.org"},
		{Channel:"bad",Target:"someone"},
	} {
		if err:=validateNotificationRoute(r);err==nil {t.Fatalf("accepted %+v",r)}
	}
	for i,want:=range []time.Duration{15*time.Second,30*time.Second,60*time.Second,120*time.Second}{
		if got:=notificationRetryDelay(i+1);got!=want {t.Fatalf("attempt %d: %v",i+1,got)}
	}
	if notificationRetryDelay(8)>time.Hour {t.Fatal("unbounded retry")}
}

func TestNotificationTransactionalOutboxPostgres(t *testing.T) {
	db,ctx,id:=incidentDB(t)
	setupNotifyRoutes(t,db,ctx)
	_,err:=db.db.ExecContext(ctx,`UPDATE aegis_agents SET
		last_contact_at=clock_timestamp()-interval '2 hours' WHERE agent_id=$1`,id)
	if err!=nil {t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil{t.Fatal(err)}
	inc:=incidentList(t,db,ctx,id,"active")
	if len(inc)!=1 {t.Fatalf("missing incident: %+v",inc)}
	stats,err:=db.NotificationStats(ctx)
	if err!=nil || stats.Pending<3 {t.Fatalf("outbox not inserted: %+v %v",stats,err)}
	// Acknowledgment is an audit event only; opening and resolution
	// generate deliveries; no duplicate jobs on repeated checks.
	if _,err=db.ChangeIncident(ctx,inc[0].ID,"acknowledge","operator");err!=nil{t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil{t.Fatal(err)}
	var n int
	err=db.db.QueryRowContext(ctx,`SELECT count(*) FROM aegis_notification_jobs j
		JOIN aegis_incident_events e ON e.id=j.event_id
		WHERE e.incident_id=$1`,inc[0].ID).Scan(&n)
	if err!=nil || n!=3 {t.Fatalf("duplicate or acknowledged delivery: %d %v",n,err)}
	// Concurrent workers see distinct leased jobs.
	var wg sync.WaitGroup
	jobs:=make(chan *NotificationJob,3)
	errs:=make(chan error,3)
	for i:=0;i<3;i++{
		wg.Add(1)
		go func(){
			defer wg.Done()
			job,err:=db.ClaimNotification(ctx,[]string{"email","telegram","webhook"})
			jobs<-job;errs<-err
		}()
	}
	wg.Wait();close(jobs);close(errs)
	ids:=make(map[string]bool)
	for e:=range errs {if e!=nil{t.Fatal(e)}}
	for j:=range jobs {
		if j==nil || ids[j.ID] {t.Fatalf("duplicate or missing lease: %+v",j)}
		ids[j.ID]=true
		if err=db.FinishNotification(ctx,*j,true,false,"");err!=nil {t.Fatal(err)}
		if err=db.FinishNotification(ctx,*j,true,false,"");err==nil {
			t.Fatal("lease accepted twice")
		}
	}
	if len(ids)!=3 {t.Fatalf("not all messages claimed: %d",len(ids))}
	if _,err=db.RecordHeartbeat(ctx,id);err!=nil{t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil{t.Fatal(err)}
	err=db.db.QueryRowContext(ctx,`SELECT count(*) FROM aegis_notification_jobs j
		JOIN aegis_incident_events e ON e.id=j.event_id
		WHERE e.incident_id=$1`,inc[0].ID).Scan(&n)
	if err!=nil || n!=6 {t.Fatalf("resolution not enqueued: %d %v",n,err)}
}

func TestNotificationRetriesAndCrashedLeasePostgres(t *testing.T) {
	db,ctx,id:=incidentDB(t)
	setupNotifyRoutes(t,db,ctx)
	_,err:=db.db.ExecContext(ctx,`UPDATE aegis_agents SET
		last_contact_at=clock_timestamp()-interval '2 hours' WHERE agent_id=$1`,id)
	if err!=nil{t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil{t.Fatal(err)}
	job,err:=db.ClaimNotification(ctx,[]string{"webhook"})
	if err!=nil||job==nil{t.Fatalf("claim: %+v %v",job,err)}
	if err=db.FinishNotification(ctx,*job,false,false,"network_error");err!=nil{t.Fatal(err)}
	if next,err:=db.ClaimNotification(ctx,[]string{"webhook"});err!=nil||next!=nil {
		t.Fatalf("backoff bypassed: %+v %v",next,err)
	}
	_,err=db.db.ExecContext(ctx,"UPDATE aegis_notification_jobs SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1",job.ID)
	if err!=nil{t.Fatal(err)}
	retry,err:=db.ClaimNotification(ctx,[]string{"webhook"})
	if err!=nil||retry==nil||retry.Attempt!=2{t.Fatalf("retry: %+v %v",retry,err)}
	if err=db.FinishNotification(ctx,*retry,false,true,"remote_rejected");err!=nil{t.Fatal(err)}
	var outcome string
	err=db.db.QueryRowContext(ctx,"SELECT status FROM aegis_notification_jobs WHERE id=$1",job.ID).Scan(&outcome)
	if err!=nil||outcome!="dead"{t.Fatalf("permanent failure: %s %v",outcome,err)}
	var attempts int
	err=db.db.QueryRowContext(ctx,"SELECT count(*) FROM aegis_notification_attempts WHERE job_id=$1",job.ID).Scan(&attempts)
	if err!=nil||attempts!=2{t.Fatalf("missing attempts journal: %d %v",attempts,err)}
	other,err:=db.ClaimNotification(ctx,[]string{"email"})
	if err!=nil||other==nil{t.Fatalf("second claim: %v %v",other,err)}
	_,err=db.db.ExecContext(ctx,`UPDATE aegis_notification_jobs SET
		attempt_count=8,leased_until=clock_timestamp()-interval '1 second'
		WHERE id=$1`,other.ID)
	if err!=nil{t.Fatal(err)}
	_,err=db.ClaimNotification(ctx,[]string{"email"})
	if err!=nil{t.Fatal(err)}
	err=db.db.QueryRowContext(ctx,"SELECT status FROM aegis_notification_jobs WHERE id=$1",other.ID).Scan(&outcome)
	if err!=nil||outcome!="dead"{t.Fatalf("last crashed lease stuck: %s %v",outcome,err)}
}
