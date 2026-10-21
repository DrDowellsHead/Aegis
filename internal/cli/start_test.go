package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigReportOmitsPrivatePaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "aegis.json")
	if err := os.WriteFile(file, []byte("{\"version\":1,\"agent\":{\"server\":\"127.0.0.1:9090\",\"credentials_file\":\"private/agent.json\",\"ca_cert_file\":\"public/ca.pem\"},\"receiver\":{\"tls_cert_file\":\"server.pem\",\"tls_key_file\":\"private/server.key\",\"agents_file\":\"private/agents.json\"}}"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"validate-config", "--config", file}, &out, &errs, "test")
	if code != 0 {
		t.Fatalf("validate %d %s", code, errs.String())
	}
	var got struct {
		Version    int      `json:"version"`
		Valid      bool     `json:"valid"`
		Components []string `json:"components"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.Version != 1 || len(got.Components) != 2 {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "server.key") || strings.Contains(out.String(), "agent.json") {
		t.Fatal("private path leaked")
	}
}

func TestStartUsesTypedConfigWithoutShell(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte("{\"version\":1,\"agent\":{\"server\":\"127.0.0.1:9090\",\"credentials_file\":\"private/agent.json\",\"ca_cert_file\":\"public/ca.pem\"},\"receiver\":{\"tls_cert_file\":\"server.pem\",\"tls_key_file\":\"private/server.key\",\"agents_file\":\"private/agents.json\"}}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"agent", "receiver"} {
		var out, errs bytes.Buffer
		code := Run(context.Background(), []string{"start", "--config", file, "--component", component}, &out, &errs, "test")
		if code != 2 {
			t.Fatalf("%s returned %d: %s", component, code, errs.String())
		}
	}
}
