package identity

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateDevelopmentTLS(t *testing.T) {
	d := t.TempDir()
	ca := filepath.Join(d, "ca.crt")
	cert := filepath.Join(d, "server.crt")
	key := filepath.Join(d, "server.key")
	if err := GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err != nil { t.Fatal(err) }
	if err := GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err == nil { t.Fatal("overwrote existing private key") }
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil { t.Fatal(err) }
	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil { t.Fatal(err) }
	caPEM, err := os.ReadFile(ca)
	if err != nil { t.Fatal(err) }
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) { t.Fatal("bad CA PEM") }
	if _, err := parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "127.0.0.1", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil { t.Fatal(err) }
	if _, err := parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil { t.Fatal("accepted certificate for wrong host") }
}
