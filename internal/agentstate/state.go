// Package agentstate owns the on-disk format of an agent's state.md — the
// markdown document that holds an agent's memory between runs.
//
// It exists because there are several doors onto that file (the runner's
// [STATE] marker, an agent editing the file with its own tools) and they had
// drifted: the reader only ever looked inside the ```json fence, while an agent
// writing the file wholesale would put its JSON somewhere else entirely. The
// reader then saw {}, the agent re-baselined on every run and went silent
// forever. Every door now lands on Apply, so they cannot disagree again.
package agentstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxStateSize bounds the serialized state object. Kept in sync with (and
// intended to replace) internal/agentrunner's maxStateSize — one limit, not two.
const MaxStateSize = 64 * 1024

// StateFilePath returns the path to an agent's state.md — its memory between
// runs, kept as a markdown document so it is readable in the knowledge base.
func StateFilePath(agentDir string) string {
	return filepath.Join(agentDir, "state.md")
}

// RenderTemplate builds a fresh state.md. The intro is italic prose, never
// an HTML comment: comments do not round-trip through the KB editor and would
// pin the file in raw mode forever. Two details matter for that round-trip:
// asterisk emphasis (*..*), not underscore (tiptap-markdown always re-emits
// emphasis with asterisks, so underscore delimiters would flip on save and
// register as lossy), and the intro stays on one physical source line (a
// soft line break within the paragraph collapses to a single space on
// serialize, which would likewise register as lossy).
func RenderTemplate(agentName, jsonBody string) string {
	return fmt.Sprintf(`# State — %s

*Managed by Rookery. The block below is this agent's memory between runs — edit it if you need to fix something by hand.*

`+"```json\n%s\n```"+`
`, agentName, jsonBody)
}

// headingPrefix and introPrefix are the two lines RenderTemplate emits above
// the fence. They are recognised (and dropped) when preserved prose is carried
// into a re-render, so normalising a file that already had them does not end up
// with two headings.
const (
	headingPrefix = "# State"
	introPrefix   = "*Managed by Rookery"
)

// fenceLoc describes where (if anywhere) the state fence lives.
type fenceLoc struct {
	Open, Close int // line indices of the ```json and ``` lines; valid only when OK
	OK          bool
	OrphanOpen  int // index of the first ```json line when OK is false; -1 when there is none
}

// findStateFence locates the FIRST well-formed json fence: an opener line
// (trimmed == "```json") terminated by a closer line (trimmed == "```") with no
// other fence-opener line in between.
//
// If the first opener is not cleanly terminated — because the file ends, or
// because another fence opener appears first — the file is damaged and we report
// OK=false rather than searching on. The state fence is by construction the FIRST
// fence; if it is malformed, nothing later in the document may be mistaken for it.
func findStateFence(lines []string) fenceLoc {
	openIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "```json" {
			openIdx = i
			break
		}
	}
	if openIdx == -1 {
		return fenceLoc{OK: false, OrphanOpen: -1}
	}
	for i := openIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "```" {
			// OrphanOpen: -1 explicitly. Its zero value is a VALID line index, and
			// read() asks `loc.OrphanOpen < 0` to tell "no fence was written" from
			// "a fence was written and damaged". Leaving it zero here made a
			// well-formed but EMPTY fence — the shape saveState writes after any
			// run that emits no [STATE], i.e. the commonest file on the install —
			// report understood=false, which disables the runner's self-heal and
			// makes the state.json migration's verify-read fail forever.
			return fenceLoc{Open: openIdx, Close: i, OK: true, OrphanOpen: -1}
		}
		if strings.HasPrefix(trimmed, "```") {
			// Another fence opener before this one closed: damaged.
			return fenceLoc{OK: false, OrphanOpen: openIdx}
		}
	}
	// Ran off the end without a closer: damaged.
	return fenceLoc{OK: false, OrphanOpen: openIdx}
}

// Get reads an agent's state, recovering where the file is malformed.
//
// The second return is `understood` — whether we made sense of the file. It is
// deliberately distinct from "the state is empty", which is a legitimate
// outcome for a fresh agent. Callers use it to decide whether a no-update turn
// may safely write back; overwriting a file we could not parse would destroy
// hand-recoverable state.
func Get(path string) (map[string]any, bool, error) {
	r, err := read(path)
	if err != nil {
		return nil, false, err
	}
	return r.state, r.understood, nil
}

