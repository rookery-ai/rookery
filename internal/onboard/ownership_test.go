package onboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeHost describes a machine this test host is not. Both the package manager
// probe and the command output are supplied, because the whole point of
// OwnerOf is behaviour on Fedora and Debian boxes we cannot run.
type fakeHost struct {
	have    map[string]bool
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (h *fakeHost) look(bin string) (string, error) {
	if h.have[bin] {
		return "/usr/bin/" + bin, nil
	}
	return "", errors.New("not found")
}

func (h *fakeHost) run(_ context.Context, name string, args ...string) ([]byte, error) {
	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	if err, ok := h.errs[name]; ok {
		return nil, err
	}
	return []byte(h.outputs[name]), nil
}

func TestOwnerOfDetectsAnRPMInstall(t *testing.T) {
	h := &fakeHost{
		have:    map[string]bool{"rpm": true},
		outputs: map[string]string{"rpm": "rookery-0.1.4-1.x86_64\n"},
	}
	got := OwnerOf(context.Background(), h.run, h.look, "/usr/bin/rookery")
	if !got.Managed {
		t.Fatal("an rpm-owned binary must be reported as managed")
	}
	if got.Package != "rookery" {
		// The raw answer is name-version-release.arch, which dnf will not
		// accept as an argument. A message that hands the user a command that
		// fails is worse than no message.
		t.Errorf("Package = %q, want the bare name %q", got.Package, "rookery")
	}
	if got.RemoveCommand != "sudo dnf remove rookery" {
		t.Errorf("RemoveCommand = %q", got.RemoveCommand)
	}
}

func TestOwnerOfDetectsADebInstall(t *testing.T) {
	h := &fakeHost{
		have:    map[string]bool{"dpkg": true},
		outputs: map[string]string{"dpkg": "rookery: /usr/bin/rookery\n"},
	}
	got := OwnerOf(context.Background(), h.run, h.look, "/usr/bin/rookery")
	if !got.Managed || got.Package != "rookery" {
		t.Fatalf("dpkg ownership not detected: %+v", got)
	}
	if got.RemoveCommand != "sudo apt remove rookery" {
		t.Errorf("RemoveCommand = %q", got.RemoveCommand)
	}
}

// An install.sh or archive user has a binary in ~/.local/bin that no package
// manager knows about. rpm exits non-zero for it.
func TestOwnerOfReportsUnmanagedForAnUnownedBinary(t *testing.T) {
	h := &fakeHost{
		have: map[string]bool{"rpm": true},
		errs: map[string]error{"rpm": errors.New("exit status 1")},
	}
	if got := OwnerOf(context.Background(), h.run, h.look, "/home/u/.local/bin/rookery"); got.Managed {
		t.Fatalf("an unowned binary must not be reported as managed: %+v", got)
	}
}

// A Fedora box has no dpkg and must never be asked about it — asking would
// produce a confusing error in the uninstall output at best.
func TestOwnerOfOnlyAsksManagersThatExist(t *testing.T) {
	h := &fakeHost{
		have:    map[string]bool{"rpm": true},
		outputs: map[string]string{"rpm": "rookery-0.1.4-1.x86_64\n"},
	}
	OwnerOf(context.Background(), h.run, h.look, "/usr/bin/rookery")
	for _, c := range h.calls {
		if strings.HasPrefix(c, "dpkg") {
			t.Errorf("dpkg was probed on a host without it: %v", h.calls)
		}
	}
}

// A host with neither manager — a container, macOS — is unmanaged, and the
// probe must not error out.
func TestOwnerOfHandlesAHostWithNoPackageManager(t *testing.T) {
	h := &fakeHost{have: map[string]bool{}}
	if got := OwnerOf(context.Background(), h.run, h.look, "/usr/local/bin/rookery"); got.Managed {
		t.Fatalf("want unmanaged, got %+v", got)
	}
	if len(h.calls) != 0 {
		t.Errorf("no manager present, but commands ran: %v", h.calls)
	}
}

// The failure policy, pinned because it is the opposite of what feels safe.
//
// An inconclusive probe must report NOT managed, so uninstall proceeds. The
// alternative — assuming managed when unsure — would make uninstall impossible
// for archive and install.sh users, who are the majority and the only ones with
// no package manager to fall back on, and would tell them to run a command
// their system does not have.
func TestOwnerOfFailsOpenWhenTheProbeItselfBreaks(t *testing.T) {
	h := &fakeHost{
		have: map[string]bool{"rpm": true, "dpkg": true},
		errs: map[string]error{
			"rpm":  errors.New("rpmdb: BDB0113 Thread died in Berkeley DB library"),
			"dpkg": errors.New("exit status 2"),
		},
	}
	if got := OwnerOf(context.Background(), h.run, h.look, "/usr/bin/rookery"); got.Managed {
		t.Fatalf("a broken probe must not block uninstall: %+v", got)
	}
}
