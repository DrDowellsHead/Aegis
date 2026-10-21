package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type activityFake struct {
	*stub
	called int
	filter storage.ActivityQuery
	err error
}
func (a *activityFake) ListActivity(_ context.Context,q storage.ActivityQuery)(storage.ActivityPage,error){
	a.called++
	a.filter=q
	if a.err!=nil{return storage.ActivityPage{},a.err}
	return storage.ActivityPage{Items:[]storage.ActivityEntry{{
		ID:12,At:time.Now().UTC(),Category:"incident",Action:"opened",
		Actor:"system",Summary:"CPU high",Details:map[string]any{"severity":"warning"},
	}}},nil
}

func TestActivityReadOnlyAuthorizationAndFilters(t *testing.T){
	h:=testRemote(t)
	store:=&activityFake{stub:&stub{}}
	h.reader=store
	if w:=remoteRequest(h,"GET","/api/web/activity","","",nil);w.Code!=401 {
		t.Fatalf("anonymous read allowed: %d",w.Code)
	}
	viewer:=loginRemote(t,h,"observer","viewer-password-very-long")
	if w:=remoteRequest(h,"GET","/api/web/activity?category=incident&action=opened&limit=12&actor=system",
		"","",viewer);w.Code!=200 || store.called!=1{
		t.Fatalf("viewer could not read activity: %d",w.Code)
	}
	if store.filter.Category!="incident"||store.filter.Action!="opened"||
		store.filter.Actor!="system"||store.filter.Limit!=12 {
		t.Fatalf("activity filter lost: %+v",store.filter)
	}
	if w:=remoteRequest(h,"POST","/api/web/activity","{}",publicOriginForTest,viewer);w.Code!=405{
		t.Fatalf("activity API unexpectedly writable: %d",w.Code)
	}
	if store.called!=1{t.Fatal("POST invoked activity store")}
}

func TestActivityRejectsBadFiltersAndRedactsErrors(t *testing.T){
	h,cookie:=testDashboard(t)
	store:=&activityFake{stub:&stub{}}
	h.reader=store
	for _,path:=range []string{
		"/api/web/activity?bad=1",
		"/api/web/activity?limit=0",
		"/api/web/activity?limit=101",
		"/api/web/activity?category=wat",
		"/api/web/activity?action=wat",
		"/api/web/activity?actor=bad%20actor",
		"/api/web/activity?cursor=what",
		"/api/web/activity?cursor=0",
		"/api/web/activity?search=x",
		"/api/web/activity?agent_id=invalid",
		"/api/web/activity?category=incident&category=operator",
		"/api/web/activity?from=wrong-date",
	}{
		w:=request(h,"GET",path,"","",cookie)
		if w.Code!=400 {t.Fatalf("%s: expected 400 got %d",path,w.Code)}
	}
	if store.called!=0{t.Fatalf("malformed queries reached DB: %d",store.called)}
	store.err=errors.New("postgres://user:secretpassword@db")
	w:=request(h,"GET","/api/web/activity","","",cookie)
	if w.Code!=http.StatusServiceUnavailable||strings.Contains(w.Body.String(),"secretpassword"){
		t.Fatalf("database secret exposed: %d %s",w.Code,w.Body.String())
	}
}
