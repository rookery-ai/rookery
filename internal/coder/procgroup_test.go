package coder

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// setProcGroup must wire both the platform process-group attribute and a
// Cancel hook. Without Cancel, exec.CommandContext signals only the direct
// child and a coder that shelled out to python leaves orphans behind.
func TestSetProcGroupWiresCancel(t *testing.T) {
	cmd := exec.Command("go", "version")
	setProcGroup(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("setProcGroup left SysProcAttr nil")
	}
	if cmd.Cancel == nil {
		t.Fatal("setProcGroup left Cancel nil")
	}
	// Cancel on a process that never started must be a no-op, not a panic:
	// buildCommand wires Cancel before Start, and a failed Start still runs it.
	if err := cmd.Cancel(); err != nil {
		t.Fatalf("Cancel on unstarted process returned %v, want nil", err)
	}
}

// The whole point of the helper is TREE termination. Spawn a shell that spawns
// a long-lived grandchild, cancel, and assert the grandchild dies too — that is
// the behaviour a plain CommandContext does not give you.
func TestSetProcGroupKillsGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("grandchild assertion uses a POSIX shell")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 60 & echo $!; wait")
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second

	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var grandchild int
	if _, err := fmt.Fscan(out, &grandchild); err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	if !processAlive(grandchild) {
		t.Fatalf("grandchild %d was not running at start", grandchild)
	}

	cancel()
	_ = cmd.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(grandchild) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived cancellation", grandchild)
}
