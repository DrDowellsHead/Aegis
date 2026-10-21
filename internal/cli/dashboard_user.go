package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DrDowellsHead/Aegis/internal/dashboard"
)

// runDashboardUser is deliberately offline. Updating the private account
// registry requires a dashboard restart so existing sessions are revoked.
func runDashboardUser(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("dashboard-user", flag.ContinueOnError)
	flags.SetOutput(errOut)
	file := flags.String("file", "", "private JSON account registry")
	username := flags.String("username", "", "lowercase login name")
	role := flags.String("role", "viewer", "admin or viewer")
	replace := flags.Bool("replace", false, "replace an existing account")
	remove := flags.Bool("remove", false, "remove an existing account")
	stdin := flags.Bool("password-stdin", false, "read password from standard input without command-line arguments")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp { return 0 }
		return 2
	}
	if flags.NArg() != 0 || *file == "" || *username == "" {
		fmt.Fprintln(errOut, "dashboard-user requires --file and --username, no positional arguments")
		return 2
	}
	if *remove && (*replace || *stdin) {
		fmt.Fprintln(errOut, "--remove is incompatible with --replace and --password-stdin")
		return 2
	}
	if !*remove && !*stdin {
		fmt.Fprintln(errOut, "password must be supplied with --password-stdin (never as a CLI argument)")
		return 2
	}
	password := ""
	if !*remove {
		reader := bufio.NewReader(io.LimitReader(os.Stdin, 75))
		input, err := io.ReadAll(reader)
		if err != nil {
			fmt.Fprintln(errOut, "cannot read password from stdin")
			return 2
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(input), "\n"), "\r")
	}
	if err := dashboard.SaveAccount(*file, *username, dashboard.Role(*role), password, *remove, *replace); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "Dashboard accounts updated. Restart the dashboard to load accounts and revoke old sessions.")
	return 0
}
