package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleFile(t *testing.T, raw string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestProfilesAndDefaults(t *testing.T) {
	file := sampleFile(t, "{\"version\":1,\"agent\":{\"server\":\"127.0.0.1:9090\",\"credentials_file\":\"private/agent.json\",\"ca_cert_file\":\"public/ca.pem\",\"interval\":\"8s\"},\"receiver\":{\"tls_cert_file\":\"cert.pem\",\"tls_key_file\":\"private/key.pem\",\"agents_file\":\"private/registry.json\"}}")
	c, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Agent == nil || c.Receiver == nil {
		t.Fatal("profiles missing")
	}
	if c.Agent.MaxQueueMB != 64 || c.Receiver.MaxInflight != 8 {
		t.Fatal("defaults lost")
	}
	if c.Agent.CredentialsFile != filepath.Join(filepath.Dir(file), "private", "agent.json") {
		t.Fatal("relative agent path")
	}
	if c.Receiver.TLSKeyFile != filepath.Join(filepath.Dir(file), "private", "key.pem") {
		t.Fatal("relative server path")
	}
	if c.Agent.Flags()[0] != "--server" || c.Receiver.Flags()[0] != "--listen" {
		t.Fatal("command mapping")
	}
}

func TestMalformedConfigurations(t *testing.T) {
	bad := []string{
		"{\"version\":1,\"agent\":{\"credentials_file\":\"cred\",\"ca_cert_file\":\"ca\",\"token\":\"secret\"}}",
		"{\"version\":1,\"database_url\":\"postgres://test:secret@localhost/db\",\"agent\":{}}",
		"{\"version\":2,\"agent\":{\"credentials_file\":\"cred\",\"ca_cert_file\":\"ca\"}}",
		"{\"version\":1,\"agent\":{\"credentials_file\":\"cred\",\"ca_cert_file\":\"ca\",\"max_queue_mb\":0}}",
		"{\"version\":1,\"agent\":{\"credentials_file\":\"cred\",\"ca_cert_file\":\"ca\",\"interval\":\"1ms\"}}",
		"{\"version\":1,\"receiver\":{\"tls_cert_file\":\"cert\",\"tls_key_file\":\"key\",\"agents_file\":\"agents\",\"diagnostics_listen\":\"0.0.0.0:9091\"}}",
		"{\"version\":1,\"receiver\":{\"tls_cert_file\":\"cert\",\"tls_key_file\":\"key\",\"agents_file\":\"agents\",\"max_inflight_samples\":1,\"max_per_agent_samples\":2}}",
		"{\"version\":1}",
		"{\"version\":1,\"version\":2,\"agent\":{}}",
		"{\"version\":1,\"agent\":{\"server\":\"a:1\",\"server\":\"b:1\"}}",
		"{\"version\":1,\"agent\":{}} {\"version\":1}",
	}
	for _, raw := range bad {
		if _, err := Load(sampleFile(t, raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestOversizedAndSymlinkConfiguration(t *testing.T) {
	file := sampleFile(t, strings.Repeat(" ", maxConfigSize+1))
	if _, err := Load(file); err == nil {
		t.Fatal("oversize accepted")
	}
	good := sampleFile(t, "{\"version\":1,\"agent\":{\"server\":\"127.0.0.1:9090\",\"credentials_file\":\"private/agent.json\",\"ca_cert_file\":\"public/ca.pem\"},\"receiver\":{\"tls_cert_file\":\"server.pem\",\"tls_key_file\":\"private/server.key\",\"agents_file\":\"private/agents.json\"}}")
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(good, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("symlink accepted")
	}
}
