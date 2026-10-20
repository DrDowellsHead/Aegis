package telemetry

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Uses a real PostgreSQL service and the real TLS/gRPC boundary.
func TestPostgresGRPCDurableACK(t *testing.T) {
	dsn:=os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn==""{t.Skip("AEGIS_TEST_DATABASE_URL not set")}
	ctx,cancel:=context.WithTimeout(context.Background(),25*time.Second)
	defer cancel()
	db,err:=storage.Open(ctx,dsn)
	if err!=nil{t.Fatal(err)}
	defer db.Close()
	if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)}
	dir:=t.TempDir()
	ca,cert,key:=filepath.Join(dir,"ca.crt"),filepath.Join(dir,"server.crt"),filepath.Join(dir,"server.key")
	if err:=identity.GenerateDevelopmentTLS("127.0.0.1",ca,cert,key);err!=nil{t.Fatal(err)}
	registryFile,credentialsFile:=filepath.Join(dir,"agents.json"),filepath.Join(dir,"agent.json")
	if _,err:=identity.Provision(registryFile,credentialsFile);err!=nil{t.Fatal(err)}
	registry,err:=identity.LoadRegistry(registryFile)
	if err!=nil{t.Fatal(err)}
	credentials,err:=identity.LoadCredentials(credentialsFile)
	if err!=nil{t.Fatal(err)}
	tlsCred,err:=ServerCredentials(cert,key)
	if err!=nil{t.Fatal(err)}
	listener,err:=net.Listen("tcp","127.0.0.1:0")
	if err!=nil{t.Fatal(err)}
	srvCtx,stop:=context.WithCancel(ctx)
	defer stop()
	done:=make(chan error,1)
	go func(){done<-ServeReceiver(srvCtx,listener,slog.New(slog.NewTextHandler(io.Discard,nil)),tlsCred,registry,db)}()
	client,err:=NewClient(listener.Addr().String(),credentials,ca)
	if err!=nil{t.Fatal(err)}
	defer client.Close()
	var raw [16]byte
	if _,err:=rand.Read(raw[:]);err!=nil{t.Fatal(err)}
	id:=hex.EncodeToString(raw[:])
	s:=sample()
	if err:=client.Send(ctx,id,s);err!=nil{t.Fatal(err)}
	if err:=client.Send(ctx,id,s);err!=nil{t.Fatalf("durable duplicate ACK failed: %v",err)}
	s.CPU.UsedPercent=99
	if err:=client.Send(ctx,id,s);status.Code(err)!=codes.FailedPrecondition{
		t.Fatalf("conflicting duplicate accepted: %v",err)
	}
	direct,err:=sql.Open("pgx",dsn)
	if err!=nil{t.Fatal(err)}
	defer direct.Close()
	var count int
	if err:=direct.QueryRowContext(ctx,
		"SELECT count(*) FROM aegis_measurements WHERE agent_id=$1 AND measurement_id=$2",
		credentials.AgentID,id).Scan(&count);err!=nil{t.Fatal(err)}
	if count!=1{t.Fatalf("expected 1 row, got %d",count)}
	stop()
	select{
	case err:=<-done:if err!=nil{t.Fatal(err)}
	case <-time.After(7*time.Second):t.Fatal("server shutdown timeout")
	}
}
