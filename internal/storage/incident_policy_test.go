package storage

import (
	"context"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/alerts"
)

func TestIncidentPolicyBounds(t *testing.T) {
	valid:=[]IncidentSettings{
		{CooldownSeconds:0,EscalateAfterSeconds:60},
		{CooldownSeconds:86400,EscalateAfterSeconds:604800},
	}
	for _,v:=range valid {if err:=validateIncidentSettings(v);err!=nil {t.Fatal(err)}}
	invalid:=[]IncidentSettings{
		{CooldownSeconds:-1,EscalateAfterSeconds:900},
		{CooldownSeconds:86401,EscalateAfterSeconds:900},
		{CooldownSeconds:300,EscalateAfterSeconds:59},
		{CooldownSeconds:300,EscalateAfterSeconds:604801},
	}
	for _,v:=range invalid {if validateIncidentSettings(v)==nil {t.Fatalf("accepted bad policy: %+v",v)}}
}

func TestOfflineCooldownAndEscalationPostgres(t *testing.T) {
	db,ctx,id:=incidentDB(t)
	setIncidentPolicyForTest(t,db,ctx,300,60)
	_,err:=db.db.ExecContext(ctx,`UPDATE aegis_agents SET last_contact_at=
		clock_timestamp()-interval '2 hours' WHERE agent_id=$1`,id)
	if err!=nil {t.Fatal(err)}
	if state,err:=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil || state!="offline" {t.Fatalf("offline: %s %v",state,err)}
	open:=incidentList(t,db,ctx,id,"active")
	if len(open)!=1 || open[0].Severity!="warning" || open[0].EscalatedAt!=nil {t.Fatalf("initial severity: %+v",open)}
	// Change the age rather than sleeping. The next evaluation sees a
	// genuinely overdue and still-unacknowledged condition.
	_,err=db.db.ExecContext(ctx,`UPDATE aegis_incidents SET opened_at=clock_timestamp()-
		interval '2 minutes' WHERE id=$1`,open[0].ID)
	if err!=nil {t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil {t.Fatal(err)}
	after:=incidentList(t,db,ctx,id,"active")
	if len(after)!=1 || after[0].Severity!="critical" || after[0].EscalatedAt==nil {
		t.Fatalf("overdue offline episode did not escalate: %+v",after)
	}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil {t.Fatal(err)}
	events,err:=db.ListIncidentEvents(ctx,open[0].ID)
	if err!=nil || len(events)!=2 || events[1].Action!="escalated" {
		t.Fatalf("escalation repeated or missing: %+v %v",events,err)
	}
	if _,err=db.ChangeIncident(ctx,open[0].ID,"resolve","operator");err!=nil {t.Fatal(err)}
	for i:=0;i<3;i++ {
		if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil {t.Fatal(err)}
	}
	if len(incidentList(t,db,ctx,id,"active"))!=0 {t.Fatal("cooldown failed")}
	resolved:=incidentList(t,db,ctx,id,"resolved")
	if len(resolved)!=1 || resolved[0].SuppressedChecks!=3 || resolved[0].LastSuppressedAt==nil {
		t.Fatalf("suppressed count not persisted: %+v",resolved)
	}
	// Pass the cooldown by adjusting persisted resolution time. A new
	// episode must be allowed, with a fresh UUID and fresh severity.
	_,err=db.db.ExecContext(ctx,`UPDATE aegis_incidents SET
		opened_at=clock_timestamp()-interval '10 minutes',
		resolved_at=clock_timestamp()-interval '6 minutes'
		WHERE id=$1`,open[0].ID)
	if err!=nil {t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil {t.Fatal(err)}
	reopened:=incidentList(t,db,ctx,id,"active")
	if len(reopened)!=1 || reopened[0].ID==open[0].ID || reopened[0].Severity!="warning" {
		t.Fatalf("cooldown expiration failed: %+v",reopened)
	}
}
func TestAcknowledgedDoesNotEscalatePostgres(t *testing.T) {
	db,ctx,id:=incidentDB(t)
	setIncidentPolicyForTest(t,db,ctx,300,60)
	_,err:=db.db.ExecContext(ctx,`UPDATE aegis_agents SET last_contact_at=
		clock_timestamp()-interval '2 hours' WHERE agent_id=$1`,id)
	if err!=nil {t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil {t.Fatal(err)}
	open:=incidentList(t,db,ctx,id,"active")
	if _,err=db.ChangeIncident(ctx,open[0].ID,"acknowledge","operator");err!=nil {t.Fatal(err)}
	_,err=db.db.ExecContext(ctx,`UPDATE aegis_incidents SET opened_at=
		clock_timestamp()-interval '2 minutes' WHERE id=$1`,open[0].ID)
	if err!=nil {t.Fatal(err)}
	if _,err=db.EvaluateAgentLiveness(ctx,id,time.Minute);err!=nil {t.Fatal(err)}
	result:=incidentList(t,db,ctx,id,"active")
	if len(result)!=1 || result[0].State!="acknowledged" ||
		result[0].Severity!="warning" || result[0].EscalatedAt!=nil {
		t.Fatalf("acknowledged episode escalated: %+v",result)
	}
}
func TestThresholdCooldownAfterNaturalRecoveryPostgres(t *testing.T) {
	db,ctx,id:=incidentDB(t)
	setIncidentPolicyForTest(t,db,ctx,300,900)
	_,err:=db.CreateRule(ctx,RuleInput{AgentID:id,Rule:alerts.Rule{
		Name:"High CPU",Metric:"cpu",Direction:"above",FireAt:85,RecoverAt:70,
		WindowSeconds:60,MinSamples:1,MaxAgeSeconds:60,
	}})
	if err!=nil {t.Fatal(err)}
	makeCPU(t,db,ctx,id,95)
	if _,err=db.EvaluateRules(ctx,id);err!=nil {t.Fatal(err)}
	first:=incidentList(t,db,ctx,id,"active")
	if len(first)!=1 {t.Fatal("missing first threshold episode")}
	_,err=db.db.ExecContext(ctx,"DELETE FROM aegis_measurements WHERE agent_id=$1",id)
	if err!=nil {t.Fatal(err)}
	makeCPU(t,db,ctx,id,50)
	if _,err=db.EvaluateRules(ctx,id);err!=nil {t.Fatal(err)}
	_,err=db.db.ExecContext(ctx,"DELETE FROM aegis_measurements WHERE agent_id=$1",id)
	if err!=nil {t.Fatal(err)}
	makeCPU(t,db,ctx,id,95)
	if _,err=db.EvaluateRules(ctx,id);err!=nil {t.Fatal(err)}
	if got:=incidentList(t,db,ctx,id,"active");len(got)!=0 {
		t.Fatalf("flapping threshold bypassed cooldown: %+v",got)
	}
	old:=incidentList(t,db,ctx,id,"resolved")
	if len(old)!=1 || old[0].SuppressedChecks!=1 {t.Fatalf("no suppressed record: %+v",old)}
}
func TestIncidentPolicyUpdatePostgres(t *testing.T) {
	db,ctx,_:=incidentDB(t)
	setIncidentPolicyForTest(t,db,ctx,0,900)
	if _,err:=db.UpdateIncidentSettings(ctx,IncidentSettings{CooldownSeconds:100000,EscalateAfterSeconds:900});err==nil {
		t.Fatal("accepted out of bounds policy")
	}
	got,err:=db.UpdateIncidentSettings(ctx,IncidentSettings{CooldownSeconds:120,EscalateAfterSeconds:600})
	if err!=nil || got.CooldownSeconds!=120 || got.EscalateAfterSeconds!=600 || got.UpdatedAt.IsZero() {
		t.Fatalf("policy update: %+v %v",got,err)
	}
	read,err:=db.GetIncidentSettings(ctx)
	if err!=nil || read.CooldownSeconds!=120 || read.EscalateAfterSeconds!=600 {
		t.Fatalf("policy did not persist: %+v %v",read,err)
	}
}

