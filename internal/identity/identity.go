// Package identity manages long-lived agent credentials and server enrollment.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Credentials struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

type registryFile struct {
	Agents map[string]string `json:"agents"` // agent UUID -> SHA-256 of random token (hex)
}

type Registry struct {
	hashes map[string][sha256.Size]byte
}

func ValidID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return id[14] == '4' && strings.ContainsRune("89ab", rune(id[19]))
}

func validToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func newCredentials() (Credentials, error) {
	var id [16]byte
	var token [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Credentials{}, fmt.Errorf("generate ID: %w", err)
	}
	if _, err := rand.Read(token[:]); err != nil {
		return Credentials{}, fmt.Errorf("generate token: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	h := hex.EncodeToString(id[:])
	return Credentials{
		AgentID: h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:],
		Token:   hex.EncodeToString(token[:]),
	}, nil
}

// DefaultPath is stable across restarts; it never relies on the working directory.
func DefaultPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "aegis", "agent.json"), nil
}

// readSecretFile rejects symlinks and world/group-readable Unix permissions.
func readSecretFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("credential file must be regular: %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("credential file must have permissions 0600: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20+1))
}

func decodeStrict(data []byte, target any) error {
	if len(data) > 1<<20 {
		return fmt.Errorf("credential file too large")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing credential data")
	}
	return nil
}

func LoadCredentials(path string) (Credentials, error) {
	data, err := readSecretFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("read agent credentials: %w", err)
	}
	var creds Credentials
	if err := decodeStrict(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("decode agent credentials: %w", err)
	}
	if !ValidID(creds.AgentID) || !validToken(creds.Token) {
		return Credentials{}, fmt.Errorf("invalid agent credentials")
	}
	return creds, nil
}

func readRegistry(path string) (registryFile, error) {
	data, err := readSecretFile(path)
	if err != nil {
		return registryFile{}, err
	}
	var file registryFile
	if err := decodeStrict(data, &file); err != nil {
		return registryFile{}, fmt.Errorf("decode agent registry: %w", err)
	}
	if file.Agents == nil {
		return registryFile{}, fmt.Errorf("registry missing agents")
	}
	for id, hash := range file.Agents {
		if !ValidID(id) || !validToken(hash) {
			return registryFile{}, fmt.Errorf("invalid entry in agent registry")
		}
	}
	return file, nil
}

func LoadRegistry(path string) (Registry, error) {
	file, err := readRegistry(path)
	if err != nil {
		return Registry{}, fmt.Errorf("read agent registry: %w", err)
	}
	r := Registry{hashes: make(map[string][sha256.Size]byte, len(file.Agents))}
	for id, raw := range file.Agents {
		decoded, err := hex.DecodeString(raw)
		if err != nil {
			return Registry{}, fmt.Errorf("invalid agent registry digest")
		}
		var digest [sha256.Size]byte
		copy(digest[:], decoded)
		r.hashes[id] = digest
	}
	return r, nil
}

// Authenticate does not reveal whether the ID or the token was incorrect.
func (r Registry) Authenticate(id, token string) bool {
	if !ValidID(id) || !validToken(token) {
		return false
	}
	expected, ok := r.hashes[id]
	computed := sha256.Sum256([]byte(token))
	return ok && subtle.ConstantTimeCompare(expected[:], computed[:]) == 1
}

func ensureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0700)
}

func writeExclusive(path string, data []byte) (err error) {
	if err := ensureParent(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func writeAtomic(path string, data []byte) (err error) {
	if err := ensureParent(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".aegis-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Provision creates one independent secret for an agent and its server-side
// SHA-256 digest. Do not provision concurrently against the same registry.
// The two files are not a single filesystem transaction.
func Provision(registryPath, clientPath string) (string, error) {
	if registryPath == "" || clientPath == "" {
		return "", fmt.Errorf("both file paths are required")
	}
	serverAbs, err := filepath.Abs(registryPath)
	if err != nil {
		return "", err
	}
	clientAbs, err := filepath.Abs(clientPath)
	if err != nil {
		return "", err
	}
	if serverAbs == clientAbs {
		return "", fmt.Errorf("registry and client file must differ")
	}
	if _, err := os.Lstat(clientPath); err == nil {
		return "", fmt.Errorf("client credentials already exist; refusing overwrite")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	file, err := readRegistry(registryPath)
	existing := err == nil
	if os.IsNotExist(err) {
		file = registryFile{Agents: make(map[string]string)}
	} else if err != nil {
		return "", fmt.Errorf("load registry: %w", err)
	}
	creds, err := newCredentials()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(creds.Token))
	file.Agents[creds.AgentID] = hex.EncodeToString(hash[:])
	registryData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	clientData, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return "", err
	}
	if existing {
		if err := writeAtomic(registryPath, append(registryData, '\n')); err != nil {
			return "", fmt.Errorf("write agent registry: %w", err)
		}
	} else {
		if err := writeExclusive(registryPath, append(registryData, '\n')); err != nil {
			return "", fmt.Errorf("create agent registry: %w", err)
		}
	}
	if err := writeExclusive(clientPath, append(clientData, '\n')); err != nil {
		return "", fmt.Errorf("write client credentials (registry may contain an unused entry): %w", err)
	}
	return creds.AgentID, nil
}
