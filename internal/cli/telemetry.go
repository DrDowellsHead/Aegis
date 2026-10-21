package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
	"github.com/DrDowellsHead/Aegis/internal/storage"
	"github.com/DrDowellsHead/Aegis/internal/telemetry"
)

func runProvision(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("provision", flag.ContinueOnError)
	flags.SetOutput(errOut)
	registry := flags.String("agents-file", "", "server agent registry JSON path")
	credentials := flags.String("credentials", "", "new per-agent credentials JSON path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *registry == "" || *credentials == "" {
		fmt.Fprintln(errOut, "provision requires --agents-file and --credentials, with no extra arguments")
		return 2
	}
	id, err := identity.Provision(*registry, *credentials)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "Provisioned agent:", id)
	fmt.Fprintln(out, "Copy credentials securely to the agent; never commit the secret file.")
	return 0
}

func runCert(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("cert", flag.ContinueOnError)
	flags.SetOutput(errOut)
	host := flags.String("host", "127.0.0.1", "DNS name or IP in the TLS certificate's SAN")
	ca := flags.String("ca-cert", "", "output development CA public certificate path")
	cert := flags.String("tls-cert", "", "output server certificate path")
	key := flags.String("tls-key", "", "output server private key path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *ca == "" || *cert == "" || *key == "" {
		fmt.Fprintln(errOut, "cert requires --ca-cert, --tls-cert and --tls-key")
		return 2
	}
	if err := identity.GenerateDevelopmentTLS(*host, *ca, *cert, *key); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "Generated development CA and server certificate. Distribute only the CA certificate to agents.")
	return 0
}

func runReceive(ctx context.Context, args []string, errOut io.Writer) int {
	flags := flag.NewFlagSet("receive", flag.ContinueOnError)
	flags.SetOutput(errOut)
	address := flags.String("listen", "127.0.0.1:9090", "gRPC listening address (public binding requires firewall rules)")
	cert := flags.String("tls-cert", "", "server TLS certificate chain")
	key := flags.String("tls-key", "", "server TLS private key")
	certHost := flags.String("tls-host", "", "optional expected server certificate DNS name or IP SAN")
	certCA := flags.String("tls-ca-cert", "", "optional explicit CA PEM to validate server certificate chain")
	registryPath := flags.String("agents-file", "", "registry of authorized agents")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *cert == "" || *key == "" || *registryPath == "" {
		fmt.Fprintln(errOut, "receive requires --tls-cert, --tls-key and --agents-file")
		return 2
	}
	if *certCA != "" && *certHost == "" {
		fmt.Fprintln(errOut, "--tls-ca-cert requires --tls-host so the complete chain and SAN can be checked")
		return 2
	}
	transport, err := telemetry.ServerCredentialsForHost(*cert, *key, *certCA, *certHost)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	registry, err := identity.LoadRegistry(*registryPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := storage.Open(dbCtx, os.Getenv("AEGIS_DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(errOut, "database:", err)
		return 2
	}
	defer db.Close()
	if err := db.Ready(dbCtx); err != nil {
		fmt.Fprintln(errOut, "database schema:", err, "(run aegis migrate)")
		return 2
	}
	// Reconcile the authoritative, token-protected JSON registry before
	// accepting network requests. Never copy token hashes into PostgreSQL.
	if err := db.SyncAgents(dbCtx, registry.IDs()); err != nil {
		fmt.Fprintln(errOut, "agent registry synchronization:", err)
		return 1
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	if err := telemetry.ServeReceiver(ctx, listener, logger, transport, registry, db); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func runSend(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	flags.SetOutput(errOut)
	address := flags.String("server", "127.0.0.1:9090", "gRPC server address (must match certificate SAN)")
	credentialsPath := flags.String("credentials", "", "agent's private credentials JSON path")
	caPath := flags.String("ca-cert", "", "trusted CA public certificate")
	disk := flags.String("disk", ".", "filesystem path to measure")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *caPath == "" {
		fmt.Fprintln(errOut, "send requires --ca-cert and no extra arguments")
		return 2
	}
	if *credentialsPath == "" {
		var err error
		*credentialsPath, err = identity.DefaultPath()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	creds, err := identity.LoadCredentials(*credentialsPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	system, err := metrics.NewSystem(*disk, 250*time.Millisecond)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	collector, err := metrics.New(system, 3*time.Second)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	snapshot, err := collector.Collect(ctx)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	id, err := telemetry.SendOnce(ctx, *address, creds, *caPath, snapshot)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(map[string]any{
		"received": true, "durable": true, "measurement_id": id,
	}); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
