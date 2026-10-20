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
)

// Client reuses one TLS gRPC connection across multiple sends and retries.
// Close releases connection resources when forwarding ends.
type Client struct {
	conn *grpc.ClientConn
	svc pb.TelemetryServiceClient
	agent identity.Credentials
}

func NewClient(address string, agent identity.Credentials, caPath string) (*Client,error) {
	if !identity.ValidID(agent.AgentID) || len(agent.Token)!=64 {
		return nil,fmt.Errorf("provisioned agent credentials required")
	}
	tlsCredentials,err:=ClientCredentials(caPath)
	if err!=nil{return nil,err}
	conn,err:=grpc.NewClient(address,
		grpc.WithTransportCredentials(tlsCredentials),
		grpc.WithPerRPCCredentials(bearer{token:agent.Token}))
	if err!=nil{return nil,fmt.Errorf("create telemetry client: %w",err)}
	return &Client{conn:conn,svc:pb.NewTelemetryServiceClient(conn),agent:agent},nil
}

func(c *Client)Close()error {
	if c==nil||c.conn==nil{return nil}
	return c.conn.Close()
}

// Send preserves the caller's measurement ID on all retry attempts.
// A nil error means only that the server validated and received the message.
func(c *Client)Send(ctx context.Context,measurementID string,snapshot metrics.Snapshot)error{
	if c==nil||c.svc==nil{return fmt.Errorf("telemetry client is nil")}
	if len(measurementID)!=32 {
		return fmt.Errorf("measurement ID must contain 32 hexadecimal digits")
	}
	for _,ch:=range measurementID {
		if !(ch>='0'&&ch<='9'||ch>='a'&&ch<='f'){
			return fmt.Errorf("measurement ID must be lowercase hex")
		}
	}
	wire,err:=EncodeSnapshot(snapshot)
	if err!=nil{return err}
	if err:=ValidateSnapshot(wire);err!=nil{return err}
	response,err:=c.svc.Send(ctx,&pb.SendRequest{
		AgentId:c.agent.AgentID,MeasurementId:measurementID,Snapshot:wire,
	})
	if err!=nil{return fmt.Errorf("send telemetry: %w",err)}
	if !response.GetReceived(){return fmt.Errorf("server did not acknowledge reception")}
	return nil
}

func SendOnce(ctx context.Context,address string,agent identity.Credentials,caPath string,snapshot metrics.Snapshot)(string,error){
	client,err:=NewClient(address,agent,caPath)
	if err!=nil{return "",err}
	defer client.Close()
	var random [16]byte
	if _,err:=rand.Read(random[:]);err!=nil{return "",fmt.Errorf("generate measurement ID: %w",err)}
	measurementID:=hex.EncodeToString(random[:])
	callCtx,cancel:=context.WithTimeout(ctx,5*time.Second)
	defer cancel()
	if err:=client.Send(callCtx,measurementID,snapshot);err!=nil{return "",err}
	return measurementID,nil
}
