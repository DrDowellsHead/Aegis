package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/DrDowellsHead/Aegis/internal/cli"
)

// version is set by the release build; development builds are explicit.
var version = "dev"

func main() { os.Exit(run()) }

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version)
}
