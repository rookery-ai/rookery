package onboard

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

// parseTaskXML parses the generated document.
//
// A CharsetReader is required because the declaration says UTF-16 — which
// describes the BYTES schtasks will read, produced by TaskXMLBytes — while the
// Go string itself is ordinary UTF-8 runes. Go's decoder refuses a declared
// encoding it does not know rather than guessing, so this passes the input
// through unchanged.
func parseTaskXML(t *testing.T, doc string) error {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	d.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	var into any
	return d.Decode(&into)
}

// The generated XML is the tested artifact here, exactly as the systemd unit
// is. There is no Windows host in this project and none in CI, so this is the
// level at which the task can be checked at all.
func TestTaskXMLIsWellFormed(t *testing.T) {
	got := TaskXMLFor(`C:\Program Files\Rookery\rookery.exe`, "serve", `MACHINE\owner`, `C:\Program Files\Rookery`)

	if err := parseTaskXML(t, got); err != nil {
		t.Fatalf("generated task XML does not parse: %v", err)
	}
}

// Four Task Scheduler defaults are wrong for a long-running server, and every
// one of them fails silently — the server is simply not running, with nothing
// logged and nothing to search for.
//
// This is the test that matters most in the file: the XML can be perfectly
// well-formed and still produce a server that stops the moment the laptop is
// unplugged, or dies after three days.
func TestTaskXMLOverridesTheDefaultsThatSilentlyStopAServer(t *testing.T) {
	got := TaskXMLFor(`C:\rookery.exe`, "serve", "owner", "")

	for _, want := range []struct{ element, reason string }{
		{"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
			"defaults true: on a laptop the task would usually not start at all"},
		{"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
			"defaults true: unplugging the charger would kill the server"},
		{"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
			"defaults to 72 hours, after which the server is terminated with no error"},
		{"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
			"a second instance cannot bind the port"},
	} {
		if !strings.Contains(got, want.element) {
			t.Errorf("missing %s — %s", want.element, want.reason)
		}
	}
}

// The whole reason a logon task was chosen over an SCM service is that it runs
// as the signed-in user with no stored credential and no elevation. A task that
// quietly asked for either would defeat the choice.
func TestTaskRunsAsTheUserWithoutElevationOrStoredCredentials(t *testing.T) {
	got := TaskXMLFor(`C:\rookery.exe`, "serve", `MACHINE\owner`, "")

	if !strings.Contains(got, "<LogonType>InteractiveToken</LogonType>") {
		t.Error("the task must use the interactive token — anything else needs a stored password")
	}
	if !strings.Contains(got, "<RunLevel>LeastPrivilege</RunLevel>") {
		t.Error("the task must not run elevated")
	}
	if !strings.Contains(got, "<LogonTrigger>") {
		t.Error("the task must be triggered at logon")
	}
	if strings.Contains(got, "S4U") {
		t.Error("S4U needs a batch-logon right a standard user may not hold")
	}
}

// A Windows account name or an install path may legally contain an ampersand.
// Task Scheduler rejects the resulting document with a message that names
// nothing useful, so it would read as a bug in the task rather than in the file.
func TestTaskXMLEscapesPathsAndAccountNames(t *testing.T) {
	got := TaskXMLFor(`C:\Users\R&D\rookery.exe`, "serve", `MACHINE\R&D`, "")

	if strings.Contains(got, "R&D") {
		t.Error("an ampersand reached the document unescaped")
	}
	if !strings.Contains(got, "R&amp;D") {
		t.Error("the ampersand was not escaped")
	}
	if err := parseTaskXML(t, got); err != nil {
		t.Fatalf("XML with an escaped ampersand does not parse: %v", err)
	}
}

// The declaration says UTF-16, so the bytes have to be UTF-16. Writing UTF-8
// under a UTF-16 header produces a file schtasks refuses with the same opaque
// "incorrectly formatted" error as a genuinely malformed task.
func TestTaskXMLBytesAreUTF16WithABOM(t *testing.T) {
	src := TaskXMLFor(`C:\rookery.exe`, "serve", "owner", "")
	got := TaskXMLBytes(src)

	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xFE {
		t.Fatal("missing little-endian byte-order mark")
	}
	if !strings.Contains(src, `encoding="UTF-16"`) {
		t.Fatal("the declaration must agree with the encoding actually written")
	}

	units := make([]uint16, 0, (len(got)-2)/2)
	for i := 2; i+1 < len(got); i += 2 {
		units = append(units, uint16(got[i])|uint16(got[i+1])<<8)
	}
	if decoded := string(utf16.Decode(units)); decoded != src {
		t.Error("the encoded bytes do not decode back to the document")
	}
}

// A scheduled task does not run in the directory the operator installed from,
// so a relative "config.yaml" would silently select different configuration —
// or none. Naming a file that does not exist would be worse than saying
// nothing, which is why the flag is conditional.
func TestServeArgumentsNamesTheConfigOnlyWhenThereIsOne(t *testing.T) {
	if got := ServeArguments(""); got != "serve" {
		t.Errorf("with no config file the task should just serve, got %q", got)
	}
	got := ServeArguments(`C:\Users\o\.rookery\config.yaml`)
	if !strings.Contains(got, `--config "C:\Users\o\.rookery\config.yaml"`) {
		t.Errorf("the config path must be passed absolutely and quoted, got %q", got)
	}
	if !strings.HasSuffix(got, "serve") {
		t.Errorf("the subcommand must come last, got %q", got)
	}
}
