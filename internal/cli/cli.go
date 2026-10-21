// Package cli implements command dispatch separately from process exit.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/probe"
	"github.com/DrDowellsHead/Aegis/internal/server"
)

// Run returns a process exit code. Writers belong to the caller.
func Run(ctx context.Context, args []string, out, errOut io.Writer, version string) int {
	if len(args) == 0 {
		usage(errOut)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(out)
		return 0
	case "version":
		if len(args) != 1 {
			fmt.Fprintln(errOut, "version accepts no arguments")
			return 2
		}
		fmt.Fprintln(out, "aegis", version)
		return 0
	case "check":
		flags := flag.NewFlagSet("check", flag.ContinueOnError)
		flags.SetOutput(errOut)
		target := flags.String("url", "", "HTTP(S) URL to check")
		timeout := flags.Duration("timeout", 5*time.Second, "request deadline (maximum 1m)")
		if err := flags.Parse(args[1:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if flags.NArg() != 0 {
			fmt.Fprintln(errOut, "unexpected positional arguments")
			return 2
		}
		result, err := probe.HTTP(ctx, *target, *timeout)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		if err := json.NewEncoder(out).Encode(result); err != nil {
			fmt.Fprintln(errOut, "write result:", err)
			return 1
		}
		if !result.Healthy {
			return 1
		}
		return 0
	case "start":
		return runStart(ctx, args[1:], errOut)
	case "validate-config":
		return runValidateConfig(args[1:], out, errOut)
	case "config":
		return runAdminConfig(args[1:], out, errOut)
	case "doctor":
		return runDoctor(ctx, args[1:], out, errOut)
	case "audit":
		return runAudit(ctx, args[1:], out, errOut)
	case "storage":
		return runStorage(ctx, args[1:], out, errOut)
	case "retention":
		return runRetention(ctx, args[1:], out, errOut)
	case "backfill":
		return runBackfill(ctx, args[1:], out, errOut)
	case "dashboard-user":
		return runDashboardUser(args[1:], out, errOut)
	case "dashboard":
		return runDashboard(ctx, args[1:], errOut)
	case "notify":
		return runNotify(ctx, args[1:], out, errOut)
	case "watch":
		return runWatch(ctx, args[1:], out, errOut)
	case "history":
		return runHistory(ctx, args[1:], errOut)
	case "migrate":
		return runMigrate(ctx, args[1:], out, errOut)
	case "backup":
		return runBackup(ctx, args[1:], out, errOut)
	case "restore":
		return runRestore(ctx, args[1:], out, errOut)
	case "provision":
		return runProvision(args[1:], out, errOut)
	case "agent-token":
		return runAgentToken(args[1:], out, errOut)
	case "cert":
		return runCert(args[1:], out, errOut)
	case "tls":
		return runTLS(args[1:], out, errOut)
	case "forward":
		return runForward(ctx, args[1:], errOut)
	case "receive":
		return runReceive(ctx, args[1:], errOut)
	case "send":
		return runSend(ctx, args[1:], out, errOut)
	case "collect", "agent":
		return runMetrics(ctx, args[0], args[1:], out, errOut)
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(errOut)
		address := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
		if err := flags.Parse(args[1:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if flags.NArg() != 0 {
			fmt.Fprintln(errOut, "unexpected positional arguments")
			return 2
		}
		logger := slog.New(slog.NewJSONHandler(errOut, nil))
		listener, err := net.Listen("tcp", *address)
		if err != nil {
			logger.Error("listen failed", "error", err)
			return 1
		}
		if err = server.Serve(ctx, listener, logger); err != nil {
			logger.Error("server failed", "error", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(errOut, "unknown command: %s\n", args[0])
		usage(errOut)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: aegis <serve|receive|send|forward|notify|watch|dashboard|dashboard-user|history|start|validate-config|config|doctor|audit|storage|retention|backfill|migrate|backup|restore|provision|agent-token|cert|tls|check|collect|agent|version|help> [options]")
}
