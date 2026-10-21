package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type fakeEvaluator struct {
	pages []storage.AgentResult
	seen []string
	listCalls int
	ruleFailures map[string]bool
	liveFailures map[string]bool
	cancel context.CancelFunc
}
func (f *fakeEvaluator) ListAgents(_ context.Context, q storage.AgentQuery) (storage.AgentResult, error) {
	if f.listCalls >= len(f.pages) {
		return storage.AgentResult{}, errors.New("unexpected list request")
	}
	wantCursor := ""
	if f.listCalls > 0 {
		wantCursor = f.pages[f.listCalls-1].NextCursor
	}
	if q.Cursor != wantCursor || q.Limit != BatchSize {
		return storage.AgentResult{}, errors.New("incorrect cursor or batch size")
	}
	page := f.pages[f.listCalls]
	f.listCalls++
	return page, nil
}
func (f *fakeEvaluator) EvaluateRulesAs(_ context.Context, id, actor string) ([]storage.RuleRecord, error) {
	if actor != "scheduler" { panic("scheduler actor not used for rule checks") }
	f.seen = append(f.seen, "rules:"+id)
	if f.cancel != nil {
		f.cancel()
	}
	if f.ruleFailures[id] { return nil, errors.New("rule unavailable") }
	return []storage.RuleRecord{{}}, nil
}
func (f *fakeEvaluator) EvaluateAgentLivenessAs(_ context.Context, id string, threshold time.Duration, actor string) (string, error) {
	if actor != "scheduler" || threshold != time.Minute {
		panic("bad scheduled liveness arguments")
	}
	f.seen = append(f.seen, "contact:"+id)
	if f.liveFailures[id] { return "", errors.New("contact unavailable") }
	return "online", nil
}
func TestCycleScansEveryPageAndIsolatesAgentErrors(t *testing.T) {
	f := &fakeEvaluator{
		pages: []storage.AgentResult{
			{Items: []storage.AgentInfo{{AgentID:"first"},{AgentID:"second"}}, HasMore:true, NextCursor:"second"},
			{Items: []storage.AgentInfo{{AgentID:"third"}}, HasMore:false},
		},
		ruleFailures: map[string]bool{"second":true},
		liveFailures: map[string]bool{"third":true},
	}
	var updates []storage.WatchProgress
	stats,err := Cycle(context.Background(),f,time.Minute,func(_ context.Context,p storage.WatchProgress)error {
		updates=append(updates,p)
		return nil
	})
	if err!=nil { t.Fatal(err) }
	if stats.AgentsChecked!=3 || stats.RulesChecked!=2 || stats.Errors!=2 {
		t.Fatalf("incorrect progress %+v",stats)
	}
	if len(updates)!=2 || updates[0].AgentsChecked!=2 || updates[1].AgentsChecked!=3 {
		t.Fatalf("missing progress reports: %+v",updates)
	}
	if len(f.seen)!=6 || f.seen[1]!="contact:first" || f.seen[3]!="contact:second" {
		t.Fatalf("liveness starved by rule failure: %+v",f.seen)
	}
}
func TestCycleRejectsBrokenPagination(t *testing.T) {
	f:=&fakeEvaluator{pages:[]storage.AgentResult{{Items:[]storage.AgentInfo{{AgentID:"first"}},
		HasMore:true,NextCursor:""}}}
	stats,err:=Cycle(context.Background(),f,time.Minute,nil)
	if err==nil||stats.Errors!=1 {t.Fatalf("bad pagination accepted: %+v %v",stats,err)}
}
func TestCycleCancellationPreventsFurtherEvaluation(t *testing.T) {
	ctx,cancel:=context.WithCancel(context.Background())
	defer cancel()
	f:=&fakeEvaluator{pages:[]storage.AgentResult{{Items:[]storage.AgentInfo{{AgentID:"first"},{AgentID:"second"}}}},
		cancel:cancel}
	_,err:=Cycle(ctx,f,time.Minute,nil)
	if !errors.Is(err,context.Canceled){t.Fatalf("expected canceled: %v",err)}
	if len(f.seen)!=1 {t.Fatalf("work continued after cancellation: %+v",f.seen)}
}
func TestCycleRejectsThreshold(t *testing.T){
	_,err:=Cycle(context.Background(),&fakeEvaluator{},time.Second,nil)
	if err==nil {t.Fatal("accepted too-short offline threshold")}
}
