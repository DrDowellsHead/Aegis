package identity

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// ServerCertificateReport contains public certificate metadata only.
// Never serialize private keys or credentials in diagnostics.
type ServerCertificateReport struct {
	Subject           string    `json:"subject"`
	SerialNumber      string    `json:"serial_number"`
	SHA256Fingerprint string    `json:"sha256_fingerprint"`
	DNSNames          []string  `json:"dns_names"`
	IPAddresses       []string  `json:"ip_addresses"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	RemainingSeconds  int64     `json:"remaining_seconds"`
	ChainVerified     bool      `json:"chain_verified"`
	Expiring          bool      `json:"expiring"`
}

// ValidateServerCertificate verifies the matching private key, server EKU,
// validity, optional hostname/IP SAN and, when roots are supplied, the full
// signed chain. Lack of a root bundle is never presented as verified trust.
func ValidateServerCertificate(certPath, keyPath, rootsPath, hostname string, warnBefore time.Duration) (ServerCertificateReport, error) {
	if certPath == "" || keyPath == "" || warnBefore < 0 {
		return ServerCertificateReport{}, fmt.Errorf("certificate, key and nonnegative warning interval are required")
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return ServerCertificateReport{}, fmt.Errorf("certificate/key pair invalid: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return ServerCertificateReport{}, fmt.Errorf("server certificate is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return ServerCertificateReport{}, fmt.Errorf("parse server certificate: %w", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return ServerCertificateReport{}, fmt.Errorf("server certificate is not currently valid")
	}
	if leaf.IsCA || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 ||
		!hasServerAuth(leaf.ExtKeyUsage) {
		return ServerCertificateReport{}, fmt.Errorf("certificate is not suitable for TLS server authentication")
	}
	if hostname != "" {
		if err := leaf.VerifyHostname(hostname); err != nil {
			return ServerCertificateReport{}, fmt.Errorf("server certificate SAN does not match requested host: %w", err)
		}
	}
	report := ServerCertificateReport{
		Subject: leaf.Subject.String(), SerialNumber: leaf.SerialNumber.String(),
		SHA256Fingerprint: hex.EncodeToString(sha256Sum(leaf.Raw)),
		DNSNames: append([]string{}, leaf.DNSNames...),
		IPAddresses: make([]string, 0, len(leaf.IPAddresses)),
		NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(),
		RemainingSeconds: int64(leaf.NotAfter.Sub(now).Seconds()),
		Expiring: leaf.NotAfter.Sub(now) <= warnBefore,
	}
	for _, ip := range leaf.IPAddresses {
		report.IPAddresses = append(report.IPAddresses, ip.String())
	}
	if rootsPath != "" {
		roots, _, err := LoadTrustBundle(rootsPath)
		if err != nil {
			return ServerCertificateReport{}, fmt.Errorf("load trust roots: %w", err)
		}
		intermediates := x509.NewCertPool()
		for _, raw := range pair.Certificate[1:] {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return ServerCertificateReport{}, fmt.Errorf("parse intermediate certificate: %w", err)
			}
			intermediates.AddCert(cert)
		}
		_, err = leaf.Verify(x509.VerifyOptions{
			Roots: roots, Intermediates: intermediates, DNSName: hostname,
			CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if err != nil {
			return ServerCertificateReport{}, fmt.Errorf("server certificate chain not trusted: %w", err)
		}
		report.ChainVerified = true
	}
	return report, nil
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func hasServerAuth(usages []x509.ExtKeyUsage) bool {
	for _, usage := range usages {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

const maxCABundleSize = 1 << 20
const maxCACertificates = 32

// ParseTrustBundle requires PEM CERTIFICATE blocks containing CA certificates.
// Rejecting unrelated blocks and trailing data prevents a malformed or
// accidentally mixed private-key file from being silently trusted.
func ParseTrustBundle(data []byte) (*x509.CertPool, []*x509.Certificate, error) {
	if len(data) == 0 || len(data) > maxCABundleSize {
		return nil, nil, fmt.Errorf("CA bundle must contain 1..1048576 bytes")
	}
	roots := x509.NewCertPool()
	certs := make([]*x509.Certificate, 0, 2)
	seen := make(map[string]struct{})
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, nil, fmt.Errorf("CA bundle contains invalid PEM data")
		}
		data = rest
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid CA certificate: %w", err)
		}
		if !cert.IsCA || !cert.BasicConstraintsValid ||
			cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, nil, fmt.Errorf("trust bundle contains a non-CA certificate")
		}
		if time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return nil, nil, fmt.Errorf("trust bundle contains an expired or not-yet-valid CA")
		}
		fingerprint := hex.EncodeToString(sha256Sum(cert.Raw))
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		certs = append(certs, cert)
		roots.AddCert(cert)
		if len(certs) > maxCACertificates {
			return nil, nil, fmt.Errorf("too many CA certificates in trust bundle")
		}
	}
	if len(certs) == 0 {
		return nil, nil, fmt.Errorf("trust bundle is empty")
	}
	return roots, certs, nil
}

func LoadTrustBundle(path string) (*x509.CertPool, []*x509.Certificate, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("CA bundle path required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect CA bundle: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxCABundleSize {
		return nil, nil, fmt.Errorf("CA bundle must be a regular file within size limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA bundle: %w", err)
	}
	return ParseTrustBundle(data)
}

// MergeTrustBundles writes a NEW trust file containing the union of current
// and next CAs. It does not overwrite either input or any existing output.
// Operators must authenticate the next CA fingerprint out of band.
func MergeTrustBundles(currentPath, nextPath, destination string) (int, error) {
	if currentPath == "" || nextPath == "" || destination == "" {
		return 0, fmt.Errorf("current CA, next CA and destination are required")
	}
	// Never accept a destination equal to either input.
	for _, source := range []string{currentPath, nextPath} {
		same, err := sameAbsolutePath(source, destination)
		if err != nil {
			return 0, err
		}
		if same {
			return 0, fmt.Errorf("trust output must not overwrite inputs")
		}
	}
	_, first, err := LoadTrustBundle(currentPath)
	if err != nil {
		return 0, err
	}
	_, second, err := LoadTrustBundle(nextPath)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	var body []byte
	for _, cert := range append(first, second...) {
		id := hex.EncodeToString(sha256Sum(cert.Raw))
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		body = append(body, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}
	if len(body) > maxCABundleSize {
		return 0, fmt.Errorf("merged CA bundle exceeds maximum size")
	}
	if err := writeExclusive(destination, body); err != nil {
		return 0, fmt.Errorf("create new trust bundle: %w", err)
	}
	return len(seen), nil
}
