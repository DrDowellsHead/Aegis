package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// pgConnection contains only connection parameters passed through the child
// process environment. A password/connection URI must never be an argv value.
type pgConnection struct {
	database string
	env      []string
}

func parsePGConnection(raw string) (pgConnection, error) {
	var result pgConnection
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.Opaque != "" || u.Fragment != "" || u.Hostname() == "" ||
		u.Path == "" || u.Path == "/" || strings.Contains(strings.TrimPrefix(u.Path, "/"), "/") {
		return result, fmt.Errorf("a PostgreSQL TCP URL with an explicit database name is required")
	}
	if u.Port() != "" {
		for _, c := range u.Port() {
			if c < '0' || c > '9' {
				return result, fmt.Errorf("invalid PostgreSQL port")
			}
		}
	}
	// libpq recognizes these settings in the PG* environment. Unknown
	// parameters are refused rather than silently ignored (e.g. sslmode).
	allowed := map[string]string{
		"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT",
		"sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY",
		"connect_timeout": "PGCONNECT_TIMEOUT", "application_name": "PGAPPNAME",
		"target_session_attrs": "PGTARGETSESSIONATTRS",
	}
	result.database = strings.TrimPrefix(u.Path, "/")
	result.env = []string{"PGHOST=" + u.Hostname(), "PGDATABASE=" + result.database}
	if port := u.Port(); port != "" {
		result.env = append(result.env, "PGPORT="+port)
	}
	if u.User != nil {
		result.env = append(result.env, "PGUSER="+u.User.Username())
		if password, ok := u.User.Password(); ok {
			result.env = append(result.env, "PGPASSWORD="+password)
		}
	}
	for key, values := range u.Query() {
		envName, ok := allowed[key]
		if !ok || len(values) != 1 || strings.ContainsAny(values[0], "\x00\n\r") {
			return pgConnection{}, fmt.Errorf("unsupported or duplicate PostgreSQL connection parameter %q", key)
		}
		result.env = append(result.env, envName+"="+values[0])
	}
	return result, nil
}

// Keep the operator's ordinary process environment but never inherit
// conflicting PG* settings or pass the Aegis DB password twice.
func pgCommand(ctx context.Context, name string, connection pgConnection, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "PG") || strings.HasPrefix(key, "AEGIS_DATABASE_URL") ||
			strings.HasPrefix(key, "AEGIS_RESTORE_DATABASE_URL") {
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
	cmd.Env = append(cmd.Env, connection.env...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard // never print libpq connection details or credentials
	return cmd
}

func privateBackupDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("backup directory: %w", err)
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("backup directory must be a private regular directory (0700 on Unix)")
	}
	return nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func regularPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("backup is not a private regular file: %s", filepath.Base(path))
	}
	return nil
}

func checkArchive(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "pg_restore", "--list", path)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore could not read custom-format archive: %w", err)
	}
	return nil
}

// runBackup writes a custom-format pg_dump and a SHA-256 sidecar.
// Neither path is overwritten. The dump is not a replacement for the
// private agents.json, dashboard users file, TLS keys or spool.
func runBackup(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(errOut)
	output := flags.String("output", "", "new custom-format PostgreSQL archive in a private directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) { return 0 }
		return 2
	}
	if flags.NArg() != 0 || *output == "" {
		fmt.Fprintln(errOut, "backup requires --output and no positional arguments")
		return 2
	}
	dest, err := filepath.Abs(*output)
	if err != nil { fmt.Fprintln(errOut, err); return 2 }
	if err := privateBackupDirectory(filepath.Dir(dest)); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	for _, path := range []string{dest, dest + ".sha256"} {
		if _, err := os.Lstat(path); err == nil {
			fmt.Fprintln(errOut, "backup destination already exists; refusing overwrite")
			return 2
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	raw := os.Getenv("AEGIS_DATABASE_URL")
	conn, err := parsePGConnection(raw)
	if err != nil { fmt.Fprintln(errOut, err); return 2 }
	if _, err := exec.LookPath("pg_dump"); err != nil { fmt.Fprintln(errOut, "pg_dump is required"); return 1 }
	if _, err := exec.LookPath("pg_restore"); err != nil { fmt.Fprintln(errOut, "pg_restore is required"); return 1 }
	db, err := openOperatorDatabase(ctx)
	if err != nil { fmt.Fprintln(errOut, "source database not ready:", err); return 1 }
	_ = db.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".aegis-backup-*")
	if err != nil { fmt.Fprintln(errOut, err); return 1 }
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0600); err != nil { fmt.Fprintln(errOut, err); return 1 }
	// pg_dump writes the same private, already-created temporary file.
	cmd := pgCommand(ctx, "pg_dump", conn,
		"--format=custom", "--no-owner", "--no-privileges", "--file="+tmp.Name(),
		"--dbname="+conn.database)
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(errOut, "pg_dump failed (check PostgreSQL connection and client version)")
		return 1
	}
	if err := tmp.Sync(); err != nil { fmt.Fprintln(errOut, err); return 1 }
	if err := checkArchive(ctx, tmp.Name()); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	digest, err := fileDigest(tmp.Name())
	if err != nil { fmt.Fprintln(errOut, err); return 1 }
	if err := tmp.Close(); err != nil { fmt.Fprintln(errOut, err); return 1 }
	// Hard-linking in the same directory publishes without replacing an
	// existing file, including a symlink installed during the dump.
	if err := os.Link(tmp.Name(), dest); err != nil {
		fmt.Fprintln(errOut, "cannot publish backup without overwrite:", err)
		return 1
	}
	sidecar, err := os.OpenFile(dest+".sha256", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = os.Remove(dest)
		fmt.Fprintln(errOut, "cannot publish backup checksum:", err)
		return 1
	}
	_, writeErr := io.WriteString(sidecar, digest+"\n")
	syncErr := sidecar.Sync()
	closeErr := sidecar.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(dest+".sha256")
		_ = os.Remove(dest)
		fmt.Fprintln(errOut, "cannot durably publish backup checksum:", err)
		return 1
	}
	// Explicit archive plus checksum is considered complete only together.
	fmt.Fprintln(out, "Backup created:", dest)
	fmt.Fprintln(out, "SHA-256:", digest)
	return 0
}

