package node

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// An agent that ended cleanly (status 0) without answering is said as such, not "%!w(<nil>)"; one
// that failed keeps its exit error, unwrappable.
func TestAgentExitIsSaidWithoutNil(t *testing.T) {
	done := make(chan struct{})
	close(done)
	call := errors.New("peer disconnected")
	clean := &acpProc{done: done}
	if got := procErr(clean, call).Error(); strings.Contains(got, "%!") || strings.Contains(got, "<nil>") || !strings.Contains(got, "status 0") {
		t.Fatalf("clean exit: %q", got)
	}
	if !errors.Is(procErr(clean, call), call) {
		t.Fatal("the call's error is not kept")
	}
	if got := exitText(clean); strings.Contains(got, "<nil>") {
		t.Fatalf("clean exit text: %q", got)
	}
	exit := exec.Command("sh", "-c", "exit 3").Run()
	failed := &acpProc{done: done, exitErr: exit}
	var ee *exec.ExitError
	if got := procErr(failed, call); !errors.As(got, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("failed exit: %v", got)
	}
}
