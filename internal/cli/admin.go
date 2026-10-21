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

    "github.com/DrDowellsHead/Aegis/internal/adminconfig"
    "github.com/DrDowellsHead/Aegis/internal/storage"
)

// AEGIS_OPERATOR is self-declared until an authenticated administrator
// identity is introduced. It is not proof of the user's OS identity.
func adminOperator()string{
    actor:=os.Getenv("AEGIS_OPERATOR")
    if actor==""{return "local-operator"}
    return actor
}

func runAdminConfig(args []string,out,errOut io.Writer)int{
    flags:=flag.NewFlagSet("config",flag.ContinueOnError)
    flags.SetOutput(errOut)
    path:=flags.String("file",os.Getenv("AEGIS_ADMIN_CONFIG"),"non-secret admin JSON configuration")
    if err:=flags.Parse(args);err!=nil{
        if errors.Is(err,flag.ErrHelp){return 0}
        return 2
    }
    if flags.NArg()!=0{fmt.Fprintln(errOut,"config does not accept positional arguments");return 2}
    cfg,err:=adminconfig.Load(*path)
    if err!=nil{fmt.Fprintln(errOut,"invalid administrative configuration:",err);return 1}
    if err:=json.NewEncoder(out).Encode(cfg);err!=nil{fmt.Fprintln(errOut,"write config report:",err);return 1}
    return 0
}

type doctorReport struct{
    ConfigValid bool `json:"config_valid"`
    DatabaseConnected bool `json:"database_connected"`
    SchemaReady bool `json:"schema_ready"`
    AuditReadable bool `json:"audit_readable"`
    Status string `json:"status"`
    Checks []string `json:"checks"`
}

func runDoctor(ctx context.Context,args []string,out,errOut io.Writer)int{
    flags:=flag.NewFlagSet("doctor",flag.ContinueOnError)
    flags.SetOutput(errOut)
    if err:=flags.Parse(args);err!=nil{
        if errors.Is(err,flag.ErrHelp){return 0}
        return 2
    }
    if flags.NArg()!=0{fmt.Fprintln(errOut,"doctor does not accept positional arguments");return 2}
    report:=doctorReport{Status:"not_ready",Checks:make([]string,0)}
    if _,err:=adminconfig.Load(os.Getenv("AEGIS_ADMIN_CONFIG"));err==nil {
        report.ConfigValid=true
    }else{
        report.Checks=append(report.Checks,"admin_config_invalid")
    }
    if os.Getenv("AEGIS_DATABASE_URL")==""{
        report.Checks=append(report.Checks,"database_url_missing")
    }else{
        checkCtx,cancel:=context.WithTimeout(ctx,10*time.Second)
        db,err:=storage.Open(checkCtx,os.Getenv("AEGIS_DATABASE_URL"))
        if err!=nil{
            report.Checks=append(report.Checks,"database_connection_failed")
        }else{
            report.DatabaseConnected=true
            if err:=db.Ready(checkCtx);err==nil{
                report.SchemaReady=true
                if _,err:=db.ListAudit(checkCtx,1);err==nil{
                    report.AuditReadable=true
                }else{report.Checks=append(report.Checks,"audit_unavailable")}
            }else{report.Checks=append(report.Checks,"schema_outdated")}
            _=db.Close()
        }
        cancel()
    }
    if report.ConfigValid&&report.DatabaseConnected&&report.SchemaReady&&report.AuditReadable{
        report.Status="ok"
    }
    if err:=json.NewEncoder(out).Encode(report);err!=nil{
        fmt.Fprintln(errOut,"write diagnostic report:",err);return 1
    }
    if report.Status!="ok"{return 1}
    return 0
}

func runAudit(ctx context.Context,args []string,out,errOut io.Writer)int{
    flags:=flag.NewFlagSet("audit",flag.ContinueOnError)
    flags.SetOutput(errOut)
    limit:=flags.Int("limit",20,"most recent administrative operations (1..100)")
    if err:=flags.Parse(args);err!=nil{
        if errors.Is(err,flag.ErrHelp){return 0}
        return 2
    }
    if flags.NArg()!=0||*limit<1||*limit>100{
        fmt.Fprintln(errOut,"audit limit must be 1..100");return 2
    }
    db,err:=openOperatorDatabase(ctx)
    if err!=nil{fmt.Fprintln(errOut,"audit database unavailable");return 1}
    defer db.Close()
    queryCtx,cancel:=context.WithTimeout(ctx,15*time.Second)
    defer cancel()
    events,err:=db.ListAudit(queryCtx,*limit)
    if err!=nil{fmt.Fprintln(errOut,"audit query failed");return 1}
    if err:=json.NewEncoder(out).Encode(map[string]any{"items":events});err!=nil{
        fmt.Fprintln(errOut,"write audit:",err);return 1
    }
    return 0
}
