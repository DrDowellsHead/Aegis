package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestWatchCLIValidatesArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"watch","--help"},0},
		{[]string{"watch","--bad"},2},
		{[]string{"watch","extra"},2},
		{[]string{"watch","--interval","5s"},2},
		{[]string{"watch","--interval","1h1s"},2},
		{[]string{"watch","--interval","15ms"},2},
		{[]string{"watch","--offline-after","5s"},2},
		{[]string{"watch","--offline-after","25h"},2},
	} {
		var out, errOut bytes.Buffer
		got:=Run(context.Background(),tc.args,&out,&errOut,"test")
		if got!=tc.code {
			t.Fatalf("%v: exit code %d, expected %d: %s",
				tc.args,got,tc.code,errOut.String())
		}
	}
}