// GetDetail is Get plus one more fact: whether the state had to be RECOVERED
// from outside the json fence.
//
// The runner needs that distinction and cannot infer it. A file whose state was
// recovered is damaged by construction — the fence was empty or gone and the
// data was found loose in the document — so if the run STARTED with state, the
// run-start snapshot is the better truth. Without this, an agent that rewrites
// state.md mid-run and happens to leave a JSON snippet in its prose (a quoted
// API error, say) has that snippet adopted as its entire memory, and the real
// state is destroyed: the exact "stored something, next run sees nothing"
// failure this package exists to remove.
func GetDetail(path string) (state map[string]any, understood, recovered bool, err error) {
	r, err := read(path)
	if err != nil {
		return nil, false, false, err
	}
	return r.state, r.understood, r.recovered, nil
}

// readResult is Get's full answer. Apply needs two facts Get does not expose:
// whether the state had to be RECOVERED from outside the fence (in which case
// the file is re-rendered rather than spliced) and, if so, which prose to carry
// over into the re-rendered document.
type readResult struct {
	state      map[string]any
	understood bool
	recovered  bool
	keep       string // prose to preserve after the fence, recovered files only
}

func read(path string) (readResult, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return readResult{state: map[string]any{}, understood: true}, nil
	}
	if err != nil {
		return readResult{}, err
	}
	lines := strings.Split(string(raw), "\n")
	loc := findStateFence(lines)

	if loc.OK {
		body := strings.TrimSpace(strings.Join(lines[loc.Open+1:loc.Close], "\n"))
		if body != "" {
			st, err := decode(body)
			if err != nil {
				// A well-formed fence whose body is not JSON: we do NOT guess
				// past it. The fence is the declared location; a broken one is
				// a human's problem, not a cue to go hunting.
				return readResult{state: map[string]any{}, understood: false}, nil
			}
			if len(st) > 0 {
				return readResult{state: st, understood: true}, nil
			}
		}
	}

	// Fence empty or absent. THIS is the hn-watch case: an agent wrote the file
	// itself and put its memory somewhere the reader never looked. Scan for the
	// first parseable JSON object and adopt it.
	//
	// Narrow on purpose. Only reached when the fence has nothing to offer, and
	// only the FIRST object is taken. The residual risk is an agent with truly
	// empty state and a JSON example in its prose, which would adopt the
	// example — unlikely, self-correcting on the next patch, and far better
	// than a correct agent going silent forever.
	skipStart, skipEnd := fenceByteRange(lines, loc)
	st, objStart, objEnd, ok := scanFirstJSONObject(raw, skipStart, skipEnd)
	if !ok {
		// Nothing to adopt. Whether that means "fresh agent" or "damaged file"
		// depends on WHY there was no usable fence, and the two must not look
		// alike: the caller's no-update turn is a no-op on a file it could not
		// understand, so calling a damaged file understood would let that turn
		// overwrite hand-recoverable state with {}.
		//
		// An orphaned (unterminated) opener is damage by construction — someone
		// wrote a fence and it did not survive — so an orphan we could not
		// recover anything from reports understood=false. A file with NO fence
		// at all, or a well-formed but empty one, is the ordinary shape of an
		// agent that has not stored anything yet.
		return readResult{state: map[string]any{}, understood: loc.OrphanOpen < 0}, nil
	}
	return readResult{
		state:      st,
		understood: true,
		recovered:  true,
		keep:       preservedProse(string(raw), skipStart, skipEnd, objStart, objEnd),
	}, nil
}

// Apply merges patch into the file's current state and writes it back. It is
// the single writer: the [STATE] marker, the API engine's set_state and the CLI
// bridge all land here, so the three doors cannot drift apart.
//
// A nil or empty patch still writes, which is what normalises a recovered file.
//
// An explicit patch WINS, even over a file we could not parse — it replaces the
// unreadable body outright. That is deliberate and long-standing: the runner's
// applyAndSaveState has always held that "an explicit [STATE] emission always
// wins, even over an unreadable prior file", and live agents emit patches
// constantly, so this is the ordinary path rather than an edge case.
//
// The protection is narrower than it first reads, and worth stating exactly: it
// is only a NO-OP Apply (nil or empty patch) that declines to touch a file it
// could not understand. That is the case where writing back would replace
// hand-recoverable bytes with {} while nobody had asked for a change.
func Apply(path, agentName string, patch map[string]any) (map[string]any, error) {
	r, err := read(path)
	if err != nil {
		return nil, err
	}
	if !r.understood && len(patch) == 0 {
		return r.state, nil
	}
	Merge(r.state, patch)

	body, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(body) > MaxStateSize {
		return nil, fmt.Errorf("state too large (%d bytes > %d limit)", len(body), MaxStateSize)
	}
	return r.state, writeFence(path, agentName, string(body), r.keep, r.recovered)
}

