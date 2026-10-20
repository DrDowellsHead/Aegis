package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProvisionAndRestart(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "registry.json")
	first := filepath.Join(root, "first.json")
	second := filepath.Join(root, "second.json")
	a, err := Provision(registryPath, first)
	if err != nil { t.Fatal(err) }
	b, err := Provision(registryPath, second)
	if err != nil { t.Fatal(err) }
	if a == b || !ValidID(a) || !ValidID(b) { t.Fatalf("invalid IDs: %q %q", a, b) }
	creds, err := LoadCredentials(first)
	if err != nil { t.Fatal(err) }
	r, err := LoadRegistry(registryPath)
	if err != nil { t.Fatal(err) }
	if creds.AgentID != a || !r.Authenticate(a, creds.Token) || r.Authenticate(b, creds.Token) || r.Authenticate(a, "bad") {
		t.Fatal("registry authentication failed")
	}
	if _, err := Provision(registryPath, first); err == nil { t.Fatal("overwrote identity") }
	reopened, err := LoadCredentials(first)
	if err != nil || reopened != creds { t.Fatalf("identity changed across restart: %+v %v", reopened, err) }
	if runtime.GOOS != "windows" {
		if err := os.Chmod(first, 0644); err != nil { t.Fatal(err) }
		if _, err := LoadCredentials(first); err == nil { t.Fatal("accepted unsafe permissions") }
	}
}

func TestCorruptAndSymlinkFiles(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "creds.json")
	if err := os.WriteFile(p, []byte("not-json"), 0600); err != nil { t.Fatal(err) }
	if _, err := LoadCredentials(p); err == nil { t.Fatal("accepted invalid JSON") }
	if err := os.Remove(p); err != nil { t.Fatal(err) }
	if err := os.Symlink(filepath.Join(root, "target"), p); err == nil {
		if _, err := LoadCredentials(p); err == nil { t.Fatal("accepted symlink") }
	}
}
