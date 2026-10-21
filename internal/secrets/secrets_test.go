package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrivateFileAndEnvironment(t *testing.T) {
	const key = "AEGIS_TEST_CONFIG_SECRET"
	t.Setenv(key, "direct")
	got, err := Resolve(key)
	if err != nil || got != "direct" { t.Fatalf("direct %q %v", got, err) }
	if err := os.Unsetenv(key); err != nil { t.Fatal(err) }
	file := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(file, []byte("from-file\n"), 0600); err != nil { t.Fatal(err) }
	t.Setenv(key + "_FILE", file)
	got, err = Resolve(key)
	if err != nil || got != "from-file" { t.Fatalf("file %q %v", got, err) }
	t.Setenv(key, "both")
	if _, err := Resolve(key); err == nil { t.Fatal("conflicting secret sources accepted") }
}

func TestRejectSecretFileAbuse(t *testing.T) {
	const key = "AEGIS_TEST_BAD_FILE"
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("first\nsecond"), 0600); err != nil { t.Fatal(err) }
	t.Setenv(key + "_FILE", path)
	if _, err := Resolve(key); err == nil { t.Fatal("multiline secret accepted") }
	if err := os.WriteFile(path, []byte(strings.Repeat("z", maxSecretSize + 1)), 0600); err != nil { t.Fatal(err) }
	if _, err := Resolve(key); err == nil { t.Fatal("oversized secret accepted") }
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte("private"), 0600); err != nil { t.Fatal(err) }
		if err := os.Chmod(path, 0644); err != nil { t.Fatal(err) }
		if _, err := Resolve(key); err == nil { t.Fatal("public secret accepted") }
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err == nil {
		t.Setenv(key + "_FILE", link)
		if _, err := Resolve(key); err == nil { t.Fatal("symlink accepted") }
	}
}
