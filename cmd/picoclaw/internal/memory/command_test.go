package memory

import (
	"strings"
	"testing"
)

func TestForgetCommandRequiresExpectedRevisionForExecute(t *testing.T) {
	cmd := newForgetCommand()
	cmd.SetArgs([]string{"entry-id", "--yes"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "expected-revision") {
		t.Fatalf("forget --yes error = %v, want expected-revision guard", err)
	}
}

func TestReplayForgetCommandDefaultsToDryRun(t *testing.T) {
	cmd := newReplayForgetCommand()
	flag := cmd.Flags().Lookup("yes")
	if flag == nil {
		t.Fatal("replay-forget missing --yes flag")
	}
	if flag.DefValue != "false" {
		t.Fatalf("replay --yes default = %q, want false", flag.DefValue)
	}
}
