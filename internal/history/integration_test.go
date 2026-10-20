package history

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Exercises HTTP -> storage.ListHistory -> PostgreSQL -> protobuf -> JSON.
func TestPostgresHTTPHistory(t *testing.T){
	dsn:=os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn==""{t.Skip("AEGIS_TEST_DATABASE_URL not set")}
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second)
	defer cancel()
	db,err:=storage.Open(ctx,dsn)
	if err!=nil{t.Fatal(err)}
	defer db.Close()
	if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)}
	var idBytes [16]byte
	if _,err:=rand.Read(idBytes[:]);err!=nil{t.Fatal(err)}
	idBytes[6]=(idBytes[6]&0x0f)|0x40
	idBytes[8]=(idBytes[8]&0x3f)|0x80
	h:=hex.EncodeToString(idBytes[:])
	agentID:=h[:8]+"-"+h[8:12]+"-"+h[12:16]+"-"+h[16:20]+"-"+h[20:]
	now:=time.Date(2026,10,20,10,0,0,0,time.UTC)
	for i:=0;i<3;i++{
		var r [16]byte
		if _,err:=rand.Read(r[:]);err!=nil{t.Fatal(err)}
		wire:=&pb.Snapshot{SchemaVersion:1,StartedAt:timestamppb.New(now.Add(time.Duration(i)*time.Minute)),
			Os:"linux",Arch:"amd64",Cpu:&pb.CPU{UsedPercent:float64(i+10)}}
		if _,err:=db.Save(ctx,agentID,hex.EncodeToString(r[:]),wire);err!=nil{t.Fatal(err)}
	}
	handler,err:=NewHandler(db,operatorToken,slog.New(slog.NewTextHandler(io.Discard,nil)))
	if err!=nil{t.Fatal(err)}
	base:="/api/v1/measurements?agent_id="+agentID+"&limit=2"
	first:=request(handler,base,http.MethodGet,operatorToken)
	if first.Code!=200{t.Fatalf("status=%d body=%s",first.Code,first.Body.String())}
	var p1 response
	if err:=json.Unmarshal(first.Body.Bytes(),&p1);err!=nil{t.Fatal(err)}
	if len(p1.Items)!=2||!p1.HasMore||p1.NextCursor==""{
		t.Fatalf("bad first page: %+v",p1)
	}
	if p1.Items[0].Snapshot.CPU==nil||p1.Items[0].Snapshot.CPU.UsedPercent!=12 {
		t.Fatalf("protobuf not decoded: %+v",p1.Items[0].Snapshot)
	}
	second:=request(handler,base+"&cursor="+url.QueryEscape(p1.NextCursor),http.MethodGet,operatorToken)
	if second.Code!=200{t.Fatalf("status=%d body=%s",second.Code,second.Body.String())}
	var p2 response
	if err:=json.Unmarshal(second.Body.Bytes(),&p2);err!=nil{t.Fatal(err)}
	if len(p2.Items)!=1||p2.HasMore||p2.NextCursor!="" {
		t.Fatalf("bad second page: %+v",p2)
	}
	seen:=map[string]bool{}
	for _,v:=range append(p1.Items,p2.Items...){
		if seen[v.MeasurementID]{t.Fatal("duplicated measurement across pages")}
		seen[v.MeasurementID]=true
	}
	// Time is [from,to): the record exactly at to must not be returned.
	from,to:=now.Add(time.Minute).Format(time.RFC3339),now.Add(2*time.Minute).Format(time.RFC3339)
	timeURL:="/api/v1/measurements?agent_id="+agentID+"&from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to)
	w:=request(handler,timeURL,http.MethodGet,operatorToken)
	if w.Code!=200{t.Fatal(w.Code)}
	var filtered response
	if err:=json.Unmarshal(w.Body.Bytes(),&filtered);err!=nil{t.Fatal(err)}
	if len(filtered.Items)!=1||filtered.Items[0].Snapshot.CPU.UsedPercent!=11{
		t.Fatalf("time filter failed: %+v",filtered.Items)
	}
	// Ensure rejected unauthenticated calls never disclose history.
	denied:=httptest.NewRecorder()
	req:=httptest.NewRequest(http.MethodGet,base,nil)
	handler.ServeHTTP(denied,req)
	if denied.Code!=401{t.Fatalf("unauthenticated access: %d",denied.Code)}
}
