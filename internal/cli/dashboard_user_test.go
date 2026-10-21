package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/dashboard"
)

func TestDashboardUserCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "users.json")
	read, write, err := os.Pipe()
	if err != nil { t.Fatal(err) }
	_, _ = io.WriteString(write, "long-and-random-example-password\n")
	_ = write.Close()
	originalStdin := os.Stdin
	os.Stdin = read
	defer func() { os.Stdin = originalStdin; read.Close() }()

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"dashboard-user", "--file", file, "--username", "operator", "--role", "admin", "--password-stdin",
	}, &out, &errOut, "test")
	if code != 0 { t.Fatalf("code=%d: %s", code, errOut.String()) }
	accounts, err := dashboard.LoadAccounts(file)
	if err != nil || len(accounts) != 1 || accounts[0].Role != dashboard.RoleAdmin {
		t.Fatalf("account provisioning: %+v %v", accounts, err)
	}
	if bytes.Contains(out.Bytes(), []byte("long-and-random-example-password")) {
		t.Fatal("password leaked to stdout")
	}
}

func TestRemoteDashboardRejectsIncompleteConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"dashboard", "--listen", "0.0.0.0:8443"},
		{"dashboard", "--remote"},
		{"dashboard", "--remote", "--listen", "hostname.example:8443"},
	} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), args, &out, &errOut, "test")
		if code != 2 {
			t.Errorf("%v: status=%d err=%s", args, code, errOut.String())
		}
	}
}
