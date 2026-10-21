package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRotateAndRevokeAgentToken(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "agents.json")
	oldClient := filepath.Join(dir, "old.json")
	otherClient := filepath.Join(dir, "other.json")
	newClient := filepath.Join(dir, "replacement.json")

	id, err := Provision(registryPath, oldClient)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Provision(registryPath, otherClient)
	if err != nil {
		t.Fatal(err)
	}
	oldCredentials, err := LoadCredentials(oldClient)
	if err != nil {
		t.Fatal(err)
	}
	otherCredentials, err := LoadCredentials(otherClient)
	if err != nil {
		t.Fatal(err)
	}

	if err := RotateToken(registryPath, newClient, id); err != nil {
		t.Fatal(err)
	}
	replacement, err := LoadCredentials(newClient)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.AgentID != id || replacement.Token == oldCredentials.Token ||
		!validToken(replacement.Token) {
		t.Fatal("rotation did not preserve identity or generate fresh token")
	}
	registry, err := LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Authenticate(id, oldCredentials.Token) ||
		!registry.Authenticate(id, replacement.Token) ||
		!registry.Authenticate(other, otherCredentials.Token) {
		t.Fatal("rotation left old token authorized, or altered second agent")
	}
	if _, err := LoadCredentials(oldClient); err != nil {
		t.Fatal("rotation must not modify existing client secrets")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(newClient)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("replacement token should be private: %v", info.Mode())
		}
	}
	if err := RevokeAgent(registryPath, id); err != nil {
		t.Fatal(err)
	}
	registry, err = LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Authenticate(id, replacement.Token) ||
		registry.Authenticate(id, oldCredentials.Token) ||
		!registry.Authenticate(other, otherCredentials.Token) {
		t.Fatal("revocation failed or disabled another agent")
	}
	if err := RevokeAgent(registryPath, id); err == nil {
		t.Fatal("silently succeeded revoking absent agent")
	}
	if _, err := os.Stat(newClient); err != nil {
		t.Fatal("revocation should not delete credentials on other machines")
	}
}

func TestAgentTokenRotationFailClosed(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "registry.json")
	oldClient := filepath.Join(dir, "old.json")
	id, err := Provision(registryPath, oldClient)
	if err != nil {
		t.Fatal(err)
	}
	original, err := LoadCredentials(oldClient)
	if err != nil {
		t.Fatal(err)
	}

	// The new credentials path must be new and must not overlap the registry.
	for _, badPath := range []string{registryPath, oldClient} {
		if err := RotateToken(registryPath, badPath, id); err == nil {
			t.Fatalf("allowed overwriting sensitive file: %s", badPath)
		}
	}
	if err := RotateToken(registryPath, filepath.Join(dir, "unknown.json"),
		"00000000-0000-4000-8000-000000000000"); err == nil {
		t.Fatal("allowed rotation of unregistered agent")
	}
	if err := RevokeAgent(registryPath, "bad-agent-id"); err == nil {
		t.Fatal("accepted malformed agent UUID")
	}
	if err := RotateToken(registryPath, filepath.Join(dir, "new.json"), "bad-id"); err == nil {
		t.Fatal("accepted malformed rotation UUID")
	}
	if err := RotateToken(filepath.Join(dir, "missing.json"),
		filepath.Join(dir, "new.json"), id); err == nil {
		t.Fatal("silently created replacement registry")
	}
	// Symlinks must not be followed when creating a replacement token.
	sym := filepath.Join(dir, "credentials-link")
	if err := os.Symlink(filepath.Join(dir, "target.json"), sym); err == nil {
		if err := RotateToken(registryPath, sym, id); err == nil {
			t.Fatal("rotation followed credential symlink")
		}
	}
	registry, err := LoadRegistry(registryPath)
	if err != nil || !registry.Authenticate(id, original.Token) {
		t.Fatal("failed rotation unexpectedly revoked original token")
	}

}

func TestRotationDoesNotExposeSecretsInErrors(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "agents.json")
	old := filepath.Join(dir, "old.json")
	id, err := Provision(registryPath, old)
	if err != nil {
		t.Fatal(err)
	}
	oldCredentials, err := LoadCredentials(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := RotateToken(registryPath, old, id); err == nil ||
		strings.Contains(err.Error(), oldCredentials.Token) {
		t.Fatal("rotation error leaked token or allowed overwrite")
	}
}
