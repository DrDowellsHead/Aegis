package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"

	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

type Account struct {
	Username     string `json:"username"`
	Role         Role   `json:"role"`
	PasswordHash string `json:"password_hash"`
}

type accountFile struct {
	Version int       `json:"version"`
	Users   []Account `json:"users"`
}

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,31}$`)

func validUsername(name string) bool { return usernamePattern.MatchString(name) }

func LoadAccounts(path string) ([]Account, error) {
	if path == "" {
		return nil, fmt.Errorf("dashboard users file is required")
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect dashboard users file: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("dashboard users path must be a regular file")
	}
	if runtime.GOOS != "windows" && stat.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("dashboard users file permissions must be 0600 or stricter")
	}
	if stat.Size() > 1<<20 {
		return nil, fmt.Errorf("dashboard users file is too large")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dashboard users file: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	dec.DisallowUnknownFields()
	var data accountFile
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode dashboard users file: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("dashboard users file must contain one JSON document")
	}
	if data.Version != 1 || len(data.Users) == 0 || len(data.Users) > 200 {
		return nil, fmt.Errorf("invalid dashboard users file version or size")
	}
	seen := make(map[string]bool, len(data.Users))
	admins := 0
	for _, u := range data.Users {
		if !validUsername(u.Username) || seen[u.Username] ||
			(u.Role != RoleAdmin && u.Role != RoleViewer) {
			return nil, fmt.Errorf("invalid or duplicate dashboard username or role")
		}
		seen[u.Username] = true
		if _, err := bcrypt.Cost([]byte(u.PasswordHash)); err != nil {
			return nil, fmt.Errorf("invalid password hash for %q", u.Username)
		}
		if u.Role == RoleAdmin {
			admins++
		}
	}
	if admins == 0 {
		return nil, fmt.Errorf("at least one dashboard administrator is required")
	}
	return data.Users, nil
}

// SaveAccount changes the offline account file atomically on the same filesystem.
// Restart the dashboard after any change to revoke all old sessions.
func SaveAccount(path, username string, role Role, password string, remove, replace bool) error {
	if !validUsername(username) {
		return fmt.Errorf("username must be 3..32 lowercase ASCII letters, digits, dot, underscore or dash")
	}
	if remove && replace {
		return fmt.Errorf("remove and replace cannot be used together")
	}
	var users []Account
	if _, err := os.Stat(path); err == nil {
		users, err = LoadAccounts(path)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if remove {
		if len(users) == 0 {
			return fmt.Errorf("no dashboard users exist")
		}
		found := false
		remaining := make([]Account, 0, len(users))
		for _, u := range users {
			if u.Username == username {
				found = true
			} else {
				remaining = append(remaining, u)
			}
		}
		if !found {
			return fmt.Errorf("dashboard user not found")
		}
		users = remaining
	} else {
		if role != RoleAdmin && role != RoleViewer {
			return fmt.Errorf("role must be admin or viewer")
		}
		if len(password) < 16 || len(password) > 72 {
			return fmt.Errorf("password must be 16..72 bytes (bcrypt limit)")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash dashboard password: %w", err)
		}
		found := false
		for i := range users {
			if users[i].Username == username {
				if !replace {
					return fmt.Errorf("dashboard user already exists (use --replace)")
				}
				users[i].PasswordHash = string(hash)
				users[i].Role = role
				found = true
			}
		}
		if !found {
			if replace {
				return fmt.Errorf("cannot replace non-existent dashboard user")
			}
			users = append(users, Account{Username: username, Role: role, PasswordHash: string(hash)})
		}
	}
	admins := 0
	for _, u := range users {
		if u.Role == RoleAdmin {
			admins++
		}
	}
	if admins == 0 || len(users) > 200 {
		return fmt.Errorf("a dashboard requires 1..200 users with at least one administrator")
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	out, err := json.MarshalIndent(accountFile{Version: 1, Users: users}, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".aegis-users-*")
	if err != nil {
		return fmt.Errorf("create private users file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace dashboard users file: %w", err)
	}
	return nil
}
