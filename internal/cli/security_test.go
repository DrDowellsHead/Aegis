package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

func TestProvisionAndCertificateCLI(t *testing.T) {
	d := t.TempDir()
	registry := filepath.Join(d, "registry.json")
	client := filepath.Join(d, "client.json")
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"provision","--agents-file",registry,"--credentials",client}, &out,&errOut,"test")
	if code != 0 { t.Fatalf("provision exit=%d: %s",code,errOut.String()) }
	creds, err := identity.LoadCredentials(client)
	if err != nil || !identity.ValidID(creds.AgentID) { t.Fatalf("credentials=%+v err=%v",creds.AgentID,err) }
	if bytes.Contains(out.Bytes(),[]byte(creds.Token)) { t.Fatal("secret printed to stdout") }
	ca,cert,key := filepath.Join(d,"ca.crt"),filepath.Join(d,"server.crt"),filepath.Join(d,"server.key")
	out.Reset()
	errOut.Reset()
	code = Run(context.Background(),[]string{"cert","--ca-cert",ca,"--tls-cert",cert,"--tls-key",key},&out,&errOut,"test")
	if code != 0 { t.Fatalf("cert exit=%d: %s",code,errOut.String()) }
}

func TestSecureFlagsAreMandatory(t *testing.T) {
	for _, args := range [][]string{
		{"receive"}, {"send"}, {"provision"}, {"cert"},
		{"send","--ca-cert","unused","--agent-id","spoofed"},
	} {
		var out,errOut bytes.Buffer
		if code := Run(context.Background(),args,&out,&errOut,"test"); code != 2 {
			t.Fatalf("%v exit=%d stderr=%s",args,code,errOut.String())
		}
	}
}
