package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// Autostart on Windows is a Task Scheduler task triggered at logon.
//
// Not a Service Control Manager service: installing one needs administrator
// rights and it then runs as a different principal, which cannot reach a data
// directory under the signing-in user's own profile. That is the same reason
// the Linux side uses a systemd USER unit rather than a system one.
//
// None of this is exercised on a real host — there is no Windows machine in
// this project and none in CI. What checks it is the cross-compile gate and the
// unit tests over the generated XML, which is the artifact all the platform
// knowledge lives in. The same position this repository already records for
// swap_windows.go and echo_windows.go.

func installAutostart(env serviceEnv, out io.Writer) error {
	xml := onboard.TaskXMLFor(
		env.binary,
		onboard.ServeArguments(env.configPath),
		currentWindowsUser(),
		filepath.Dir(env.binary),
	)

	// schtasks reads the definition from a file, so one is written next to the
	// user's temp directory and removed afterwards. It is UTF-16 because the
	// document declares UTF-16; the two disagreeing is rejected with an error
	// that names nothing useful.
	f, err := os.CreateTemp("", "rookery-task-*.xml")
	if err != nil {
		return fmt.Errorf("create task definition: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(onboard.TaskXMLBytes(xml)); err != nil {
		f.Close()
		return fmt.Errorf("write task definition: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write task definition: %w", err)
	}

	// /F replaces an existing task rather than failing, which makes re-running
	// setup and `rookery upgrade` idempotent instead of an error the operator
	// has to interpret.
	if err := runArgs([]string{"schtasks", "/Create", "/TN", onboard.TaskName, "/XML", f.Name(), "/F"}); err != nil {
		return fmt.Errorf("register the logon task: %w", err)
	}
	fmt.Fprintf(out, "registered the %q logon task\n", onboard.TaskName)

	// A logon trigger fires at the NEXT sign-in, so without this the operator
	// finishes setup and finds nothing running, having just been told Rookery
	// would start automatically.
	if err := runArgs([]string{"schtasks", "/Run", "/TN", onboard.TaskName}); err != nil {
		fmt.Fprintf(out, "registered, but could not start it now: %v\n", err)
		fmt.Fprintf(out, "  it will start when you next sign in, or run: %s\n", onboard.CurrentService().Foreground)
		return nil
	}
	fmt.Fprintln(out, "started")
	return nil
}

func uninstallAutostart(out io.Writer) error {
	// Stopping first is best-effort: a task that is not running must not stop
	// the registration being removed.
	_ = runArgs([]string{"schtasks", "/End", "/TN", onboard.TaskName})

	if err := runArgs([]string{"schtasks", "/Delete", "/TN", onboard.TaskName, "/F"}); err != nil {
		return fmt.Errorf("remove the logon task: %w", err)
	}
	fmt.Fprintln(out, "Rookery will no longer start automatically.")
	fmt.Fprintln(out, "Your data directory is untouched.")
	return nil
}

func autostartStatus() (bool, string, error) {
	out, err := exec.Command("schtasks", "/Query", "/TN", onboard.TaskName).CombinedOutput()
	if err != nil {
		// schtasks exits non-zero when the task does not exist, which is an
		// answer rather than a failure.
		return false, "", nil
	}
	return true, strings.TrimSpace(string(out)), nil
}

// currentWindowsUser returns DOMAIN\user for the task's principal, or "" to let
// Task Scheduler default to whoever registers it.
//
// Returning "" on an incomplete environment is deliberate: an empty <UserId>
// element would be rejected outright, whereas omitting it leaves a task that
// still registers correctly for the current user.
func currentWindowsUser() string {
	user := os.Getenv("USERNAME")
	if user == "" {
		return ""
	}
	if domain := os.Getenv("USERDOMAIN"); domain != "" {
		return domain + `\` + user
	}
	return user
}
