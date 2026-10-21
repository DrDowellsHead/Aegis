package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorNeverLeaksDatabaseDSN(t *testing.T) {
	t.Setenv("AEGIS_DATABASE_URL", "postgres://user:secret-password@127.0.0.1:1/aegis?sslmode=disable")
	t.Setenv("AEGIS_ADMIN_CONFIG", "")
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"doctor"}, &out, &errOut, "test")
	if code != 1 {
		t.Fatalf("bad database returned %d: %s", code, out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("secret-password")) ||
		bytes.Contains(errOut.Bytes(), []byte("secret-password")) {
		t.Fatal("doctor leaked DSN")
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["status"] != "not_ready" || v["config_valid"] != true {
		t.Fatalf("unexpected report: %+v", v)
	}
}

func TestConfigCommandAndAuditArgumentValidation(t *testing.T) {
	t.Setenv("AEGIS_DATABASE_URL", "")
	path := filepath.Join(t.TempDir(), "operator.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"retention_days":30,"backfill_batch":25}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"config", "--file", path}, &out, &errOut, "test"); code != 0 {
		t.Fatalf("config exit=%d err=%s", code, errOut.String())
	}
	var cfg map[string]any
	if err := json.Unmarshal(out.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["retention_days"] != float64(30) {
		t.Fatal(cfg)
	}
	for _, args := range [][]string{{"audit", "--limit", "0"}, {"audit", "--limit", "101"}, {"doctor", "unexpected"}, {"config", "extra"}} {
		out.Reset()
		errOut.Reset()
		if code := Run(context.Background(), args, &out, &errOut, "test"); code != 2 {
			t.Errorf("%v code=%d want 2: %s", args, code, errOut.String())
		}
	}
}