// runRestore accepts only a fresh empty target database. It never drops
// existing data and verifies the checksum before any SQL changes.
func runRestore(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(errOut)
	input := flags.String("input", "", "existing custom-format PostgreSQL archive")
	confirm := flags.Bool("confirm-empty-target", false, "explicitly authorize restore ONLY to an empty separate database")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) { return 0 }
		return 2
	}
	if flags.NArg() != 0 || *input == "" || !*confirm {
		fmt.Fprintln(errOut, "restore requires --input and --confirm-empty-target; no existing database may be overwritten")
		return 2
	}
	src, err := filepath.Abs(*input)
	if err != nil { fmt.Fprintln(errOut, err); return 2 }
	if err := privateBackupDirectory(filepath.Dir(src)); err != nil { fmt.Fprintln(errOut, err); return 2 }
	if err := regularPrivateFile(src); err != nil { fmt.Fprintln(errOut, err); return 1 }
	if err := regularPrivateFile(src+".sha256"); err != nil { fmt.Fprintln(errOut, err); return 1 }
	rawDigest, err := os.ReadFile(src+".sha256")
	if err != nil { fmt.Fprintln(errOut, err); return 1 }
	want := strings.TrimSpace(string(rawDigest))
	if len(want) != 64 {
		fmt.Fprintln(errOut, "invalid backup SHA-256 sidecar")
		return 1
	}
	if _, err := hex.DecodeString(want); err != nil {
		fmt.Fprintln(errOut, "invalid backup SHA-256 sidecar")
		return 1
	}
	got, err := fileDigest(src)
	if err != nil { fmt.Fprintln(errOut, err); return 1 }
	if got != want {
		fmt.Fprintln(errOut, "backup checksum mismatch; database left untouched")
		return 1
	}
	if _, err := exec.LookPath("pg_restore"); err != nil { fmt.Fprintln(errOut, "pg_restore is required"); return 1 }
	if err := checkArchive(ctx, src); err != nil { fmt.Fprintln(errOut, err); return 1 }
	conn, err := parsePGConnection(os.Getenv("AEGIS_RESTORE_DATABASE_URL"))
	if err != nil { fmt.Fprintln(errOut, "invalid restore target:", err); return 2 }
	source := os.Getenv("AEGIS_DATABASE_URL")
	if source != "" && source == os.Getenv("AEGIS_RESTORE_DATABASE_URL") {
		fmt.Fprintln(errOut, "source and restore target must be different databases")
		return 2
	}
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	target, err := storage.Open(connectCtx, os.Getenv("AEGIS_RESTORE_DATABASE_URL"))
	if err != nil { cancel(); fmt.Fprintln(errOut, "restore target unavailable:", err); return 1 }
	defer target.Close()
	empty, err := target.EmptyForRestore(connectCtx)
	cancel()
	if err != nil { fmt.Fprintln(errOut, "cannot validate empty restore target:", err); return 1 }
	if !empty {
		fmt.Fprintln(errOut, "restore target is not empty; refusing to replace existing objects")
		return 1
	}
	// One failed statement rolls back the restore instead of leaving a
	// partially populated database.
	cmd := pgCommand(ctx, "pg_restore", conn, "--exit-on-error",
		"--single-transaction", "--no-owner", "--no-privileges",
		"--dbname="+conn.database, src)
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(errOut, "pg_restore failed; restore transaction rolled back")
		return 1
	}
	migrateCtx, finish := context.WithTimeout(ctx, 60*time.Second)
	defer finish()
	if err := target.Migrate(migrateCtx); err != nil {
		fmt.Fprintln(errOut, "restored data present but migration verification failed:", err)
		return 1
	}
	if err := target.Ready(migrateCtx); err != nil {
		fmt.Fprintln(errOut, "restored data present but schema not ready:", err)
		return 1
	}
	fmt.Fprintln(out, "PostgreSQL restored and migrations verified in the separate target database")
	return 0
}

// Date is intentionally not used in the archive format: every dump is
// content-addressed by its SHA-256 and has an operator-selected filename.
var _ = time.Second
