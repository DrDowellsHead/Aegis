package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParsePGConnectionNoSecretsInArguments(t *testing.T) {
	conn, err := parsePGConnection("postgres://operator:private-secret@127.0.0.1:5432/aegis_restore?sslmode=verify-full&sslrootcert=%2Fetc%2Fca.crt")
	if err != nil {
		t.Fatal(err)
	}
	if conn.database != "aegis_restore" {
		t.Fatalf("incorrect db name: %q", conn.database)
	}
	t.Setenv("PGPASSWORD", "inherited-secret")
	cmd := pgCommand(context.Background(), "pg_restore", conn, "--dbname="+conn.database)
	for _, arg := range cmd.Args {
		if strings.Contains(arg, "private-secret") || strings.Contains(arg, "inherited-secret") {
			t.Fatal("database password exposed in subprocess command-line arguments")
		}
	}
	var foundPassword, foundSSL, foundInherited bool
	for _, item := range cmd.Env {
		if item == "PGPASSWORD=private-secret" {
			foundPassword = true
		}
		if item == "PGSSLMODE=verify-full" {
			foundSSL = true
		}
		if item == "PGPASSWORD=inherited-secret" {
			foundInherited = true
		}
	}
	if !foundPassword || !foundSSL || foundInherited {
		t.Fatal("subprocess environment did not isolate connection settings")
	}
}

func TestRejectInvalidBackupConnectionURI(t *testing.T) {
	for _, raw := range []string{
		"", "postgres://localhost", "https://localhost/aegis",
		"postgres://localhost/aegis?sslmode=disable&sslmode=verify-full",
		"postgres://localhost/aegis?unknown_option=1",
		"postgres://localhost/aegis/other",
	} {
		if _, err := parsePGConnection(raw); err == nil {
			t.Fatalf("accepted unsafe PostgreSQL URI: %q", raw)
		}
	}
}

func TestRestoreNeedsConfirmationAndValidChecksum(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "sample.dump")
	if err := os.WriteFile(archive, []byte("not a real archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive+".sha256", []byte(strings.Repeat("0", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if got := runRestore(context.Background(), []string{"--input", archive}, &out, &stderr); got != 2 {
		t.Fatalf("missing confirmation accepted: status=%d error=%s", got, stderr.String())
	}
	stderr.Reset()
	if got := runRestore(context.Background(), []string{"--input", archive, "--confirm-empty-target"}, &out, &stderr); got != 1 {
		t.Fatalf("checksum mismatch accepted: status=%d error=%s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Fatalf("expected checksum diagnostic: %s", stderr.String())
	}
}

func TestBackupRefusesToOverwriteExistingArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "existing.dump")
	if err := os.WriteFile(archive, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if got := runBackup(context.Background(), []string{"--output", archive}, &out, &stderr); got != 2 {
		t.Fatalf("overwriting existing archive was accepted: status=%d error=%s", got, stderr.String())
	}
	data, err := os.ReadFile(archive)
	if err != nil || string(data) != "preserve me" {
		t.Fatal("existing archive was modified")
	}
}

func TestPrivateBackupDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directories require ACL inspection outside POSIX mode bits")
	}
	dir := filepath.Join(t.TempDir(), "exposed")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := privateBackupDirectory(dir); err == nil {
		t.Fatal("accepted publicly accessible backup directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := privateBackupDirectory(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(t.TempDir(), "symlink")); err != nil && !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
}
