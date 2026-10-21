package main

import (
	"context"
	"os"

	"github.com/DrDowellsHead/Aegis/internal/cli"
	"github.com/DrDowellsHead/Aegis/internal/lifecycle"
)

// version is set by the release build; development builds are explicit.
var version = "dev"

func main() { os.Exit(run()) }

func run() int {
	return lifecycle.Run(os.Args[1:], func(ctx context.Context) int {
		return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version)
	})
}
