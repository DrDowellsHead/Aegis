package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

func openOperatorDatabase(ctx context.Context) (*storage.Postgres,error) {
	connectCtx,cancel:=context.WithTimeout(ctx,10*time.Second)
	defer cancel()
	db,err:=storage.Open(connectCtx,os.Getenv("AEGIS_DATABASE_URL"))
	if err!=nil{return nil,err}
	if err:=db.Ready(connectCtx);err!=nil{
		_ = db.Close()
		return nil,fmt.Errorf("schema not ready (run aegis migrate): %w",err)
	}
	return db,nil
}

// 'storage' performs a read-only overview. No automatic deletions.
func runStorage(ctx context.Context,args []string,out,errOut io.Writer) int {
	flags:=flag.NewFlagSet("storage",flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err:=flags.Parse(args);err!=nil {
		if errors.Is(err,flag.ErrHelp){return 0}
		return 2
	}
	if flags.NArg()!=0{
		fmt.Fprintln(errOut,"storage does not accept positional arguments")
		return 2
	}
	db,err:=openOperatorDatabase(ctx)
	if err!=nil{fmt.Fprintln(errOut,err);return 1}
	defer db.Close()
	queryCtx,cancel:=context.WithTimeout(ctx,30*time.Second)
	defer cancel()
	usage,err:=db.Usage(queryCtx)
	if err!=nil{fmt.Fprintln(errOut,err);return 1}
	if err:=json.NewEncoder(out).Encode(usage);err!=nil{
		fmt.Fprintln(errOut,"encode storage usage:",err)
		return 1
	}
	return 0
}

type retentionOutput struct{
	Mode string `json:"mode"`
	Preview storage.RetentionPreview `json:"preview"`
	DeletedRows int64 `json:"deleted_rows"`
	MaxDelete int `json:"max_delete,omitempty"`
	ReachedLimit bool `json:"reached_limit,omitempty"`
}

// Default is preview. Applying to ALL agents requires --all-agents explicitly.
// This is a one-shot operator command, not a background retention scheduler.
func runRetention(ctx context.Context,args []string,out,errOut io.Writer)int{
	flags:=flag.NewFlagSet("retention",flag.ContinueOnError)
	flags.SetOutput(errOut)
	days:=flags.Int("days",0,"retention period in whole days (7..3650; required)")
	agentID:=flags.String("agent-id","","optional UUID for one agent")
	all:=flags.Bool("all-agents",false,"explicitly authorize a global deletion")
	apply:=flags.Bool("apply",false,"actually delete rows (otherwise preview only)")
	batch:=flags.Int("batch-size",500,"rows per transaction (1..2000)")
	maxDelete:=flags.Int("max-delete",10000,"maximum rows deleted per invocation (1..1000000)")
	if err:=flags.Parse(args);err!=nil{
		if errors.Is(err,flag.ErrHelp){return 0}
		return 2
	}
	filter:=storage.RetentionFilter{Days:*days,AgentID:*agentID}
	if flags.NArg()!=0||filter.Validate()!=nil||*batch<1||*batch>storage.MaxRetentionBatch||
		*maxDelete<1||*maxDelete>1_000_000||(*all&&*agentID!="")||
		(*apply&&(*all==(*agentID!=""))) {
		fmt.Fprintln(errOut,"invalid arguments: --days 7..3650, --batch-size 1..2000, --max-delete 1..1000000; --apply requires exactly one of --agent-id or --all-agents")
		return 2
	}
	db,err:=openOperatorDatabase(ctx)
	if err!=nil{fmt.Fprintln(errOut,err);return 1}
	defer db.Close()
	previewCtx,cancel:=context.WithTimeout(ctx,60*time.Second)
	preview,err:=db.Preview(previewCtx,filter)
	cancel()
	if err!=nil{fmt.Fprintln(errOut,err);return 1}
	result:=retentionOutput{Mode:"preview",Preview:preview}
	if *apply {
		result.Mode="apply"
		result.MaxDelete=*maxDelete
		deleted,err:=db.Purge(ctx,preview,*batch,*maxDelete)
		result.DeletedRows=deleted
		result.ReachedLimit=deleted==int64(*maxDelete)
		if err!=nil{
			fmt.Fprintf(errOut,"retention interrupted after %d committed deletions: %v\n",deleted,err)
			_ = json.NewEncoder(out).Encode(result)
			return 1
		}
	}
	if err:=json.NewEncoder(out).Encode(result);err!=nil{
		fmt.Fprintln(errOut,"encode retention result:",err)
		return 1
	}
	return 0
}
