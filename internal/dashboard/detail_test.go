package dashboard

import (
    "encoding/base64"
    "encoding/json"
    "errors"
    "net/http"
    "strings"
    "testing"
    "time"

    "github.com/DrDowellsHead/Aegis/internal/metrics"
    "github.com/DrDowellsHead/Aegis/internal/storage"
)

const sampleID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDetailRoutesRequireSessionAndValidateQueries(t *testing.T){
    h,fake:=testDashboard(t)
    now:=time.Now().UTC().Truncate(time.Second)
    from:=now.Add(-time.Hour).Format(time.RFC3339)
    to:=now.Format(time.RFC3339)
    seriesURL:="/api/web/series?agent_id="+exampleAgent+"&from="+from+"&to="+to+"&step=1m"
    historyURL:="/api/web/measurements?agent_id="+exampleAgent+"&from="+from+"&to="+to+"&limit=2"
    for _,path:=range []string{seriesURL,historyURL}{
        if w:=request(h,"GET",path,"","",nil);w.Code!=http.StatusUnauthorized{
            t.Fatalf("unauthenticated read %s => %d",path,w.Code)
        }
    }
    if fake.seriesCalls!=0||fake.historyCalls!=0{t.Fatal("read before authorization")}
    cookie:=login(t,h)
    for _,path:=range []string{
        "/api/web/series?agent_id=invalid&from="+from+"&to="+to+"&step=1m",
        "/api/web/series?agent_id="+exampleAgent+"&from="+from+"&to="+to+"&step=bad",
        "/api/web/series?agent_id="+exampleAgent+"&from="+from+"&to="+to+"&step=1m&step=5m",
        "/api/web/series?agent_id="+exampleAgent+"&from="+from+"&to="+to+"&step=1m&surprise=1",
        "/api/web/series?agent_id="+exampleAgent+"&from="+to+"&to="+from+"&step=1m",
        "/api/web/measurements?agent_id=invalid",
        "/api/web/measurements?agent_id="+exampleAgent+"&limit=101",
        "/api/web/measurements?agent_id="+exampleAgent+"&cursor=bad",
        "/api/web/measurements?agent_id="+exampleAgent+"&limit=1&limit=2",
        "/api/web/measurements?agent_id="+exampleAgent+"&surprise=1",
    } {
        if w:=request(h,"GET",path,"","",cookie);w.Code!=http.StatusBadRequest{
            t.Errorf("%s: expected 400, got %d %s",path,w.Code,w.Body.String())
        }
    }
    if fake.seriesCalls!=0||fake.historyCalls!=0{t.Fatal("malformed queries reached storage")}
    if w:=request(h,"POST",seriesURL,"","",cookie);w.Code!=405{t.Fatal(w.Code)}
    fake.seriesResult=storage.SeriesResult{
        AgentID:exampleAgent,From:now.Add(-time.Hour),To:now,Step:"1m0s",
        Points:[]storage.SeriesPoint{{
            At:now.Add(-30*time.Minute),Samples:1,CPUSamples:1,CPUAvg:floatP(27.5),
            CPUMin:floatP(20),CPUMax:floatP(40),MemorySamples:0,MemoryAvg:nil,
            DiskSamples:0,DiskAvg:nil,
        }},
    }
    got:=request(h,"GET",seriesURL,"","",cookie)
    if got.Code!=200||fake.seriesCalls!=1||fake.seriesQuery.Step!=time.Minute {
        t.Fatalf("series status=%d calls=%d query=%+v",got.Code,fake.seriesCalls,fake.seriesQuery)
    }
    var parsed storage.SeriesResult
    if err:=json.Unmarshal(got.Body.Bytes(),&parsed);err!=nil||len(parsed.Points)!=1||
        parsed.Points[0].MemoryAvg!=nil||parsed.Points[0].DiskAvg!=nil{
        t.Fatalf("null metric semantics lost: %v %+v",err,parsed)
    }
    fake.historyResult=storage.HistoryResult{Items:[]storage.HistoryItem{{
        AgentID:exampleAgent,MeasurementID:sampleID,StartedAt:now.Add(-10*time.Minute),ReceivedAt:now,
        Snapshot:metrics.Snapshot{SchemaVersion:1,StartedAt:now.Add(-10*time.Minute),
            CPU:nil,Memory:nil,Disk:nil,
            Errors:map[string]string{"cpu":"permission denied"}},
    }},HasMore:true}
    got=request(h,"GET",historyURL,"","",cookie)
    if got.Code!=200||fake.historyCalls!=1{t.Fatalf("history %d calls=%d",got.Code,fake.historyCalls)}
    var page historyPage
    if err:=json.Unmarshal(got.Body.Bytes(),&page);err!=nil{t.Fatal(err)}
    if !page.HasMore||page.NextCursor==""||len(page.Items)!=1||page.Items[0].Snapshot.CPU!=nil{
        t.Fatalf("bad cursor or missing metric: %+v",page)
    }
    if _,err:=base64.RawURLEncoding.DecodeString(page.NextCursor);err!=nil{t.Fatal(err)}
    next:=request(h,"GET",historyURL+"&cursor="+page.NextCursor,"","",cookie)
    if next.Code!=200||fake.historyCalls!=2||fake.historyQuery.CursorID!=sampleID{
        t.Fatalf("cursor not forwarded: %d %+v",next.Code,fake.historyQuery)
    }
    mismatch:=request(h,"GET",historyURL+"&to="+from+"&cursor="+page.NextCursor,"","",cookie)
    if mismatch.Code!=400{t.Fatal("cursor with different interval was allowed")}
}
type historyPage struct{
    Items []storage.HistoryItem `json:"items"`
    HasMore bool `json:"has_more"`
    NextCursor string `json:"next_cursor"`
}
func floatP(v float64)*float64{return &v}

func TestDetailDatabaseErrorsNeverLeakSecrets(t *testing.T){
    h,fake:=testDashboard(t)
    c:=login(t,h)
    fake.err=errors.New("postgres://user:private-password@server")
    for _,path:=range []string{
        "/api/web/measurements?agent_id="+exampleAgent,
        "/api/web/series?agent_id="+exampleAgent+"&from=2026-10-01T00:00:00Z&to=2026-10-01T01:00:00Z&step=1m",
    }{
        w:=request(h,"GET",path,"","",c)
        if w.Code!=503||strings.Contains(w.Body.String(),"private-password")||
            w.Header().Get("Cache-Control")!="no-store"{
            t.Fatalf("database detail leaked: %d %s",w.Code,w.Body.String())
        }
    }
}
