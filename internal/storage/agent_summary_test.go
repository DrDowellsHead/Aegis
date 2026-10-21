package storage

import (
    "context"
    "os"
    "testing"
    "time"
)

func TestCountAgentsPostgres(t *testing.T){
    dsn:=os.Getenv("AEGIS_TEST_DATABASE_URL")
    if dsn==""{t.Skip("requires AEGIS_TEST_DATABASE_URL")}
    ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second)
    defer cancel()
    db,err:=Open(ctx,dsn)
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)}
    online:=agentUUID(t)
    offline:=agentUUID(t)
    unknown:=agentUUID(t)
    if err:=db.SyncAgents(ctx,[]string{online,offline,unknown});err!=nil{t.Fatal(err)}
    _,err=db.db.ExecContext(ctx,`
        UPDATE aegis_agents SET last_contact_at=CASE agent_id
            WHEN $1::uuid THEN statement_timestamp()
            WHEN $2::uuid THEN statement_timestamp()-interval '2 hours'
            ELSE NULL END WHERE agent_id IN ($1,$2,$3)
    `,online,offline,unknown)
    if err!=nil{t.Fatal(err)}
    stats,err:=db.CountAgents(ctx,time.Minute)
    if err!=nil{t.Fatal(err)}
    if stats.Total!=3||stats.Online!=1||stats.Offline!=1||stats.Unknown!=1||
        stats.CheckedAt.IsZero()||stats.OfflineAfter!="1m0s"{
        t.Fatalf("unexpected counts: %+v",stats)
    }
    for _,bad:=range []time.Duration{time.Second,25*time.Hour,time.Minute+time.Nanosecond}{
        if _,err:=db.CountAgents(ctx,bad);err==nil{t.Errorf("accepted bad threshold: %v",bad)}
    }
}
