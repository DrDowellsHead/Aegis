// Package appconfig loads non-secret, versioned startup profiles for the
// Aegis agent and telemetry receiver. Credentials never belong in this JSON.
package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxConfigSize = 64 << 10

type Config struct {
	Version  int
	Agent    *Agent
	Receiver *Receiver
}

type Agent struct {
	Server            string `json:"server"`
	CredentialsFile   string `json:"credentials_file"`
	CACertFile        string `json:"ca_cert_file"`
	Disk              string `json:"disk"`
	QueueDir          string `json:"queue_dir"`
	Interval          string `json:"interval"`
	Count             int    `json:"count"`
	MaxQueueMB        int    `json:"max_queue_mb"`
	RequestTimeout    string `json:"request_timeout"`
	HeartbeatInterval string `json:"heartbeat_interval"`
}

type Receiver struct {
	Listen            string `json:"listen"`
	TLSCertFile       string `json:"tls_cert_file"`
	TLSKeyFile        string `json:"tls_key_file"`
	TLSHost           string `json:"tls_host"`
	TLSCACertFile     string `json:"tls_ca_cert_file"`
	AgentsFile        string `json:"agents_file"`
	MaxInflight       int    `json:"max_inflight_samples"`
	MaxHeartbeats     int    `json:"max_inflight_heartbeats"`
	MaxPerAgent       int    `json:"max_per_agent_samples"`
	DiagnosticsListen string `json:"diagnostics_listen"`
}

func defaultAgent() Agent {
	return Agent{Server: "127.0.0.1:9090", Disk: ".", Interval: "5s",
		MaxQueueMB: 64, RequestTimeout: "5s", HeartbeatInterval: "15s"}
}

