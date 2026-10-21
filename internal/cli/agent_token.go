package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// runAgentToken changes an existing agent's server-side authorization.
// Secrets are never accepted as flags or printed; the replacement token is
// written exclusively to a private JSON file for secure client distribution.
func runAgentToken(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "agent-token requires rotate or revoke")
		return 2
	}
	operation := args[0]
	if operation != "rotate" && operation != "revoke" {
		fmt.Fprintln(errOut, "agent-token operation must be rotate or revoke")
		return 2
	}
	flags := flag.NewFlagSet("agent-token "+operation, flag.ContinueOnError)
	flags.SetOutput(errOut)
	registry := flags.String("agents-file", "", "server-side private agent registry")
	agentID := flags.String("agent-id", "", "UUID of an existing agent")
	credentials := flags.String("credentials", "", "NEW private credentials JSON path (rotate only)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *registry == "" || !identity.ValidID(*agentID) ||
		(operation == "rotate" && *credentials == "") ||
		(operation == "revoke" && *credentials != "") {
		fmt.Fprintln(errOut, "invalid agent-token options: require --agents-file and --agent-id; rotate also requires --credentials")
		return 2
	}
	var err error
	if operation == "rotate" {
		err = identity.RotateToken(*registry, *credentials, *agentID)
	} else {
		err = identity.RevokeAgent(*registry, *agentID)
	}
	if err != nil {
		fmt.Fprintln(errOut, "agent-token operation failed:", err)
		return 1
	}
	if operation == "rotate" {
		fmt.Fprintln(out, "New agent credentials written to a private file.")
		fmt.Fprintln(out, "Transfer the file securely to the agent, then restart aegis receive.")
		fmt.Fprintln(out, "Old agent credentials remain accepted by an already-running receiver until restart.")
	} else {
		fmt.Fprintln(out, "Agent removed from the authorization registry.")
		fmt.Fprintln(out, "Restart aegis receive to enforce revocation; old credentials on the agent must also be retired.")
	}
	return 0
}
