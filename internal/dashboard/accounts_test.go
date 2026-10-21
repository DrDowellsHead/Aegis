package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAccountLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if err := SaveAccount(path, "operator", RoleAdmin, "a-strong-example-password", false, false); err != nil {
		t.Fatal(err)
	}
	users, err := LoadAccounts(path)
	if err != nil || len(users) != 1 || users[0].Username != "operator" {
		t.Fatalf("users=%v err=%v", users, err)
	}
	if strings.Contains(users[0].PasswordHash, "a-strong-example-password") ||
		bcrypt.CompareHashAndPassword([]byte(users[0].PasswordHash), []byte("a-strong-example-password")) != nil {
		t.Fatal("stored hash is not bcrypt")
	}
	if err := SaveAccount(path, "reader", RoleViewer, "another-example-password", false, false); err != nil {
		t.Fatal(err)
	}
	if err := SaveAccount(path, "operator", RoleViewer, "replacement-example-password", false, true); err == nil {
		t.Fatal("last administrator could be downgraded")
	}
	if err := SaveAccount(path, "operator", "", "", true, false); err == nil {
		t.Fatal("last administrator could be removed")
	}
	if err := SaveAccount(path, "reader", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if err := SaveAccount(path, "operator", RoleAdmin, "replacement-example-password", false, true); err != nil {
		t.Fatal(err)
	}
	users, err = LoadAccounts(path)
	if err != nil || len(users) != 1 || bcrypt.CompareHashAndPassword([]byte(users[0].PasswordHash), []byte("replacement-example-password")) != nil {
		t.Fatalf("replacement invalid: %v", err)
	}
}

func TestAccountFileRejectsMalformedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	for _, contents := range []string{
		`{}`,
		`{"version":1,"users":[]}`,
		`{"version":1,"users":[{"username":"operator","role":"admin","password_hash":"plaintext"}]}`,
		`{"version":1,"users":[{"username":"bad name","role":"admin","password_hash":"plaintext"}]}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAccounts(path); err == nil {
			t.Fatalf("accepted invalid account file: %s", contents)
		}
	}
}

func TestSaveAccountValidatesInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	for _, name := range []string{"UPPER", "../wrong", "ab", "a b"} {
		if err := SaveAccount(path, name, RoleAdmin, "a-strong-example-password", false, false); err == nil {
			t.Errorf("accepted username %q", name)
		}
	}
	if err := SaveAccount(path, "operator", RoleAdmin, "short", false, false); err == nil {
		t.Fatal("accepted short password")
	}
}
