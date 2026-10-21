package storage

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// SecurityEvent never includes passwords, bearer tokens, session IDs,
// remote IPs or arbitrary request bodies. Events are append-only at the
// application layer and independent of the incident activity journal.
type SecurityEvent struct {
	ID int64 `json:"id"`
	At time.Time `json:"at"`
	Action string `json:"action"`
	Actor string `json:"actor"`
	Subject string `json:"subject"`
	Role string `json:"role"`
	AffectedSessions int `json:"affected_sessions"`
}
type SecurityEventInput struct {
	Action string
	Actor string
	Subject string
	Role string
	AffectedSessions int
}
type SecurityEventQuery struct {
	Action string
	Actor string
	Cursor string
	Limit int
}
type SecurityEventPage struct {
	Items []SecurityEvent `json:"items"`
	HasMore bool `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}
func ValidSecurityAction(action string) bool {
	switch action {
	case "login_ok","login_failed","login_throttled","logout","sessions_revoked","admin_denied":
		return true
	}
	return false
}
func securityIdentity(s string,empty bool) bool {
	if s == "" {return empty}
	if len(s)>32 {return false}
	for _,c:=range s {
		if !(c>='a'&&c<='z'||c>='0'&&c<='9'||c=='.'||c=='_'||c=='-'){
			return false
		}
	}
	return true
}
func ValidateSecurityEvent(v SecurityEventInput) error {
	if !ValidSecurityAction(v.Action)||!securityIdentity(v.Actor,false)||
		!securityIdentity(v.Subject,true)||
		(v.Role!=""&&v.Role!="admin"&&v.Role!="viewer")||
		v.AffectedSessions<0||v.AffectedSessions>128 {
		return fmt.Errorf("invalid security event metadata")
	}
	return nil
}
func ValidateSecurityQuery(q SecurityEventQuery) error {
	if q.Limit<1||q.Limit>100||q.Action!=""&&!ValidSecurityAction(q.Action)||
		!securityIdentity(q.Actor,true) {return fmt.Errorf("invalid security audit query")}
	if q.Cursor!=""{
		num,e:=strconv.ParseInt(q.Cursor,10,64)
		if e!=nil||num<=0||len(q.Cursor)>19{return fmt.Errorf("invalid security audit cursor")}
	}
	return nil
}
func(p *Postgres) RecordSecurityEvent(ctx context.Context,v SecurityEventInput)error{
	if err:=ValidateSecurityEvent(v);err!=nil{return err}
	if p==nil||p.db==nil{return fmt.Errorf("database unavailable")}
	_,err:=p.db.ExecContext(ctx,`
		INSERT INTO aegis_security_events (action,actor,subject,role,affected_sessions)
		VALUES($1,$2,$3,$4,$5)`,v.Action,v.Actor,v.Subject,v.Role,v.AffectedSessions)
	if err!=nil{return fmt.Errorf("record security event: %w",err)}
	return nil
}
func(p *Postgres) ListSecurityEvents(ctx context.Context,q SecurityEventQuery)(SecurityEventPage,error){
	if err:=ValidateSecurityQuery(q);err!=nil{return SecurityEventPage{},err}
	if p==nil||p.db==nil{return SecurityEventPage{},fmt.Errorf("database unavailable")}
	var cursor int64
	if q.Cursor!=""{cursor,_=strconv.ParseInt(q.Cursor,10,64)}
	rows,err:=p.db.QueryContext(ctx,`
		SELECT id,occurred_at,action,actor,subject,role,affected_sessions
		FROM aegis_security_events
		WHERE ($1::text='' OR action=$1) AND ($2::text='' OR actor=$2)
		 AND ($3::bigint=0 OR id<$3)
		ORDER BY id DESC LIMIT $4`,q.Action,q.Actor,cursor,q.Limit+1)
	if err!=nil{return SecurityEventPage{},fmt.Errorf("read security events: %w",err)}
	defer rows.Close()
	page:=SecurityEventPage{Items:make([]SecurityEvent,0,q.Limit)}
	for rows.Next(){
		var e SecurityEvent
		if err=rows.Scan(&e.ID,&e.At,&e.Action,&e.Actor,&e.Subject,
			&e.Role,&e.AffectedSessions);err!=nil{return SecurityEventPage{},err}
		e.At=e.At.UTC()
		if len(page.Items)==q.Limit{page.HasMore=true;break}
		page.Items=append(page.Items,e)
	}
	if err=rows.Err();err!=nil{return SecurityEventPage{},err}
	if page.HasMore{page.NextCursor=strconv.FormatInt(page.Items[len(page.Items)-1].ID,10)}
	return page,nil
}
