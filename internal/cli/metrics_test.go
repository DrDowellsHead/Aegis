package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

func TestCollectCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"collect", "--disk", t.TempDir(), "--cpu-window", "100ms", "--timeout", "10s"}, &out, &errOut, "test")
	if code != 0 {
		t.Fatalf("code=%d: %s", code, &errOut)
	}
	var snapshot metrics.Snapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.CPU == nil || snapshot.Memory == nil || snapshot.Disk == nil {
		t.Fatalf("%+v", snapshot)
	}
}
func TestCollectMissingDisk(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"collect", "--disk", filepath.Join(t.TempDir(), "missing"), "--cpu-window", "100ms", "--timeout", "10s"}, &out, &errOut, "test")
	if code != 1 {
		t.Fatalf("code=%d: %s", code, &errOut)
	}
	var snapshot metrics.Snapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Disk != nil || snapshot.Errors["disk"] == "" || snapshot.Memory == nil {
		t.Fatalf("%+v", snapshot)
	}
}
func TestMetricsArguments(t *testing.T) {
	for _, args := range [][]string{
		{"collect", "extra"}, {"collect", "--cpu-window", "0s"}, {"collect", "--timeout", "1ms"},
		{"agent", "--interval", "0s"}, {"agent", "--count", "-1"}, {"collect", "--disk", ""},
	} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), args, &out, &errOut, "test"); code != 2 {
			t.Fatalf("%v: %d %s", args, code, &errOut)
		}
	}
}
