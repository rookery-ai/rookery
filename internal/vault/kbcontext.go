package vault

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxKBContextBytes caps the knowledge-base block injected into a design turn.
const maxKBContextBytes = 6 * 1024

// maxFolderSummaryBytes is the folder summary's own slice of maxKBContextBytes.
// FolderSummary's total output is unbounded in folder count (see its doc
// comment), so BuildKBContext must budget it explicitly rather than trust it
// to self-limit. One folder line runs roughly 30-60 bytes ("- notes/ — 12
// files (md×12)\n"), so 2 KiB holds on the order of 40-60 folders before the
// "…and N more folders" marker kicks in — enough to describe a normally-sized
// vault in full. The remaining ~4 KiB (two thirds of the total budget) is left
// for passages: the folder summary is a table of contents, not the payload —
// the retrieved TEXT is what the designer actually reasons about, so it gets
// the majority share.
const maxFolderSummaryBytes = 2 * 1024

// maxKBContextChunks is how many retrieved passages are shown.
const maxKBContextChunks = 5

// kbExcludedDirs are vault-relative top-level directories excluded from the
// retrieval BuildKBContext quotes into the design prompt. chats/ holds
// reflected conversation transcripts and agents/ holds an agent's internal
// state (agents/<id>/state.md, tools output, run logs) — both can contain
// content a user never wrote as a "note" and never expected to be echoed back
// into an unrelated design conversation just because a query happened to
// match a word in it (a bank number mentioned in passing in a chat, an
// "internal_note: do not surface" field in agent state). This mirrors what
// the old NotePaths manifest deliberately excluded. memory/ is NOT excluded
// here: it is already injected wholesale elsewhere in the design prompt, so
// quoting a passage from it is not new exposure, and it is genuinely useful
// "what already exists" signal for the designer.
//
// files/ (FilesDir, see import.go) holds the RAW preserved original of every
// imported document — ImportFile writes both notes/<name>.md (converted) and
// files/<name>.<ext> (original), and the index re-converts and chunks the
// original too, so an imported PDF/CSV/DOCX is indexed TWICE with identical
// text. Both copies then compete for the SAME limited passage budget here,
// wasting up to half of it on a duplicate the designer gains nothing from —
// the converted note in notes/ already carries whatever the original had.
// Excluded from the designer's retrieval only; an agent may legitimately want
// the original bytes, so it stays fully reachable via search_files/read_file.
//
// search_files (the LLM tool, internal/coder/hosttools.go) is UNAFFECTED —
// it calls Indexer().Search (whole vault), not SearchExcluding. Only this
// designer-facing block is scoped.
var kbExcludedDirs = []string{"chats", "agents", FilesDir}

