package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// runMigrate is an operator command. Database secrets are supplied via an
// environment variable, not visible in the command-line argument list.
func runMigrate(ctx context.Context,args []string,out,errOut io.Writer)int {
	if len(args)!=0 {
		fmt.Fprintln(errOut,"migrate takes no arguments")
		return 2
	}
	dbCtx,cancel:=context.WithTimeout(ctx,30*time.Second)
	defer cancel()
	db,err:=storage.Open(dbCtx,os.Getenv("AEGIS_DATABASE_URL"))
	if err!=nil {fmt.Fprintln(errOut,err);return 1}
	defer db.Close()
	if err:=db.Migrate(dbCtx);err!=nil{fmt.Fprintln(errOut,err);return 1}
	fmt.Fprintln(out,"PostgreSQL schema migrations applied successfully")
	return 0
}
