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

	"github.com/DrDowellsHead/Aegis/internal/history"
)

func runHistory(ctx context.Context, args []string, errOut io.Writer) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	flags.SetOutput(errOut)
	listen := flags.String("listen", "127.0.0.1:8081", "loopback-only HTTP history API")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errOut, "unexpected positional arguments")
		return 2
	}
	if err := history.RequireLoopback(*listen); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	// Initialize DB only after validating the operator credential.
	// It is never logged or placed in command-line arguments.
	token := os.Getenv("AEGIS_HISTORY_TOKEN")
	if len(token) < 32 || len(token) > 4096 {
		fmt.Fprintln(errOut, "AEGIS_HISTORY_TOKEN must be 32..4096 bytes")
		return 2
	}
	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := openConfiguredDatabase(dbCtx)
	if err != nil {
		fmt.Fprintln(errOut, "history database unavailable:", err)
		return 1
	}
	defer db.Close()
	if err := db.Ready(dbCtx); err != nil {
		fmt.Fprintln(errOut, "history schema unavailable:", err)
		return 1
	}
	handler, err := history.NewHandler(db, token, logger)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := history.Serve(ctx, listener, handler, logger); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
