package lifecycle

import "testing"

func TestServiceName(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"start", "--config", "C:/Aegis/config.json", "--component", "agent"}, "AegisAgent"},
		{[]string{"start", "--component=receiver", "--config", "config.json"}, "AegisReceiver"},
		{[]string{"start", "--component", "agent", "--component", "receiver"}, ""},
		{[]string{"start", "--component", "unknown"}, ""},
		{[]string{"forward", "--component", "agent"}, ""},
		{[]string{"start"}, ""},
	}
	for _, tc := range tests {
		got, ok := ServiceName(tc.args)
		if got != tc.want || ok != (tc.want != "") {
			t.Fatalf("args=%v got=%q ok=%v want=%q", tc.args, got, ok, tc.want)
		}
	}
}
