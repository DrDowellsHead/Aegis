package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateDevelopmentTLS makes a private local CA and a 90-day server cert.
// Only the public CA certificate is written; its signing key is discarded.
// This is a bootstrap convenience, not a production certificate authority.
func GenerateDevelopmentTLS(host, caPath, certPath, keyPath string) error {
	if host == "" || strings.TrimSpace(host) != host || strings.ContainsAny(host, "/\\ :") {
		if net.ParseIP(host) == nil {
			return fmt.Errorf("host must be a DNS name or bare IP address")
		}
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || !validDNS(host) {
			return fmt.Errorf("invalid server DNS name")
		}
	}
	seen := make(map[string]bool)
	for _, path := range []string{caPath, certPath, keyPath} {
		if path == "" {
			return fmt.Errorf("certificate paths must not be empty")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if seen[abs] {
			return fmt.Errorf("certificate paths must differ")
		}
		seen[abs] = true
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("refusing to overwrite %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	serverPublic, serverPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	caSerial, err := randomSerial()
	if err != nil {
		return err
	}
	serverSerial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: "Aegis development CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if err != nil {
		return err
	}
	leaf := &x509.Certificate{
		SerialNumber: serverSerial, Subject: pkix.Name{CommonName: host},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		leaf.IPAddresses = []net.IP{ip}
	} else {
		leaf.DNSNames = []string{host}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, serverPublic, caPrivate)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(serverPrivate)
	if err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
	}{
		{caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})},
		{certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})},
		{keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})},
	}
	created := make([]string, 0, 3)
	for _, file := range files {
		if err := writeExclusive(file.path, file.data); err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			return fmt.Errorf("create certificate %s: %w", file.path, err)
		}
		created = append(created, file.path)
	}
	return nil
}

func validDNS(host string) bool {
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' ||
				ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

func randomSerial() (*big.Int, error) {
	for {
		n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, fmt.Errorf("generate certificate serial: %w", err)
		}
		if n.Sign() > 0 {
			return n, nil
		}
	}
}
