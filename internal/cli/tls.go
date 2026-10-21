package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// runTLS exposes certificate inspection and deliberately staged CA rollover.
// It never reads a CA private key, issues an untrusted certificate, or
// replaces a previously-existing trust file.
func runTLS(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "tls requires check or bundle")
		return 2
	}
	switch args[0] {
	case "check":
		flags := flag.NewFlagSet("tls check", flag.ContinueOnError)
		flags.SetOutput(errOut)
		cert := flags.String("tls-cert", "", "TLS server PEM certificate chain")
		key := flags.String("tls-key", "", "matching private key (never printed)")
		ca := flags.String("ca-cert", "", "optional explicit CA trust roots PEM")
		host := flags.String("host", "", "required server DNS name or IP in SAN")
		warn := flags.Duration("warn-before", 30*24*time.Hour,
			"report expiry when time remaining is at or below this duration")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if flags.NArg() != 0 || *cert == "" || *key == "" || *host == "" ||
			*warn < 0 || *warn > 365*24*time.Hour {
			fmt.Fprintln(errOut, "tls check requires --tls-cert, --tls-key, --host and valid --warn-before")
			return 2
		}
		report, err := identity.ValidateServerCertificate(*cert, *key, *ca, *host, *warn)
		if err != nil {
			fmt.Fprintln(errOut, "TLS certificate check failed:", err)
			return 1
		}
		if err := json.NewEncoder(out).Encode(report); err != nil {
			fmt.Fprintln(errOut, "write TLS diagnostic failed")
			return 1
		}
		if report.Expiring {
			fmt.Fprintln(errOut, "TLS certificate renewal required: expires within warning period")
			return 1
		}
		return 0
	case "bundle":
		flags := flag.NewFlagSet("tls bundle", flag.ContinueOnError)
		flags.SetOutput(errOut)
		current := flags.String("current-ca", "", "currently trusted CA PEM")
		next := flags.String("next-ca", "", "new CA PEM, independently verified")
		output := flags.String("output", "", "NEW combined CA bundle path")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if flags.NArg() != 0 || *current == "" || *next == "" || *output == "" {
			fmt.Fprintln(errOut, "tls bundle requires --current-ca, --next-ca and --output")
			return 2
		}
		count, err := identity.MergeTrustBundles(*current, *next, *output)
		if err != nil {
			fmt.Fprintln(errOut, "TLS trust bundle failed:", err)
			return 1
		}
		fmt.Fprintf(out, "Created a new CA trust bundle containing %d distinct CAs. Verify new CA fingerprint before distributing; restart agent processes to load it.\n", count)
		return 0
	default:
		fmt.Fprintln(errOut, "tls requires check or bundle")
		return 2
	}
}
