package agentrunner

import "testing"

// TestSilentMarkerToleratesModelFormatting is the reliability fix for the one
// thing an agent must get right when it has nothing to say: staying quiet.
//
// The marker used to be matched with `trimmed == "[SILENT]"` — an exact line
// compare — so every ordinary way a model decorates a token missed it:
// **[SILENT]**, `[SILENT]`, [silent], [SILENT]. and a bare SILENT. A missed
// marker is not a no-op. The runner treats "no [CHAT] and not silent" as a
// broken run and sends "⚠️ Ran but produced no notification", so a correctly
// behaving agent that had nothing to report notifies the user anyway — twice a
// day, forever. That is the opposite of what the agent was built to do, and it
// trains the user to ignore the channel.
//
// The [CHAT] parser was already hardened against a stray [/CHAT] for exactly
// this class of drift; [SILENT] was not.
func TestSilentMarkerToleratesModelFormatting(t *testing.T) {
	for _, raw := range []string{
		"[SILENT]",
		"[SILENT]\n",
		"  [SILENT]  ",
		"**[SILENT]**",
		"*[SILENT]*",
		"`[SILENT]`",
		"[silent]",
		"[Silent]",
		"[SILENT].",
		"[SILENT]!",
		"[/SILENT]",
		"SILENT",
		"silent",
		"\"[SILENT]\"",
		"[STATE]{\"seen\": []}[/STATE]\n**[SILENT]**\n",
		"[STATE]{\"seen\": []}[/STATE]\n\n  `[silent]`  \n",
	} {
		out := parseCoderOutput(raw)
		if !out.silent {
			t.Errorf("parseCoderOutput(%q).silent = false, want true — this sends a spurious warning to the user", raw)
		}
		if len(out.chatLines) != 0 {
			t.Errorf("parseCoderOutput(%q) produced chat lines %q — a silent run must deliver nothing", raw, out.chatLines)
		}
	}
}

// TestSilentMarkerDoesNotSwallowRealMessages is the other half, and the reason
// the match stays strict about CONTEXT even while it is lenient about
// decoration: a line that merely mentions the marker inside a sentence is
// prose, not a signal. Treating it as silent would drop a real notification the
// user was waiting for — a worse failure than the spurious warning above,
// because nothing at all arrives and nothing says so.
func TestSilentMarkerDoesNotSwallowRealMessages(t *testing.T) {
	for _, raw := range []string{
		"[CHAT] I stayed [SILENT] last night because nothing changed.",
		"[CHAT] Nothing new. Next time I will emit [SILENT] instead.",
		"[CHAT] The silent treatment continues.",
		"[CHAT] silent mode is on",
	} {
		out := parseCoderOutput(raw)
		if out.silent {
			t.Errorf("parseCoderOutput(%q).silent = true, want false — a real message was suppressed", raw)
		}
		if len(out.chatLines) == 0 {
			t.Errorf("parseCoderOutput(%q) delivered nothing, want the message", raw)
		}
	}
}
