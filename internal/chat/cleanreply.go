package chat

import (
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/db"
)

// ── Protocol markers in a conversational reply ───────────────────────────────
//
// The agent output protocol ([CHAT], [STATE], [CALL:], [SILENT]) belongs to an
// agent RUN, where agentrunner.parseCoderOutput consumes the markers as
// structure. Chat has no such parser: handleChatMessage and the chat-platform
// handler sent the model's text straight through, so whenever a model reached
// for the protocol the owner read the markers verbatim.
//
// Removing the instruction (prompts.platformContextBlock no longer describes the
// protocol on the chat surface) is the fix; this is the guarantee. A prompt
// steers and does not bind, models differ in how strictly they follow a system
// instruction, and the same model complies on one turn and not the next as the
// conversation grows. Measured on a live install, 30 of 192 assistant rows had
// leaked at least one marker.
//
// EVERY RULE HERE IS LINE-ANCHORED, and that is the whole design. A marker that
// OPENS a line is protocol; a marker inside a sentence, or in backticks, is the
// model explaining itself — and both shapes occur in real transcripts:
//
//	leak  →  \n\n[STATE]{"last_email_search": {…}}[/STATE]
//	docs  →  - **`[STATE]{"key": "value"}[/STATE]`** — saves data between runs
//
// A substring replace cannot tell those apart. That was the first implementation,
// and on a real reply enumerating the four markers it emptied the code span in
// the second bullet above, leaving a bullet whose subject had vanished while the
// description stayed. Do not "simplify" this back to strings.ReplaceAll.
//
// Deliberately NOT shared with prompts.StripProtocolMarkers, which serves the KB
// assist endpoint. That one rewrites a passage the owner is about to paste over
// their own writing, so content between markers IS the answer and it keeps a
// [STATE] body (its own test pins this). Here the input is a conversation and a
// leaked state block is machine memory the owner must never see, so the block
// goes whole. Same tokens, opposite policy, because the inputs differ — merging
// them is the obvious future cleanup and would reintroduce one bug or the other.
var (
	// A [STATE]…[/STATE] block that OPENS a line, body included. Non-greedy so
	// two blocks in one reply do not collapse into one match.
	stateBlockRE = regexp.MustCompile(`(?ms)^[ \t]*\[STATE\].*?\[/STATE\][ \t]*$`)
	// An opener with no closer — a reply cut off mid-block still must not show
	// raw JSON.
	stateOrphanRE = regexp.MustCompile(`(?m)^[ \t]*\[/?STATE\].*$`)
	// [CHAT] / [/CHAT] opening a line. The rest of the line is content and is
	// kept: the live data has both "[CHAT]\nHere are…" and "[CHAT] Here are…".
	chatMarkerRE = regexp.MustCompile(`(?i)^[ \t]*\[/?CHAT\][ \t]*`)
	// A line that is ONLY [SILENT]. Lenient about decoration for the reason
	// agentrunner.isSilentMarker records: models write **[SILENT]**, [silent]
	// and [SILENT]. — and a missed marker here is a visible leak, while a
	// false match costs only a dropped line that said nothing else anyway.
	silentLineRE = regexp.MustCompile(`(?i)^[ \t]*[*_` + "`" + `]*\[?[ \t]*silent[ \t]*\]?[*_` + "`" + `]*[.!]?[ \t]*$`)
	// A [CALL: agent] line — chat cannot invoke agents, so this is pure leakage.
	callLineRE = regexp.MustCompile(`(?i)^[ \t]*\[CALL:`)
)

// markerOnlyPlaceholder is what the owner sees when a reply was nothing but
// protocol markers.
//
// Ten of the live install's assistant rows are exactly "[SILENT]" — the model
// complying with "don't say anything" using the one marker the prompt had given
// it. Neither obvious fallback works: showing the raw text re-displays the leak
// this file exists to remove, and rendering nothing leaves an empty bubble,
// which reads as being ignored and gives the owner no way forward (the lesson
// agentdesigner.UserFacingDesignText already had to learn). So the reply is
// replaced with a short, honest line that does not put words in the assistant's
// mouth about WHY it said nothing.
const markerOnlyPlaceholder = "_(no reply)_"

// emptyReplyPlaceholder stands in for a coder call that SUCCEEDED and returned
// no text whatsoever — a different event from the one above, where the model
// spoke but only in protocol markers.
//
// This case used to return "", which handleChatMessage persisted unguarded, so
// the owner got a blank bubble. Four such rows exist on the reporting install,
// one of them the answer to a question about a 155 KB table the model could not
// read within its tool-result cap. #242 fixed only the marker-only case.
//
// The wording differs from markerOnlyPlaceholder because the remedies differ:
// nothing came back at all here, so retrying is the useful next step, whereas a
// marker-only reply was a deliberate (if malformed) decision to say nothing. It
// also matters that this is not silently empty in the stored transcript: a
// blank assistant turn is few-shot evidence to the NEXT turn that answering
// with nothing is acceptable here.
const emptyReplyPlaceholder = "_(no reply — the model returned nothing. Try asking again.)_"

// CleanReply turns one raw coder reply into the text a human reads.
//
// Safe to call on text that contains no markers at all: ordinary prose comes
// back byte-identical, which is the property the tests guard most closely — a
// cleaner that rewrites innocent replies would be worse than the leak.
func CleanReply(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return emptyReplyPlaceholder
	}
	cleaned := cleanMarkers(raw)
	if cleaned != "" {
		return cleaned
	}
	// Nothing survived, so the reply WAS the markers — a distinct event from the
	// empty input handled above, and given its own placeholder for that reason.
	return markerOnlyPlaceholder
}

// cleanMarkers applies the line-anchored rules and returns the surviving prose,
// which may be empty. Split out from CleanReply so the placeholder decision has
// exactly one home.
func cleanMarkers(raw string) string {
	s := stateBlockRE.ReplaceAllString(raw, "")
	s = stateOrphanRE.ReplaceAllString(s, "")

	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if silentLineRE.MatchString(line) || callLineRE.MatchString(line) {
			continue
		}
		kept = append(kept, chatMarkerRE.ReplaceAllString(line, ""))
	}
	out := strings.Join(kept, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

// CleanHistory returns a copy of a conversation with the protocol markers taken
// out of the ASSISTANT turns.
//
// This is not cosmetic — the history is fed back to the model as prior turns, so
// every previously-leaked reply is few-shot evidence that markers are how one
// answers here. A live transcript shows the escalation plainly: clean early,
// then wrapped on nearly every turn after the first leak. Without this, existing
// conversations would keep re-teaching themselves after the prompt is fixed.
//
// User turns are left exactly as typed. The owner's words are not ours to
// rewrite, and someone who pastes a marker to ask what it means must still be
// quoting it on the next turn.
//
// A turn that cleans to nothing keeps its raw text rather than becoming the
// placeholder: this output is for the MODEL, and the placeholder is a message
// for a human.
func CleanHistory(history []db.ChatMessage) []db.ChatMessage {
	out := make([]db.ChatMessage, len(history))
	copy(out, history)
	for i := range out {
		if out[i].Role != "assistant" {
			continue
		}
		if cleaned := cleanMarkers(out[i].Content); cleaned != "" {
			out[i].Content = cleaned
		}
	}
	return out
}
