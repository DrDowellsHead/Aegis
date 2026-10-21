package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type watchStatusFake struct {
	*stub
	called int
	err error
}
func (f *watchStatusFake) GetWatchStatus(context.Context) (storage.WatchStatus,error) {
	f.called++
	if f.err!=nil {return storage.WatchStatus{},f.err}
	return storage.WatchStatus{State:"ok",AgentsChecked:2,RulesChecked:3,
		IntervalSeconds:30,OfflineAfterSeconds:60,Stale:false},nil
}
func TestWatchStatusAuthAndReadOnly(t *testing.T) {
	h:=testRemote(t)
	store:=&watchStatusFake{stub:&stub{}}
	h.reader=store
	if w:=remoteRequest(h,"GET","/api/web/watch/status","","",nil);w.Code!=401 {
		t.Fatalf("unauthenticated watch read: %d",w.Code)
	}
	viewer:=loginRemote(t,h,"observer","viewer-password-very-long")
	if w:=remoteRequest(h,"GET","/api/web/watch/status","","",viewer);w.Code!=200 ||
		!strings.Contains(w.Body.String(),`"agents_checked":2`) {
		t.Fatalf("viewer read failed: %d %s",w.Code,w.Body.String())
	}
	if w:=remoteRequest(h,"GET","/api/web/watch/status?secret=1","","",viewer);w.Code!=400{
		t.Fatalf("query accepted: %d",w.Code)
	}
	if w:=remoteRequest(h,"POST","/api/web/watch/status","{}",publicOriginForTest,viewer);w.Code!=405{
		t.Fatalf("mutation accepted: %d",w.Code)
	}
	if store.called!=1 {t.Fatalf("unexpected store calls: %d",store.called)}
}
func TestWatchStatusDatabaseErrorsNotLeaked(t *testing.T) {
	h,_:=testDashboard(t)
	store:=&watchStatusFake{stub:&stub{},err:errors.New("postgres://password-secret")}
	h.reader=store
	cookie:=login(t,h)
	w:=request(h,"GET","/api/web/watch/status","","",cookie)
	if w.Code!=http.StatusServiceUnavailable || strings.Contains(w.Body.String(),"password-secret"){
		t.Fatalf("error leaked to browser: %d %s",w.Code,w.Body.String())
	}
}
var _=time.Minute
