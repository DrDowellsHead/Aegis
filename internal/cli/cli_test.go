package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestCommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"empty", nil, 2}, {"unknown", []string{"wat"}, 2},
		{"version", []string{"version"}, 0}, {"version extra", []string{"version", "extra"}, 2},
		{"help", []string{"help"}, 0}, {"serve help", []string{"serve", "-h"}, 0},
		{"bad flag", []string{"serve", "--bad"}, 2}, {"extra", []string{"serve", "extra"}, 2},
		{"bad address", []string{"serve", "--listen", "bad-address"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := Run(context.Background(), tc.args, &out, &errOut, "test"); code != tc.code {
				t.Fatalf("code=%d want %d: %s", code, tc.code, &errOut)
			}
			if tc.name == "version" && out.String() != "aegis test\n" {
				t.Fatal(out.String())
			}
		})
	}
}

func TestCheckValidation(t *testing.T) {
	for _, args := range [][]string{{"check"}, {"check", "--url", "ftp://host"}, {"check", "--url", "http://host", "--timeout", "0s"}, {"check", "extra"}} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), args, &out, &errOut, "test"); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
}
