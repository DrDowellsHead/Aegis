package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RotateToken generates a fresh 256-bit bearer token for an existing agent.
// The UUID stays unchanged, so PostgreSQL telemetry and incident history
// remain attached to the same identity.
//
// The new client file is created exclusively BEFORE the registry is changed.
// If publishing the new digest fails, the newly created file is removed.
// The files cannot be committed as one filesystem transaction: operators must
// serialize registry modifications and restart the receiver after rotation.
func RotateToken(registryPath, credentialsPath, agentID string) error {
	if registryPath == "" || credentialsPath == "" || !ValidID(agentID) {
		return fmt.Errorf("rotation requires registry, new credential file, and valid agent ID")
	}
	if err := distinctCredentialPaths(registryPath, credentialsPath); err != nil {
		return err
	}
	registry, err := readRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("load agent registry: %w", err)
	}
	if _, ok := registry.Agents[agentID]; !ok {
		return fmt.Errorf("agent not found in registry")
	}
	if err := rejectExistingPath(credentialsPath); err != nil {
		return err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("generate replacement token: %w", err)
	}
	credentials := Credentials{AgentID: agentID, Token: hex.EncodeToString(token[:])}
	digest := sha256.Sum256([]byte(credentials.Token))
	clientJSON, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	if err := writeExclusive(credentialsPath, append(clientJSON, '\n')); err != nil {
		return fmt.Errorf("write new agent credentials: %w", err)
	}
	registry.Agents[agentID] = hex.EncodeToString(digest[:])
	if err := persistRegistry(registryPath, registry); err != nil {
		// Best-effort cleanup: if removal fails, report it without
		// embedding credential bytes. The old server registry remains valid.
		if removeErr := os.Remove(credentialsPath); removeErr != nil {
			return fmt.Errorf("registry unchanged; cleanup of new credentials failed: %w", removeErr)
		}
		return fmt.Errorf("registry unchanged; failed to publish replacement token: %w", err)
	}
	return nil
}

// RevokeAgent removes only the specified agent's server-side token digest.
// The agent's PostgreSQL history is deliberately untouched. An already
// running gRPC receiver MUST be restarted to load this change.
func RevokeAgent(registryPath, agentID string) error {
	if registryPath == "" || !ValidID(agentID) {
		return fmt.Errorf("revocation requires registry and valid agent ID")
	}
	registry, err := readRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("load agent registry: %w", err)
	}
	if _, ok := registry.Agents[agentID]; !ok {
		return fmt.Errorf("agent not found in registry")
	}
	delete(registry.Agents, agentID)
	if err := persistRegistry(registryPath, registry); err != nil {
		return fmt.Errorf("revoke agent credential: %w", err)
	}
	return nil
}

func rejectExistingPath(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("new credentials file already exists; refusing overwrite")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect new credentials path: %w", err)
	}
	return nil
}

func distinctCredentialPaths(registryPath, credentialsPath string) error {
	server, err := filepath.Abs(registryPath)
	if err != nil {
		return err
	}
	client, err := filepath.Abs(credentialsPath)
	if err != nil {
		return err
	}
	if server == client {
		return fmt.Errorf("registry and new credentials paths must differ")
	}
	return nil
}

func persistRegistry(path string, r registryFile) error {
	// readRegistry has already verified the original registry file.
	// Do not silently create a new registry on a typo: revocation and
	// rotation only apply to an existing, explicitly named registry.
	if r.Agents == nil {
		return fmt.Errorf("invalid agent registry")
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}
