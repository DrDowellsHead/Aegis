package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/DrDowellsHead/Aegis/internal/appconfig"
)

// start selects one supported supervised process. No shell is involved:
// values are passed directly into the existing typed CLI entrypoints.
func runStart(ctx context.Context, args []string, errOut io.Writer) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(errOut)
	filename := flags.String("config", "", "versioned non-secret JSON configuration")
	component := flags.String("component", "", "agent or receiver")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) { return 0 }
		return 2
	}
	if flags.NArg() != 0 || *filename == "" || (*component != "agent" && *component != "receiver") {
		fmt.Fprintln(errOut, "start requires --config FILE --component agent|receiver")
		return 2
	}
	cfg, err := appconfig.Load(*filename)
	if err != nil {
		fmt.Fprintln(errOut, "invalid start configuration:", err)
		return 2
	}
	switch *component {
	case "agent":
		if cfg.Agent == nil {
			fmt.Fprintln(errOut, "configuration does not define an agent")
			return 2
		}
		return runForward(ctx, cfg.Agent.Flags(), errOut)
	case "receiver":
		if cfg.Receiver == nil {
			fmt.Fprintln(errOut, "configuration does not define a receiver")
			return 2
		}
		return runReceive(ctx, cfg.Receiver.Flags(), errOut)
	}
	return 2
}

// validate-config never opens credentials, connects to PostgreSQL or prints
// secret material. It reports only the validated sections.
func runValidateConfig(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("validate-config", flag.ContinueOnError)
	flags.SetOutput(errOut)
	filename := flags.String("config", "", "versioned non-secret JSON configuration")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) { return 0 }
		return 2
	}
	if flags.NArg() != 0 || *filename == "" {
		fmt.Fprintln(errOut, "validate-config requires --config FILE")
		return 2
	}
	cfg, err := appconfig.Load(*filename)
	if err != nil {
		fmt.Fprintln(errOut, "invalid configuration:", err)
		return 2
	}
	names := make([]string, 0, 2)
	if cfg.Agent != nil { names = append(names, "agent") }
	if cfg.Receiver != nil { names = append(names, "receiver") }
	if err := json.NewEncoder(out).Encode(struct {
		Version int `json:"version"`
		Valid bool `json:"valid"`
		Components []string `json:"components"`
	}{Version: cfg.Version, Valid: true, Components: names}); err != nil {
		fmt.Fprintln(errOut, "write config report failed")
		return 1
	}
	return 0
}
