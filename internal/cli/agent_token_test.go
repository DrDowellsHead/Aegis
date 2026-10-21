package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

func TestAgentTokenCLI(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, "agents.json")
	oldPath := filepath.Join(dir, "old.json")
	replacementPath := filepath.Join(dir, "new.json")
	id, err := identity.Provision(registry, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	old, err := identity.LoadCredentials(oldPath)
	if err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	code := Run(context.Background(), []string{"agent-token", "rotate",
		"--agents-file", registry, "--agent-id", id,
		"--credentials", replacementPath}, &out, &stderr, "test")
	if code != 0 {
		t.Fatalf("rotate returned %d: %s", code, stderr.String())
	}
	newCreds, err := identity.LoadCredentials(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{old.Token, newCreds.Token} {
		if strings.Contains(out.String(), secret) || strings.Contains(stderr.String(), secret) {
			t.Fatal("CLI printed agent bearer token")
		}
	}
	if !strings.Contains(out.String(), "restart aegis receive") {
		t.Fatal("CLI failed to warn that receiver needs restart")
	}
	out.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"agent-token", "revoke",
		"--agents-file", registry, "--agent-id", id}, &out, &stderr, "test")
	if code != 0 {
		t.Fatalf("revoke returned %d: %s", code, stderr.String())
	}
	reg, err := identity.LoadRegistry(registry)
	if err != nil || reg.Authenticate(id, newCreds.Token) {
		t.Fatal("revoked bearer token still validates")
	}
	if _, err = os.Stat(replacementPath); err != nil {
		t.Fatal("CLI must not delete deployed client credentials")
	}
}

func TestAgentTokenRejectsIncompleteCommands(t *testing.T) {
	for _, args := range [][]string{
		{"agent-token"},
		{"agent-token", "foo"},
		{"agent-token", "rotate"},
		{"agent-token", "revoke"},
		{"agent-token", "rotate", "--agents-file", "a", "--agent-id", "bad", "--credentials", "b"},
		{"agent-token", "revoke", "--agents-file", "a", "--agent-id", "bad", "--credentials", "b"},
	} {
		var out, stderr bytes.Buffer
		if got := Run(context.Background(), args, &out, &stderr, "test"); got != 2 {
			t.Fatalf("%v: code=%d wanted 2: %s", args, got, stderr.String())
		}
	}
	var out, stderr bytes.Buffer
	if got := Run(context.Background(), []string{"agent-token", "rotate", "--help"}, &out, &stderr, "test"); got != 0 {
		t.Fatalf("rotate help: code=%d: %s", got, stderr.String())
	}
}
