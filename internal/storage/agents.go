package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// SyncAgents reconciles the server's authoritative JSON registry with
// PostgreSQL. It never stores any authentication token or token digest.
// Agents removed from JSON are hidden (registered=false), never deleted
// from measurement history. All changes commit atomically.
func(p *Postgres) SyncAgents(ctx context.Context,ids []string)error{
	if p==nil||p.db==nil{return fmt.Errorf("database unavailable")}
	unique:=make(map[string]bool,len(ids))
	for _,id:=range ids{
		if !identity.ValidID(id){return fmt.Errorf("invalid registered agent ID")}
		if unique[id]{return fmt.Errorf("duplicate registered agent ID")}
		unique[id]=true
	}
	tx,err:=p.db.BeginTx(ctx,nil)
	if err!=nil{return fmt.Errorf("begin registry sync: %w",err)}
	defer tx.Rollback()
	if _,err:=tx.ExecContext(ctx,"UPDATE aegis_agents SET registered=FALSE WHERE registered=TRUE");err!=nil{
		return fmt.Errorf("disable old registrations: %w",err)
	}
	for _,id:=range ids{
		_,err:=tx.ExecContext(ctx,`
			INSERT INTO aegis_agents(agent_id,registered)
			VALUES($1,TRUE)
			ON CONFLICT(agent_id) DO UPDATE SET registered=TRUE
		`,id)
		if err!=nil{return fmt.Errorf("sync registered agent %s: %w",id,err)}
	}
	if err:=tx.Commit();err!=nil{return fmt.Errorf("commit registered agents: %w",err)}
	return nil
}

// AgentQuery uses UUID keyset pagination to keep the registry bounded.
// OfflineAfter measures the time elapsed since an authenticated, committed
// telemetry request (not proof that an OS process is currently running).
type AgentQuery struct{
	Limit int
	Cursor string
	OfflineAfter time.Duration
}

type AgentInfo struct{
	AgentID string `json:"agent_id"`
	Status string `json:"status"`
	RegisteredAt time.Time `json:"registered_at"`
	LastContactAt *time.Time `json:"last_contact_at"`
	LastSampleAt *time.Time `json:"last_sample_at"`
	OS string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
}

type AgentResult struct{
	Items []AgentInfo `json:"items"`
	HasMore bool `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	OfflineAfter string `json:"offline_after"`
}

func ValidateAgentQuery(q AgentQuery)error{
	if q.Limit<1||q.Limit>200||q.OfflineAfter<10*time.Second||
		q.OfflineAfter>24*time.Hour||q.OfflineAfter%time.Second!=0 ||
		(q.Cursor!=""&&!identity.ValidID(q.Cursor)){
		return fmt.Errorf("invalid agent list pagination or offline threshold")
	}
	return nil
}

const listAgentsSQL=`
SELECT agent_id::text, registered_at, last_contact_at, last_sample_at,
       COALESCE(last_os,''), COALESCE(last_arch,''),
       CASE WHEN last_contact_at IS NULL THEN 'unknown'
            WHEN last_contact_at >= statement_timestamp() - ($2::bigint * interval '1 second') THEN 'online'
            ELSE 'offline' END AS status
FROM aegis_agents
WHERE registered=TRUE AND ($1::uuid IS NULL OR agent_id > $1::uuid)
ORDER BY agent_id ASC
LIMIT $3
`

func timePointer(t sql.NullTime)*time.Time{
	if !t.Valid{return nil}
	value:=t.Time.UTC()
	return &value
}

func(p *Postgres)ListAgents(ctx context.Context,q AgentQuery)(AgentResult,error){
	if err:=ValidateAgentQuery(q);err!=nil{return AgentResult{},err}
	if p==nil||p.db==nil{return AgentResult{},fmt.Errorf("database unavailable")}
	var cursor any
	if q.Cursor!=""{cursor=q.Cursor}
	rows,err:=p.db.QueryContext(ctx,listAgentsSQL,cursor,int64(q.OfflineAfter/time.Second),q.Limit+1)
	if err!=nil{return AgentResult{},fmt.Errorf("query registered agents: %w",err)}
	defer rows.Close()
	result:=AgentResult{Items:make([]AgentInfo,0,q.Limit),OfflineAfter:q.OfflineAfter.String()}
	for rows.Next(){
		var info AgentInfo
		var contact,sample sql.NullTime
		if err:=rows.Scan(&info.AgentID,&info.RegisteredAt,&contact,&sample,
			&info.OS,&info.Arch,&info.Status);err!=nil{
			return AgentResult{},fmt.Errorf("scan registered agent: %w",err)
		}
		if len(result.Items)==q.Limit{
			result.HasMore=true
			break
		}
		info.RegisteredAt=info.RegisteredAt.UTC()
		info.LastContactAt=timePointer(contact)
		info.LastSampleAt=timePointer(sample)
		result.Items=append(result.Items,info)
	}
	if err:=rows.Err();err!=nil{return AgentResult{},fmt.Errorf("iterate registered agents: %w",err)}
	if result.HasMore&&len(result.Items)>0{
		result.NextCursor=result.Items[len(result.Items)-1].AgentID
	}
	return result,nil
}