// Replace sets the file's state to exactly `state`, discarding what was there.
//
// This is the whole-state write the agent designer has always had (WriteState),
// kept sharply distinct from Apply's patch semantics: Replace does not read the
// existing state at all, so a key absent from `state` is a key deleted. It does
// not run the recovery scan either — the caller is asserting the state, not
// asking what the file currently believes.
//
// The caller owns the size cap. Replace deliberately does not enforce
// MaxStateSize: the one-shot state.json migration must be able to carry a
// legacy state larger than the limit, and refusing it would strand that agent
// with no state.md at all — which reads as {} and re-baselines on every run,
// the exact failure this package exists to remove. Every agent-facing write
// path (Apply, and the runner's saveState) does enforce it.
func Replace(path, agentName string, state map[string]any) (map[string]any, error) {
	if state == nil {
		state = map[string]any{}
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return state, writeFence(path, agentName, string(body), "", false)
}

// DecodeJSON unmarshals with json.Number, and every path that decodes an agent's
// state MUST use it rather than json.Unmarshal.
//
// Plain Unmarshal decodes every JSON number as float64, which silently rounds any
// integer above 2^53. The commonest thing an agent stores is an id — a 64-bit
// Discord snowflake, a Hacker News item id — so the corruption lands squarely on
// the data this file exists to keep: 1400000000000000001 comes back as
// 1400000000000000000, and the agent then re-reports an item it had already seen,
// forever. json.Number round-trips the original literal digits.
//
// It is exported because the decode sites are in three packages — the API
// engine's tool args, the bridge's request body, and the CLI subcommand's --patch
// flag — and three private copies is exactly how one of them ends up plain again.
func DecodeJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// Merge applies a patch in place. A nil value deletes the key — the semantic
// [STATE] has always had, now the rule for every door.
func Merge(existing, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(existing, k)
		} else {
			existing[k] = v
		}
	}
}

// decode parses a state object. UseNumber preserves integer fidelity: plain
// json.Unmarshal decodes every JSON number as float64, silently rounding any
// integer above 2^53 (e.g. a 64-bit Discord snowflake ID — the single most
// common thing an agent stashes in state). json.Number round-trips through
// MarshalIndent as the original literal digits, so state.md ends up
// byte-identical either way.
func decode(body string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var st map[string]any
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("state.md json block: %w", err)
	}
	if st == nil {
		st = map[string]any{}
	}
	return st, nil
}

// scanFirstJSONObject finds the first byte offset holding a complete JSON
// object that decodes to a NON-EMPTY map, and returns it with its byte span.
//
// [skipStart, skipEnd) is the fence's own region and is stepped over: this
// function is only ever called when the fence had nothing to offer, so the `{}`
// sitting inside an empty fence is exactly the answer we must not "recover".
func scanFirstJSONObject(raw []byte, skipStart, skipEnd int) (map[string]any, int, int, bool) {
	for i := 0; i < len(raw); i++ {
		if skipStart < skipEnd && i >= skipStart && i < skipEnd {
			i = skipEnd - 1 // loop's i++ lands on skipEnd
			continue
		}
		if raw[i] != '{' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw[i:]))
		dec.UseNumber()
		var st map[string]any
		if err := dec.Decode(&st); err != nil || len(st) == 0 {
			continue
		}
		// InputOffset is relative to the sub-slice the decoder was given, and
		// after one Decode it sits just past the object's closing brace.
		return st, i, i + int(dec.InputOffset()), true
	}
	return nil, 0, 0, false
}

// fenceByteRange converts a fenceLoc into a byte span over the original file.
//
// A well-formed fence yields its whole region (opener line through closer line
// inclusive). A DAMAGED one yields just its orphaned opener line: the same
// single line the splice path deletes, and for the same reason — that one line
// is the corruption, and nothing else in the document may be assumed to be.
// Returns an empty span when there is no fence at all.
func fenceByteRange(lines []string, loc fenceLoc) (int, int) {
	offsets := make([]int, len(lines)+1)
	for i, line := range lines {
		offsets[i+1] = offsets[i] + len(line) + 1 // +1 for the "\n" that split consumed
	}
	switch {
	case loc.OK:
		return offsets[loc.Open], offsets[loc.Close+1]
	case loc.OrphanOpen >= 0:
		return offsets[loc.OrphanOpen], offsets[loc.OrphanOpen+1]
	default:
		return 0, 0
	}
}