func defaultReceiver() Receiver {
	return Receiver{Listen: "127.0.0.1:9090", MaxInflight: 8, MaxHeartbeats: 2, MaxPerAgent: 2}
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid configuration structure: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

// rejectDuplicates prevents one parser accepting two contradictory values.
func rejectDuplicates(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		v, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := v.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for d.More() {
				keyValue, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyValue.(string)
				if !ok {
					return fmt.Errorf("invalid JSON key")
				}
				if seen[key] {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		case '[':
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	return walk()
}

func resolvePath(base, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(base, path))
}

func (a *Agent) resolve(base string) {
	a.CredentialsFile = resolvePath(base, a.CredentialsFile)
	a.CACertFile = resolvePath(base, a.CACertFile)
	a.Disk = resolvePath(base, a.Disk)
	a.QueueDir = resolvePath(base, a.QueueDir)
}

func (r *Receiver) resolve(base string) {
	r.TLSCertFile = resolvePath(base, r.TLSCertFile)
	r.TLSKeyFile = resolvePath(base, r.TLSKeyFile)
	r.TLSCACertFile = resolvePath(base, r.TLSCACertFile)
	r.AgentsFile = resolvePath(base, r.AgentsFile)
}

func validPath(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\x00\r\n") &&
		!strings.Contains(s, "://")
}

func validAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535
}

func betweenDuration(value string, min, max time.Duration, zeroOK bool) bool {
	d, err := time.ParseDuration(value)
	if err != nil {
		return false
	}
	return zeroOK && d == 0 || d >= min && d <= max
}

func (a Agent) Validate() error {
	if !validAddress(a.Server) {
		return fmt.Errorf("agent.server must be host:port")
	}
	if !validPath(a.CredentialsFile) || !validPath(a.CACertFile) || !validPath(a.Disk) {
		return fmt.Errorf("agent requires credentials_file, ca_cert_file and disk paths")
	}
	if a.QueueDir != "" && !validPath(a.QueueDir) {
		return fmt.Errorf("invalid agent.queue_dir")
	}
	if !betweenDuration(a.Interval, 100*time.Millisecond, 24*time.Hour, false) {
		return fmt.Errorf("agent.interval must be 100ms..24h")
	}
	if !betweenDuration(a.RequestTimeout, time.Nanosecond, time.Minute, false) {
		return fmt.Errorf("agent.request_timeout must be greater than 0 and no more than 1m")
	}
	if !betweenDuration(a.HeartbeatInterval, time.Second, time.Hour, true) {
		return fmt.Errorf("agent.heartbeat_interval must be 0 or 1s..1h")
	}
	if a.Count < 0 || a.MaxQueueMB < 1 || a.MaxQueueMB > 1024 {
		return fmt.Errorf("agent.count must be nonnegative and max_queue_mb 1..1024")
	}
	return nil
}

func (r Receiver) Validate() error {
	if !validAddress(r.Listen) {
		return fmt.Errorf("receiver.listen must be host:port")
	}
	if !validPath(r.TLSCertFile) || !validPath(r.TLSKeyFile) || !validPath(r.AgentsFile) {
		return fmt.Errorf("receiver requires tls_cert_file, tls_key_file, agents_file")
	}
	if r.TLSCACertFile != "" && (!validPath(r.TLSCACertFile) || r.TLSHost == "") {
		return fmt.Errorf("receiver.tls_ca_cert_file requires tls_host")
	}
	if r.MaxInflight < 1 || r.MaxInflight > 64 ||
		r.MaxHeartbeats < 1 || r.MaxHeartbeats > 16 ||
		r.MaxPerAgent < 1 || r.MaxPerAgent > 8 || r.MaxPerAgent > r.MaxInflight {
		return fmt.Errorf("receiver limits must be samples 1..64, heartbeats 1..16, per-agent 1..8 and <= samples")
	}
	if r.DiagnosticsListen != "" {
		host, _, err := net.SplitHostPort(r.DiagnosticsListen)
		ip := net.ParseIP(host)
		if err != nil || !validAddress(r.DiagnosticsListen) || ip == nil || !ip.IsLoopback() || strings.Contains(host, "%") {
			return fmt.Errorf("receiver.diagnostics_listen must use numeric loopback IP")
		}
	}
	return nil
}

// Load validates the entire configuration before starting a process. Paths
// are resolved relative to the config file, not the caller's working directory.
func Load(filename string) (Config, error) {
	if filename == "" {
		return Config{}, fmt.Errorf("--config is required")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxConfigSize {
		return Config{}, fmt.Errorf("configuration must be a regular file of at most 64 KiB")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if len(raw) > maxConfigSize {
		return Config{}, fmt.Errorf("configuration exceeds 64 KiB")
	}
	if err := rejectDuplicates(raw); err != nil {
		return Config{}, err
	}
	var envelope struct {
		Version  int             `json:"version"`
		Agent    json.RawMessage `json:"agent"`
		Receiver json.RawMessage `json:"receiver"`
	}
	if err := decodeStrict(raw, &envelope); err != nil {
		return Config{}, err
	}
	if envelope.Version != 1 {
		return Config{}, fmt.Errorf("configuration version must be 1")
	}
	var cfg Config
	cfg.Version = 1
	if len(envelope.Agent) > 0 && string(envelope.Agent) != "null" {
		a := defaultAgent()
		if err := decodeStrict(envelope.Agent, &a); err != nil {
			return Config{}, err
		}
		a.resolve(filepath.Dir(filename))
		if err := a.Validate(); err != nil {
			return Config{}, err
		}
		cfg.Agent = &a
	}
	if len(envelope.Receiver) > 0 && string(envelope.Receiver) != "null" {
		r := defaultReceiver()
		if err := decodeStrict(envelope.Receiver, &r); err != nil {
			return Config{}, err
		}
		r.resolve(filepath.Dir(filename))
		if err := r.Validate(); err != nil {
			return Config{}, err
		}
		cfg.Receiver = &r
	}
	if cfg.Agent == nil && cfg.Receiver == nil {
		return Config{}, fmt.Errorf("at least one agent or receiver section is required")
	}
	return cfg, nil
}

func (a Agent) Flags() []string {
	return []string{
		"--server", a.Server, "--credentials", a.CredentialsFile, "--ca-cert", a.CACertFile,
		"--disk", a.Disk, "--queue-dir", a.QueueDir, "--interval", a.Interval,
		"--count", strconv.Itoa(a.Count), "--max-queue-mb", strconv.Itoa(a.MaxQueueMB),
		"--request-timeout", a.RequestTimeout, "--heartbeat-interval", a.HeartbeatInterval,
	}
}

func (r Receiver) Flags() []string {
	return []string{
		"--listen", r.Listen, "--tls-cert", r.TLSCertFile, "--tls-key", r.TLSKeyFile,
		"--tls-host", r.TLSHost, "--tls-ca-cert", r.TLSCACertFile, "--agents-file", r.AgentsFile,
		"--max-inflight-samples", strconv.Itoa(r.MaxInflight),
		"--max-inflight-heartbeats", strconv.Itoa(r.MaxHeartbeats),
		"--max-per-agent-samples", strconv.Itoa(r.MaxPerAgent),
		"--diagnostics-listen", r.DiagnosticsListen,
	}
}
