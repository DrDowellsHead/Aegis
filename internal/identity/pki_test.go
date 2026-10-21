package identity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tlsFixture(t *testing.T, dir, suffix, host string) (string, string, string) {
	t.Helper()
	ca := filepath.Join(dir, suffix+"-ca.pem")
	cert := filepath.Join(dir, suffix+"-cert.pem")
	key := filepath.Join(dir, suffix+"-key.pem")
	if err := GenerateDevelopmentTLS(host, ca, cert, key); err != nil {
		t.Fatal(err)
	}
	return ca, cert, key
}
func TestTLSCheckTrustSANKeyAndExpiryWarning(t *testing.T) {
	d := t.TempDir()
	ca, cert, key := tlsFixture(t, d, "one", "server.example.test")
	r, err := ValidateServerCertificate(cert, key, ca, "server.example.test", 0)
	if err != nil || !r.ChainVerified || r.Expiring || r.RemainingSeconds <= 0 || r.SHA256Fingerprint == "" {
		t.Fatalf("invalid report: %+v %v", r, err)
	}
	if _, err = ValidateServerCertificate(cert, key, ca, "other.example.test", 0); err == nil {
		t.Fatal("wrong SAN accepted")
	}
	if _, err = ValidateServerCertificate(cert, key, ca, "127.0.0.1", 0); err == nil {
		t.Fatal("wrong IP accepted")
	}
	unverified, err := ValidateServerCertificate(cert, key, "", "server.example.test", time.Until(r.NotAfter)+time.Hour)
	if err != nil || unverified.ChainVerified || !unverified.Expiring {
		t.Fatalf("incorrect unchecked trust report %+v %v", unverified, err)
	}
	otherCA, _, otherKey := tlsFixture(t, d, "other", "server.example.test")
	if _, err = ValidateServerCertificate(cert, otherKey, ca, "server.example.test", 0); err == nil {
		t.Fatal("wrong private key accepted")
	}
	if _, err = ValidateServerCertificate(cert, key, otherCA, "server.example.test", 0); err == nil {
		t.Fatal("wrong CA accepted")
	}
	if _, err = ValidateServerCertificate(cert, key, ca, "server.example.test", -time.Second); err == nil {
		t.Fatal("invalid warn interval")
	}
}
func TestTLSCARolloverTrustsBothIssuers(t *testing.T) {
	d := t.TempDir()
	oldCA, oldLeaf, oldKey := tlsFixture(t, d, "old", "server.example.test")
	newCA, newLeaf, newKey := tlsFixture(t, d, "new", "server.example.test")
	overlap := filepath.Join(d, "overlap.pem")
	count, err := MergeTrustBundles(oldCA, newCA, overlap)
	if err != nil || count != 2 {
		t.Fatalf("merge: %d %v", count, err)
	}
	_, certs, err := LoadTrustBundle(overlap)
	if err != nil || len(certs) != 2 {
		t.Fatalf("invalid trust bundle: %v", err)
	}
	for _, cert := range []struct{ leaf, key string }{{oldLeaf, oldKey}, {newLeaf, newKey}} {
		report, err := ValidateServerCertificate(cert.leaf, cert.key, overlap, "server.example.test", 0)
		if err != nil || !report.ChainVerified {
			t.Fatalf("broken trust rollover: %+v %v", report, err)
		}
	}
	if _, err = ValidateServerCertificate(newLeaf, newKey, oldCA, "server.example.test", 0); err == nil {
		t.Fatal("old CA trusted new server")
	}
	if _, err = MergeTrustBundles(oldCA, newCA, overlap); err == nil {
		t.Fatal("overwrote bundle")
	}
	if _, err = MergeTrustBundles(oldCA, newCA, oldCA); err == nil {
		t.Fatal("overwrote original CA")
	}
	dedup := filepath.Join(d, "dedup.pem")
	count, err = MergeTrustBundles(oldCA, oldCA, dedup)
	if err != nil || count != 1 {
		t.Fatalf("duplicate CA not deduplicated: %d %v", count, err)
	}
}
func TestTLSCABundleRejectsLeafKeyGarbage(t *testing.T) {
	d := t.TempDir()
	ca, cert, key := tlsFixture(t, d, "one", "server.example.test")
	for _, source := range []string{cert, key} {
		if _, _, err := LoadTrustBundle(source); err == nil {
			t.Fatalf("non-CA accepted: %s", source)
		}
	}
	data, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ParseTrustBundle(append(append([]byte{}, data...), keyPEM...)); err == nil {
		t.Fatal("mixed private key accepted")
	}
	if _, _, err = ParseTrustBundle(append(append([]byte{}, data...), []byte("garbage")...)); err == nil {
		t.Fatal("trailing garbage accepted")
	}
	if _, _, err = ParseTrustBundle(append([]byte("leading garbage\n"), data...)); err == nil {
		t.Fatal("leading garbage accepted")
	}
	if _, _, err = ParseTrustBundle([]byte("garbage")); err == nil {
		t.Fatal("invalid PEM accepted")
	}
	if _, err = MergeTrustBundles(ca, cert, filepath.Join(d, "bad.pem")); err == nil {
		t.Fatal("non CA rollover accepted")
	}
}
