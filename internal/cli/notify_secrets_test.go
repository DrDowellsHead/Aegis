package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifyPrivateSecretFilesFailClosed(t *testing.T) {
	const pathName = "AEGIS_TELEGRAM_TOKEN_FILE"
	const rawName = "AEGIS_TELEGRAM_TOKEN"
	t.Setenv(rawName, "")
	_ = os.Unsetenv(rawName)
	file := filepath.Join(t.TempDir(), "telegram")
	if err := os.WriteFile(file, []byte("secret-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pathName, file)
	t.Setenv("AEGIS_SMTP_PASSWORD", "")
	_ = os.Unsetenv("AEGIS_SMTP_PASSWORD")
	_ = os.Unsetenv("AEGIS_SMTP_PASSWORD_FILE")
	_ = os.Unsetenv("AEGIS_WEBHOOK_SECRET")
	_ = os.Unsetenv("AEGIS_WEBHOOK_SECRET_FILE")
	var stdout, stderr bytes.Buffer
	// A valid private token gets through the credentials loader;
	// the invalid notification target then fails validation before DB access.
	code := Run(context.Background(), []string{"notify", "--telegram-chat-id", "invalid"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("expected configuration rejection, got %d: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret-token") {
		t.Fatal("secret was printed to stderr")
	}
	t.Setenv(rawName, "conflicting-inline-token")
	stderr.Reset()
	code = Run(context.Background(), []string{"notify"}, &stdout, &stderr, "test")
	if code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("conflicting secrets accepted: %d %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "conflicting-inline-token") {
		t.Fatal("secret exposed")
	}
}
