package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type heartbeatTestStore struct {
	successStore
	count int
	err error
}
func (s *heartbeatTestStore) RecordHeartbeat(_ context.Context, _ string) error {
	s.count++
	return s.err
}

func TestHeartbeatAuthenticationAndDurability(t *testing.T) {
	dir := t.TempDir()
	registryPath, clientPath := filepath.Join(dir,"agents.json"),filepath.Join(dir,"client.json")
	if _,err := identity.Provision(registryPath,clientPath);err!=nil{t.Fatal(err)}
	creds,err:=identity.LoadCredentials(clientPath);if err!=nil{t.Fatal(err)}
	registry,err:=identity.LoadRegistry(registryPath);if err!=nil{t.Fatal(err)}
	store:=&heartbeatTestStore{}
	r:=&Receiver{
		logger:slog.New(slog.NewTextHandler(io.Discard,nil)),
		registry:registry,store:store,
	}
	valid:=metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization","Bearer "+creds.Token))
	req:=&pb.HeartbeatRequest{AgentId:creds.AgentID}
	if _,err:=r.Heartbeat(context.Background(),req);status.Code(err)!=codes.Unauthenticated{
		t.Fatalf("unauthenticated heartbeat accepted: %v",err)
	}
	if _,err:=r.Heartbeat(valid,nil);status.Code(err)!=codes.Unauthenticated{
		t.Fatalf("nil heartbeat accepted: %v",err)
	}
	if _,err:=r.Heartbeat(valid,&pb.HeartbeatRequest{AgentId:"invalid"});status.Code(err)!=codes.Unauthenticated{
		t.Fatalf("invalid agent ID accepted: %v",err)
	}
	wrong:=metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization","Bearer "+strings.Repeat("0",64)))
	if _,err:=r.Heartbeat(wrong,req);status.Code(err)!=codes.Unauthenticated{
		t.Fatalf("wrong token accepted: %v",err)
	}
	if store.count!=0{t.Fatal("unauthorized request reached database")}
	resp,err:=r.Heartbeat(valid,req)
	if err!=nil || !resp.GetDurable() || store.count!=1{t.Fatalf("good heartbeat: %v %v",resp,err)}
	store.err=storage.ErrAgentNotRegistered
	if _,err:=r.Heartbeat(valid,req);status.Code(err)!=codes.FailedPrecondition{
		t.Fatalf("unsynchronized registry: %v",err)
	}
	store.err=errors.New("sensitive postgres details")
	if _,err:=r.Heartbeat(valid,req);status.Code(err)!=codes.Unavailable || strings.Contains(err.Error(),"sensitive"){
		t.Fatalf("database secret disclosed: %v",err)
	}
}