// BuildKBContext assembles the <knowledge_base> block for a design turn: a
// folder summary describing the vault's shape, plus the passages most relevant
// to what the user is asking for.
//
// This replaces an exhaustive path list (the old NotePaths) that capped at 60
// files in walk order and rendered 30 of them — arbitrary, truncated, and
// content-free. Retrieval runs over the vault's user-facing content (every
// file type except chats/ and agents/, see kbExcludedDirs) and is matched on
// filename and path as well as body text (via the Indexer's BM25 index), so a
// request naming "expenses.csv" resolves even though the designer is
// text-only and cannot call a search tool itself.
//
// The folder summary and the passages each get their own byte budget (see
// maxFolderSummaryBytes) so one cannot silently starve or corrupt the other:
// without a per-section budget, a summary that happens to fit under the
// overall cap on its own can still leave a matching passage no room once
// appended, and the two facts "nothing matched" / "something matched but
// there was no room to show it" must never be conflated — a designer told
// point-blank that nothing matched, on a topic the user has notes on, is the
// exact failure the "ask instead of invent" instruction below exists to
// prevent.
func BuildKBContext(v *Vault, workspaceID, query string) string {
	if v == nil || workspaceID == "" {
		return ""
	}
	var sb strings.Builder

	summary := v.FolderSummary(workspaceID)
	if summary == "" {
		summary = "The knowledge base is empty."
	}
	summary = truncateFolderSummary(summary, maxFolderSummaryBytes)
	sb.WriteString("Knowledge base structure:\n")
	sb.WriteString(summary)

	hits := v.Indexer().SearchExcluding(workspaceID, query, maxKBContextChunks, kbExcludedDirs)
	// A hit with empty Text matched ONLY via fieldBoost's filename/path score
	// (see nameOnlyEntry in index.go: an oversized or unconvertible file that
	// was deliberately never read into the index). It is tracked separately
	// from candidates, not dropped: a file the user has, that the query names
	// by filename, must never be reported as "no existing notes matched" just
	// because there is no body text to quote as a passage.
	var candidates []Scored
	var nameOnly []string
	for _, h := range hits {
		if strings.TrimSpace(h.Text) != "" {
			candidates = append(candidates, h)
		} else {
			nameOnly = append(nameOnly, h.Path)
		}
	}

	// Build entries into a side buffer first and only commit the "relevant
	// notes" header once at least one entry is confirmed to fit. Writing the
	// header unconditionally (as a prior version did) and letting the very
	// first entry fail the budget check is exactly how the block ended up
	// asserting both "here are relevant notes" and "no notes matched" at once.
	header := "\nExisting notes relevant to this request:\n"
	budget := maxKBContextBytes - sb.Len() - len(header)
	var kept []string
	used := 0
	for _, h := range candidates {
		location := h.Path
		if h.Heading != "" {
			location += " — " + h.Heading
		}
		entry := fmt.Sprintf("\n[%s]\n%s\n", location, strings.TrimSpace(h.Text))
		if used+len(entry) > budget {
			break
		}
		kept = append(kept, entry)
		used += len(entry)
	}

	switch {
	case len(kept) > 0:
		sb.WriteString(header)
		for _, e := range kept {
			sb.WriteString(e)
		}
	case len(candidates) > 0:
		// Matches existed — this is NOT "no notes matched" — there was simply
		// no room left to quote them. Conflating the two would tell the
		// designer the opposite of the truth on a topic the user does have
		// notes on.
		fmt.Fprintf(&sb, "\n%d existing note(s) matched this request but did not fit in the space available here. Do not guess their content — ask the user directly instead.\n", len(candidates))
	case len(nameOnly) == 0:
		sb.WriteString("\nNo existing notes matched this request. Do not guess a file path — if the user refers to a document you cannot see here, ask them where it is or whether they still need to add it.\n")
		// else: only name-only matches exist — the block appended below covers it,
		// so falling through here without writing anything avoids a redundant or
		// contradictory "no notes matched" line sitting right next to it.
	}

	// Name-only matches are reported regardless of which branch above fired —
	// a file too large or unreadable to preview is a DIFFERENT fact than "no
	// content fit the budget", and must survive even when it's the only thing
	// found (see the case above for why "no existing notes matched" must not
	// also fire when this is non-empty).
	if len(nameOnly) > 0 {
		fmt.Fprintf(&sb, "\nAlso found by filename (too large or not in a readable format to preview here): %s\n", strings.Join(nameOnly, ", "))
	}

	out := sb.String()
	if len(out) > maxKBContextBytes {
		out = out[:runeSafeCut(out, maxKBContextBytes)] + "\n…(truncated)\n"
	}
	return out
}

// truncateFolderSummary bounds a FolderSummary result to maxBytes, cutting on
// whole folder lines (never mid-line) and, if anything was dropped, appending
// a visible count of what was omitted — silently dropping folders would be
// the same shape of bug as Finding 1(a): the designer would believe it has
// seen the vault's full shape when it has not.
func truncateFolderSummary(full string, maxBytes int) string {
	if len(full) <= maxBytes {
		return full
	}
	lines := strings.SplitAfter(full, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var sb strings.Builder
	kept := 0
	for _, line := range lines {
		if sb.Len()+len(line) > maxBytes {
			break
		}
		sb.WriteString(line)
		kept++
	}
	if remaining := len(lines) - kept; remaining > 0 {
		fmt.Fprintf(&sb, "…and %d more folders\n", remaining)
	}
	return sb.String()
}

// runeSafeCut returns the largest index <= maxBytes that does not split a
// multibyte UTF-8 rune, so a hard byte-slice truncation never emits invalid
// UTF-8. This is a backstop only — the per-section budgets above are what
// actually keep BuildKBContext under maxKBContextBytes in the normal case.
//
// This is the ONE copy in this package (chunk.go's hardSplitWindow and
// kbsearch.go's RenderSearchResult both call this one too — a second,
// byte-identical "runeFloor" used to live in chunk.go; keeping two functions
// that do the exact same thing in the same package is itself a maintenance
// hazard, so it was folded into this one, which is the one with a test).
func runeSafeCut(s string, maxBytes int) int {
	if maxBytes >= len(s) {
		return len(s)
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}
