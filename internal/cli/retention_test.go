package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestRetentionRequiresExplicitScopeBeforeAnyDatabaseConnection(t *testing.T) {
	t.Setenv("AEGIS_DATABASE_URL", "")
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"retention"}, 2},
		{[]string{"retention", "--days", "0"}, 2},
		{[]string{"retention", "--days", "6"}, 2},
		{[]string{"retention", "--days", "30", "--apply"}, 2},
		{[]string{"retention", "--days", "30", "--apply", "--all-agents", "--agent-id", "123e4567-e89b-42d3-a456-426614174000"}, 2},
		{[]string{"retention", "--days", "30", "--apply", "--batch-size", "0", "--all-agents"}, 2},
		{[]string{"retention", "--days", "30", "--apply", "--max-delete", "0", "--all-agents"}, 2},
		{[]string{"retention", "--days", "30", "--apply", "--all-agents"}, 1},
		{[]string{"retention", "--days", "30"}, 1},
		{[]string{"storage"}, 1},
	} {
		var stdout, stderr bytes.Buffer
		got := Run(context.Background(), tc.args, &stdout, &stderr, "test")
		if got != tc.want {
			t.Errorf("args=%v code=%d want=%d stderr=%s", tc.args, got, tc.want, stderr.String())
		}
	}
}
