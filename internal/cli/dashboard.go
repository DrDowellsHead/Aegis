package cli

import (
    "context"
    "errors"
    "flag"
    "fmt"
    "io"
    "log/slog"
    "net"
    "os"
    "time"

    "github.com/DrDowellsHead/Aegis/internal/dashboard"
    "github.com/DrDowellsHead/Aegis/internal/history"
    "github.com/DrDowellsHead/Aegis/internal/storage"
)

func runDashboard(ctx context.Context,args []string,errOut io.Writer)int{
    flags:=flag.NewFlagSet("dashboard",flag.ContinueOnError)
    flags.SetOutput(errOut)
    listen:=flags.String("listen","127.0.0.1:8082","numeric loopback address (HTTP; local development only)")
    staticDir:=flags.String("static-dir","web/dist","compiled web frontend directory")
    if err:=flags.Parse(args);err!=nil{
        if errors.Is(err,flag.ErrHelp){return 0}
        return 2
    }
    if flags.NArg()!=0{fmt.Fprintln(errOut,"unexpected positional arguments");return 2}
    if err:=history.RequireLoopback(*listen);err!=nil{
        fmt.Fprintln(errOut,"dashboard requires numeric loopback:",err)
        return 2
    }
    token:=os.Getenv("AEGIS_DASHBOARD_TOKEN")
    logger:=slog.New(slog.NewJSONHandler(errOut,nil))
    // Verify password before touching DB, so there is no unauthenticated
    // fallback when a secret is absent.
    if len(token)<32||len(token)>4096{
        fmt.Fprintln(errOut,"AEGIS_DASHBOARD_TOKEN must be 32..4096 bytes")
        return 2
    }
    dbCtx,cancel:=context.WithTimeout(ctx,10*time.Second)
    defer cancel()
    db,err:=storage.Open(dbCtx,os.Getenv("AEGIS_DATABASE_URL"))
    if err!=nil{fmt.Fprintln(errOut,"dashboard database unavailable");return 1}
    defer db.Close()
    if err:=db.Ready(dbCtx);err!=nil{
        fmt.Fprintln(errOut,"dashboard database schema unavailable (run aegis migrate)")
        return 1
    }
    handler,err:=dashboard.New(db,token,*staticDir,logger)
    if err!=nil{fmt.Fprintln(errOut,err);return 2}
    listener,err:=net.Listen("tcp",*listen)
    if err!=nil{fmt.Fprintln(errOut,"cannot start dashboard listener:",err);return 1}
    if err:=dashboard.Serve(ctx,listener,handler,logger);err!=nil{
        fmt.Fprintln(errOut,"dashboard stopped:",err);return 1
    }
    return 0
}