// preservedProse returns everything in the document that was neither the fence
// region nor the recovered object, with the two lines RenderTemplate re-emits
// (heading and intro) dropped so a re-render cannot duplicate them.
//
// Everything else survives verbatim, including a legitimate later fence in a
// "## Notes" section: it lands AFTER the re-rendered state fence, which is what
// keeps the state fence first and therefore the one the next read finds.
func preservedProse(raw string, skipStart, skipEnd, objStart, objEnd int) string {
	cut := func(s string, start, end int) string {
		if start >= end || start < 0 {
			return s
		}
		// Clamp rather than bail. fenceByteRange charges every line a trailing
		// "\n", which the final line does not have when the file ends without
		// one — so its end offset overruns by a byte. Returning s unchanged
		// there silently left the old fence in the output; clamping cuts what
		// was actually asked for.
		if end > len(s) {
			end = len(s)
		}
		return s[:start] + s[end:]
	}
	// Cut the later span first so the earlier span's offsets stay valid.
	if objStart >= skipEnd {
		raw = cut(raw, objStart, objEnd)
		raw = cut(raw, skipStart, skipEnd)
	} else {
		raw = cut(raw, skipStart, skipEnd)
		raw = cut(raw, objStart, objEnd)
	}

	lines := strings.Split(raw, "\n")
	drop := func(pred func(string) bool) {
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
		if len(lines) > 0 && pred(strings.TrimSpace(lines[0])) {
			lines = lines[1:]
		}
	}
	drop(func(s string) bool { return strings.HasPrefix(s, headingPrefix) })
	drop(func(s string) bool { return strings.HasPrefix(s, introPrefix) })

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// writeFence puts `body` in the file's state fence.
//
// Normally it SPLICES: it replaces only the first json fence and leaves the
// heading, intro and any agent-written prose byte-for-byte untouched. A file
// with no fence gains one; a missing file is created from the template. An
// orphaned (unterminated) json-fence opener is deleted — and only that one
// line — so a legitimate later fence (e.g. in an agent-written "## Notes"
// section) is never touched.
//
// When the state had to be RECOVERED from outside the fence, splicing is not
// enough: the document's shape is what was wrong with it. It is re-rendered
// from RenderTemplate with the recovered state in the fence, and `keep`
// (everything that was neither fence nor recovered object) appended after.
func writeFence(path, agentName, body, keep string, recovered bool) error {
	if recovered {
		out := RenderTemplate(agentName, body)
		if keep != "" {
			out += "\n" + keep + "\n"
		}
		return os.WriteFile(path, []byte(out), 0o640)
	}

	fenceLines := []string{"```json", body, "```"}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		// Only treat missing file as create-fresh; propagate other errors.
		if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		return os.WriteFile(path, []byte(RenderTemplate(agentName, body)), 0o640)
	}

	if len(strings.TrimSpace(string(raw))) == 0 {
		return os.WriteFile(path, []byte(RenderTemplate(agentName, body)), 0o640)
	}

	lines := strings.Split(string(raw), "\n")
	loc := findStateFence(lines)

	// Deliberately built without a capacity hint. state.md is a few KB, so the
	// hint bought nothing measurable, and both spellings computed the capacity
	// from len(lines) — a length derived from a file whose `## Notes` section an
	// agent may grow without limit. CodeQL reads that arithmetic as a potential
	// allocation-size overflow (go/allocation-size-overflow) and it is right to:
	// the value is not bounded by anything this package enforces. The invariants
	// do hold today — findStateFence assigns Close inside a loop bound by
	// i < len(lines), so Open < Close <= len(lines)-1 and neither the
	// subtraction nor lines[Close+1:] can go out of range — but an unbounded
	// input feeding an allocation size is not worth defending for an
	// optimization this code does not need. Do not add the hint back.
	var out []string
	switch {
	case loc.OK:
		// Line splice: replace lines[Open..Close] inclusive. Everything
		// before Open and after Close survives byte-for-byte.
		out = append(out, lines[:loc.Open]...)
		out = append(out, fenceLines...)
		out = append(out, lines[loc.Close+1:]...)
	case loc.OrphanOpen >= 0:
		// Replace only the one orphaned opener line, in place, with the new
		// fence. Never strip any other ```json line — that is what protects
		// a legitimate fence further down (e.g. in Notes).
		//
		// The new fence goes HERE, not appended at the end of the file: if a
		// legitimate fence already exists later in the document (e.g. in
		// Notes), appending after it would make that later fence the new
		// "first" fence. Get would then return the Notes fence's data
		// instead of the state we just wrote, and a SECOND write would hit
		// the loc.OK branch and splice over the Notes fence — destroying it.
		// Writing in place keeps the new fence first and leaves everything
		// after it byte-for-byte untouched.
		out = append(out, lines[:loc.OrphanOpen]...)
		out = append(out, fenceLines...)
		out = append(out, lines[loc.OrphanOpen+1:]...)
	default:
		out = appendFence(append([]string{}, lines...), fenceLines)
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o640)
}

// appendFence appends a blank separator line and the fence lines to the end
// of the document, trimming any trailing blank lines first so spacing is
// consistent regardless of how much trailing whitespace the source had.
func appendFence(lines []string, fenceLines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = append(lines, "")
	lines = append(lines, fenceLines...)
	lines = append(lines, "")
	return lines
}
