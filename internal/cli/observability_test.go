package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestReceiveDiagnosticsRejectUnsafeBindingAndMissingToken(t *testing.T) {
	testArgs := []string{"--tls-cert", "nonexistent-cert", "--tls-key", "nonexistent-key", "--agents-file", "nonexistent-registry"}
	t.Setenv("AEGIS_DIAGNOSTICS_TOKEN", strings.Repeat("a", 48))
	for _, bind := range []string{"0.0.0.0:9091", "localhost:9091", "[::]:9091"} {
		var errors bytes.Buffer
		args := append(append([]string{}, testArgs...), "--diagnostics-listen", bind)
		if code := runReceive(context.Background(), args, &errors); code != 2 ||
			!strings.Contains(errors.String(), "numeric loopback") {
			t.Fatalf("unsafe diagnostics bind %q accepted: code=%d output=%s", bind, code, errors.String())
		}
	}
	t.Setenv("AEGIS_DIAGNOSTICS_TOKEN", "")
	var errors bytes.Buffer
	args := append(append([]string{}, testArgs...), "--diagnostics-listen", "127.0.0.1:9091")
	if code := runReceive(context.Background(), args, &errors); code != 2 ||
		!strings.Contains(errors.String(), "AEGIS_DIAGNOSTICS_TOKEN") {
		t.Fatalf("unprotected diagnostics accepted: code=%d output=%s", code, errors.String())
	}
}
