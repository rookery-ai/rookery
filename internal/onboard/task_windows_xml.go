package onboard

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// TaskName is what the logon task is called in Task Scheduler, and what
// `schtasks /TN` is given.
const TaskName = "Rookery"

// TaskXMLFor renders the Task Scheduler definition that starts Rookery when the
// owner signs in.
//
// It is generated rather than templated on disk for the same reason
// UnitFileFor is: the path to the binary is decided at install time, and a file
// shipped with a fixed path would register a task that starts nothing.
//
// Four settings here are load-bearing, and all four default the wrong way for a
// long-running server on a laptop — which is the machine this project documents
// as its common case:
//
//   - DisallowStartIfOnBatteries defaults to TRUE. Left alone, the task simply
//     does not start when the machine is on battery. On a laptop that is most
//     of the time, and nothing reports it: the server is just not running.
//   - StopIfGoingOnBatteries defaults to TRUE, which kills a running server the
//     moment the charger is unplugged.
//   - ExecutionTimeLimit defaults to 72 hours, after which Task Scheduler
//     terminates the task. A server that dies every three days with no error is
//     among the harder things to diagnose from the outside. PT0S is unlimited.
//   - MultipleInstancesPolicy decides what happens when the task is triggered
//     while it is already running. IgnoreNew keeps one server; the default
//     would start a second one that cannot bind the port.
//
// RestartOnFailure mirrors the systemd unit's Restart=on-failure. RunLevel is
// LeastPrivilege deliberately: this must not quietly become an elevated task.
func TaskXMLFor(binary, arguments, user, workDir string) string {
	var sb strings.Builder

	sb.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\r\n")
	sb.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\r\n")
	sb.WriteString("  <RegistrationInfo>\r\n")
	sb.WriteString("    <Description>Rookery control plane — agents, scheduler and web UI</Description>\r\n")
	sb.WriteString("    <URI>\\" + TaskName + "</URI>\r\n")
	sb.WriteString("  </RegistrationInfo>\r\n")

	sb.WriteString("  <Triggers>\r\n")
	sb.WriteString("    <LogonTrigger>\r\n")
	sb.WriteString("      <Enabled>true</Enabled>\r\n")
	if user != "" {
		sb.WriteString("      <UserId>" + xmlEscape(user) + "</UserId>\r\n")
	}
	sb.WriteString("    </LogonTrigger>\r\n")
	sb.WriteString("  </Triggers>\r\n")

	sb.WriteString("  <Principals>\r\n")
	sb.WriteString(`    <Principal id="Author">` + "\r\n")
	if user != "" {
		sb.WriteString("      <UserId>" + xmlEscape(user) + "</UserId>\r\n")
	}
	sb.WriteString("      <LogonType>InteractiveToken</LogonType>\r\n")
	sb.WriteString("      <RunLevel>LeastPrivilege</RunLevel>\r\n")
	sb.WriteString("    </Principal>\r\n")
	sb.WriteString("  </Principals>\r\n")

	sb.WriteString("  <Settings>\r\n")
	sb.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\r\n")
	sb.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\r\n")
	sb.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\r\n")
	sb.WriteString("    <AllowHardTerminate>true</AllowHardTerminate>\r\n")
	sb.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\r\n")
	sb.WriteString("    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>\r\n")
	sb.WriteString("    <IdleSettings>\r\n")
	sb.WriteString("      <StopOnIdleEnd>false</StopOnIdleEnd>\r\n")
	sb.WriteString("      <RestartOnIdle>false</RestartOnIdle>\r\n")
	sb.WriteString("    </IdleSettings>\r\n")
	sb.WriteString("    <AllowStartOnDemand>true</AllowStartOnDemand>\r\n")
	sb.WriteString("    <Enabled>true</Enabled>\r\n")
	sb.WriteString("    <Hidden>false</Hidden>\r\n")
	sb.WriteString("    <RunOnlyIfIdle>false</RunOnlyIfIdle>\r\n")
	sb.WriteString("    <WakeToRun>false</WakeToRun>\r\n")
	sb.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\r\n")
	sb.WriteString("    <Priority>7</Priority>\r\n")
	sb.WriteString("    <RestartOnFailure>\r\n")
	sb.WriteString("      <Interval>PT1M</Interval>\r\n")
	sb.WriteString("      <Count>3</Count>\r\n")
	sb.WriteString("    </RestartOnFailure>\r\n")
	sb.WriteString("  </Settings>\r\n")

	sb.WriteString(`  <Actions Context="Author">` + "\r\n")
	sb.WriteString("    <Exec>\r\n")
	sb.WriteString("      <Command>" + xmlEscape(binary) + "</Command>\r\n")
	if arguments != "" {
		sb.WriteString("      <Arguments>" + xmlEscape(arguments) + "</Arguments>\r\n")
	}
	if workDir != "" {
		sb.WriteString("      <WorkingDirectory>" + xmlEscape(workDir) + "</WorkingDirectory>\r\n")
	}
	sb.WriteString("    </Exec>\r\n")
	sb.WriteString("  </Actions>\r\n")
	sb.WriteString("</Task>\r\n")

	return sb.String()
}

// xmlEscape escapes the five XML metacharacters.
//
// Written out rather than reached for via encoding/xml because these values are
// filesystem paths and a Windows account name, and `&` is legal in both. A path
// under "C:\Users\R&D\..." would otherwise produce a document Task Scheduler
// rejects with "the task XML contains a value which is incorrectly formatted",
// an error that names nothing useful.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// TaskXMLBytes encodes the definition as UTF-16 little-endian with a byte-order
// mark, which is what `schtasks /Create /XML` expects.
//
// This is not a stylistic choice. The declaration says encoding="UTF-16", so
// writing UTF-8 bytes produces a file whose content contradicts its own header;
// schtasks rejects it with the same unhelpful "incorrectly formatted" message,
// and the natural conclusion is that something is wrong with the task rather
// than with the file encoding.
func TaskXMLBytes(xml string) []byte {
	encoded := utf16.Encode([]rune(xml))

	out := make([]byte, 0, 2+len(encoded)*2)
	out = append(out, 0xFF, 0xFE) // BOM, little-endian
	for _, u := range encoded {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

// ServeArguments builds the argument string for the task's action.
//
// The config path is passed only when there actually is a file, because the
// root --config flag defaults to a RELATIVE "config.yaml" and a scheduled task
// does not run in the directory the operator installed from. Naming the file
// absolutely is the only way the task reads the same configuration the operator
// just used; naming a file that does not exist would be worse than saying
// nothing.
func ServeArguments(configPath string) string {
	if configPath == "" {
		return "serve"
	}
	return fmt.Sprintf(`--config "%s" serve`, configPath)
}
