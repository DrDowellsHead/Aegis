package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Client struct {
	conn *grpc.ClientConn
	svc pb.TelemetryServiceClient
	agent identity.Credentials
}

func NewClient(address string, agent identity.Credentials, caPath string) (*Client,error) {
	if !identity.ValidID(agent.AgentID)||len(agent.Token)!=64{return nil,fmt.Errorf("provisioned agent credentials required")}
	tlsCredentials,err:=ClientCredentials(caPath)
	if err!=nil{return nil,err}
	conn,err:=grpc.NewClient(address,grpc.WithTransportCredentials(tlsCredentials),
		grpc.WithPerRPCCredentials(bearer{token:agent.Token}))
	if err!=nil{return nil,fmt.Errorf("create TLS telemetry client: %w",err)}
	return &Client{conn:conn,svc:pb.NewTelemetryServiceClient(conn),agent:agent},nil
}

func(c *Client)Close()error{
	if c==nil||c.conn==nil{return nil}
	return c.conn.Close()
}

// Send returns nil ONLY for a durable database acknowledgement.
// A legacy server's received=true/durable=false MUST NOT delete local data.
func(c *Client)Send(ctx context.Context,measurementID string,snapshot metrics.Snapshot)error{
	if c==nil||c.svc==nil{return fmt.Errorf("telemetry client is nil")}
	if !validMeasurementID(measurementID){return status.Error(codes.InvalidArgument,"invalid measurement ID")}
	wire,err:=EncodeSnapshot(snapshot)
	if err!=nil{return status.Error(codes.InvalidArgument,err.Error())}
	if err:=ValidateSnapshot(wire);err!=nil{return status.Error(codes.InvalidArgument,err.Error())}
	response,err:=c.svc.Send(ctx,&pb.SendRequest{
		AgentId:c.agent.AgentID,MeasurementId:measurementID,Snapshot:wire,
	})
	if err!=nil{return fmt.Errorf("send telemetry: %w",err)}
	if response==nil||!response.GetDurable()||!response.GetReceived(){
		return status.Error(codes.Unavailable,"server did not confirm durable storage")
	}
	return nil
}

func SendOnce(ctx context.Context,address string,agent identity.Credentials,caPath string,snapshot metrics.Snapshot)(string,error){
	client,err:=NewClient(address,agent,caPath)
	if err!=nil{return "",err}
	defer client.Close()
	var random [16]byte
	if _,err:=rand.Read(random[:]);err!=nil{return "",fmt.Errorf("generate measurement ID: %w",err)}
	id:=hex.EncodeToString(random[:])
	callCtx,cancel:=context.WithTimeout(ctx,5*time.Second)
	defer cancel()
	if err:=client.Send(callCtx,id,snapshot);err!=nil{return "",err}
	return id,nil
}
