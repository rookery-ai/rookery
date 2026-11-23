package backup

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// lockHelperEnv turns the test binary into a lock holder: it takes the install
// lock for the named directory, says so on stdout, and holds it until stdin
// closes or the process is killed. That lets a test kill a real holder the way
// a reboot or a closed console window does — with no deferred code running.
const lockHelperEnv = "ROOKERY_LOCK_HELPER_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(lockHelperEnv); dir != "" {
		lock, err := AcquireLock(dir)
		if err != nil {
			fmt.Printf("refused: %v\n", err)
			os.Exit(3)
		}
		fmt.Println("locked")
		// Block until the parent closes stdin or kills us.
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		_ = lock.Release()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLockRefusesSecondHolder(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := AcquireLock(dir); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("got %v, want ErrServerRunning", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("lock must be reusable after release: %v", err)
	}
	second.Release()
}

// The reported Windows failure: a server killed without running its deferred
// Release left its lock behind, and every later start refused. A lock the OS
// drops together with its holder cannot do that.
func TestLockFreedWhenHolderIsKilled(t *testing.T) {
	dir := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lineCh <- strings.TrimSpace(line)
	}()
	select {
	case line := <-lineCh:
		if line != "locked" {
			t.Fatalf("helper did not take the lock: %q", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("helper never reported taking the lock")
	}

	if _, err := AcquireLock(dir); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("while the holder is alive: got %v, want ErrServerRunning", err)
	}

	// A hard kill: TerminateProcess on Windows, SIGKILL elsewhere. No deferred
	// code in the holder runs, exactly as on a reboot or a closed window.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_ = cmd.Wait()

	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("after the holder was killed the lock must be free: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// Every install already wedged by the old Windows lock has a rookery.pid that
// nobody holds. Upgrading must heal it rather than keep refusing.
func TestLockAcquiresOverLeftoverFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(LockPath(dir), []byte("99999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("a leftover lock file nobody holds must not block: %v", err)
	}
	defer lock.Release()
	pid, err := ReadLockPID(dir)
	if err != nil {
		t.Fatalf("ReadLockPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("lock file records pid %d, want %d", pid, os.Getpid())
	}
}

// The pid is there for a human to read during triage, so the lock must not
// cover the bytes that hold it — on Windows a locked range refuses reads.
func TestLockFileReadableWhileHeld(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	raw, err := os.ReadFile(LockPath(dir))
	if err != nil {
		t.Fatalf("lock file must stay readable while held: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != fmt.Sprint(os.Getpid()) {
		t.Fatalf("lock file holds %q, want our pid", got)
	}
}

// The lock is the OS lock, not the file, so releasing it leaves the file in
// place — the same as the Unix flock implementation.
func TestLockReleaseKeepsFile(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LockPath(dir)); err != nil {
		t.Fatalf("Release must not remove the lock file: %v", err)
	}
}
