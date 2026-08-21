package vault

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// This file holds the ONE shared implementation of "search the whole
// knowledge base and render a two-pass result" — exact ripgrep matches first,
// then ranked BM25 passages with their path and heading trail. It answers
// "where in my knowledge base is this?" the same way from every door that
// asks: the API engine's search_files host tool (internal/coder/hosttools.go)
// and the CLI-coder loopback bridge's /search (bridge.go).
//
// Before this existed, hosttools.go grew its own two-pass implementation and
// the bridge stayed on a bare literal-ripgrep dump — the bridge never got the
// ranked-BM25 upgrade the host tool received, so a CLI-coder workspace got
// strictly worse retrieval than an API-engine workspace for no reason a user
// could see or control. Factoring the algorithm here, called by both, is what
// makes that specific drift structurally impossible to repeat: there is only
// one place to upgrade.

// KB search budget constants, shared by every caller of SearchKB/RenderSearchResult.
const (
	// MaxSearchHits caps how many exact ripgrep matches are considered, across
	// all files (the Searcher itself already caps at 5 matches PER file).
	MaxSearchHits = 50

	// MaxRankedChunks caps how many ranked BM25 passages are considered.
	MaxRankedChunks = 10

	// ExactSectionBudgetNum/Den express, as an integer ratio, the CEILING on the
	// share of the total byte budget the exact-match section may consume WHEN
	// both sections have content to contribute (2/5 = 40%). Without a cap of its
	// own, a query that hits dozens of near-identical notes (a common literal
	// phrase repeated across a vault) can fill the entire budget with exact
	// lines before the ranked section ever gets a byte — silently dropping the
	// BM25 hits this search exists to surface (a note about "dentist" that says
	// "orthodontist" never reaches the caller). This is a CEILING, not a fixed
	// split: the ranked section afterward gets whatever the exact section
	// ACTUALLY used, not a complementary fixed share — see RenderSearchResult.
	ExactSectionBudgetNum = 2
	ExactSectionBudgetDen = 5

	// SearchTimeout bounds the underlying exact-match (ripgrep) search call.
	SearchTimeout = 10 * time.Second
)

// SearchKB runs the two-pass knowledge-base search and renders it into one
// budget-capped string: "where in my knowledge base is this?", answered with
// exact matches (unbeatable for a UUID, an error string, a code identifier)
// first, then ranked BM25 passages (finds a note about "dentist" that says
// "orthodontist", with heading trail so a caller gets usable context in one
// call). No matches is a NON-error "(no matches for %q)" result, never an
// empty string, so a caller can treat it as a normal outcome rather than a
// failure.
//
// searcher may be nil, meaning v.NewSearcher(); a caller passes a test double
// to exercise the degrade-on-exact-failure path deterministically. A failed
// exact-match search degrades to ranked-only results (logged, not failed) —
// ranked retrieval alone can still answer usefully.
func SearchKB(ctx context.Context, v *Vault, searcher Searcher, workspaceID, query string, maxBytes int) string {
	query = strings.TrimSpace(query)
	if query == "" || v == nil {
		return fmt.Sprintf("(no matches for %q)", query)
	}
	if searcher == nil {
		searcher = v.NewSearcher()
	}

	sctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()

	hits, err := searcher.Search(sctx, workspaceID, query)
	if err != nil {
		// Degrade, don't fail: BM25 ranked retrieval below can still answer
		// usefully even when the exact/ripgrep path is broken (missing binary,
		// subprocess error) — failing the whole call would be strictly worse
		// than falling back to ranked-only results. But a silent degrade would
		// hide a real regression from every human who might otherwise notice
		// ripgrep is broken, so it's logged rather than swallowed.
		slog.Warn("vault: kb search: exact match search failed; degrading to ranked-only results",
			"workspace", workspaceID, "error", err)
		hits = nil
	}
	if len(hits) > MaxSearchHits {
		hits = hits[:MaxSearchHits]
	}

	ranked := v.Indexer().Search(workspaceID, query, MaxRankedChunks)

	return RenderSearchResult(query, hits, ranked, maxBytes)
}

// SearchKBIn is SearchKB restricted to one vault-relative file.
//
// BOTH passes are scoped. Scoping only the exact one is the trap this function
// exists to close: the ranked pass would keep returning passages from the whole
// vault, so a search the caller believes is about one file quietly answers from
// others — and the caller has no way to tell, because the output looks the same.
//
// The exact pass also uses a much larger per-file cap here (MaxHitsInFile
// rather than the vault-wide five), because the reason to cap per file
// disappears once the caller has named the file.
func SearchKBIn(ctx context.Context, v *Vault, searcher Searcher, workspaceID, query, rel string, maxBytes int) string {
	query = strings.TrimSpace(query)
	rel = strings.TrimSpace(rel)
	if query == "" || rel == "" || v == nil {
		return fmt.Sprintf("(no matches for %q in %q)", query, rel)
	}
	if searcher == nil {
		searcher = v.NewSearcher()
	}

	sctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()

	hits, err := searcher.SearchIn(sctx, workspaceID, query, rel)
	if err != nil {
		// Same degrade-don't-fail policy as SearchKB, and logged for the same
		// reason: ranked-only results still answer usefully, but a silent
		// degrade would hide a broken ripgrep from everyone.
		slog.Warn("vault: scoped kb search: exact match search failed; degrading to ranked-only",
			"workspace", workspaceID, "path", rel, "error", err)
		hits = nil
	}
	if len(hits) > MaxHitsInFile {
		hits = hits[:MaxHitsInFile]
	}

	ranked := v.Indexer().SearchWithin(workspaceID, query, MaxRankedChunks, rel)

	if len(hits) == 0 && len(ranked) == 0 {
		// Non-error, like SearchKB: "nothing here" is a normal outcome, and an
		// `error:` string would trip the API engine's oscillation guard.
		return fmt.Sprintf("(no matches for %q in %s)", query, rel)
	}
	return RenderSearchResult(query, hits, ranked, maxBytes)
}

// RenderSearchResult renders already-fetched exact hits and ranked passages
// into the shared two-pass text view. Exact hits come first because a caller
// who typed an exact token wants it, but each section is bounded to its OWN
// byte budget (see ExactSectionBudgetNum/Den) so a flood of exact hits cannot
// crowd the ranked section out entirely. A truncated exact section says so
// explicitly ("…and N more exact matches") so the omission is visible rather
// than silent.
func RenderSearchResult(query string, hits []SearchHit, ranked []Scored, maxBytes int) string {
	// A ranked hit with empty Text matched ONLY via fieldBoost's filename/path
	// score (see nameOnlyEntry in index.go — an oversized or unconvertible file
	// that was deliberately never read into the index). Dropping it outright
	// here would repeat exactly the bug this whole indexing path exists to fix:
	// the file goes right back to being invisible to a query that names it,
	// just one layer up from where it was invisible before. It carries no body
	// to quote as a "passage", so it is tracked separately and reported by name
	// rather than silently discarded.
	var nonEmptyRanked, nameOnlyRanked []Scored
	for _, r := range ranked {
		if strings.TrimSpace(r.Text) != "" {
			nonEmptyRanked = append(nonEmptyRanked, r)
		} else {
			nameOnlyRanked = append(nameOnlyRanked, r)
		}
	}

	// The exact section is capped at its share only when both sections have
	// content — otherwise it gets the full budget rather than being trimmed
	// for a ranked section that has nothing to show anyway.
	exactBudget := maxBytes
	if len(hits) > 0 && len(nonEmptyRanked) > 0 {
		exactBudget = maxBytes * ExactSectionBudgetNum / ExactSectionBudgetDen
	}

	var exactSB strings.Builder
	if len(hits) > 0 {
		exactSB.WriteString("Exact matches:\n")
		header := exactSB.Len()
		omitted := 0
		for i, hit := range hits {
			line := fmt.Sprintf("%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet)
			// Always include at least the first hit even if it alone exceeds the
			// budget (mirrors the "top result must survive" rule elsewhere) —
			// only STOP once at least one hit is in and the next one would overflow.
			if exactSB.Len() > header && exactSB.Len()+len(line) > exactBudget {
				omitted = len(hits) - i
				break
			}
			exactSB.WriteString(line)
		}
		if omitted > 0 {
			fmt.Fprintf(&exactSB, "…and %d more exact matches (omitted to leave room for ranked passages)\n", omitted)
		}
	}

	// The ranked section gets whatever the exact section ACTUALLY used, not
	// its reserved share — an exact section with only a few short hits (well
	// under 40%) must not cap ranked at a fixed 60% and waste the rest of the
	// cap, and a full exact section must not leave ranked room to write a
	// passage that the final combined string would then have to cut mid-body.
	rankedBudget := maxBytes - exactSB.Len()
	if exactSB.Len() > 0 {
		rankedBudget-- // the "\n" separator joining the two sections, added below
	}

	var rankedSB strings.Builder
	const rankedHeader = "Related passages:\n"
	for _, r := range nonEmptyRanked {
		location := r.Path
		if r.Heading != "" {
			location += " — " + r.Heading
		}
		passage := fmt.Sprintf("\n[%s]\n%s\n", location, strings.TrimSpace(r.Text))
		// Stop cleanly once the next whole passage would overflow the ranked
		// budget — never emit a passage cut mid-body. The first passage is
		// still always included (same "top result survives" rule as above),
		// so this check only fires once something has already been written.
		if rankedSB.Len() > 0 && rankedSB.Len()+len(passage) > rankedBudget {
			break
		}
		if rankedSB.Len() == 0 {
			rankedSB.WriteString(rankedHeader)
		}
		rankedSB.WriteString(passage)
	}

	var sb strings.Builder
	sb.WriteString(exactSB.String())
	if exactSB.Len() > 0 && rankedSB.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString(rankedSB.String())

	// Name-only matches are appended last, whenever there's room, regardless of
	// whether the sections above found anything: they're a DIFFERENT kind of
	// fact ("a file by this name exists, but nothing readable was indexed for
	// it") that must survive even when it's the ONLY thing found — that's the
	// whole point of tracking it separately from nonEmptyRanked above.
	if len(nameOnlyRanked) > 0 {
		paths := make([]string, len(nameOnlyRanked))
		for i, r := range nameOnlyRanked {
			paths[i] = r.Path
		}
		line := fmt.Sprintf("Also found by filename (too large or not in a readable format to preview here): %s\n", strings.Join(paths, ", "))
		if sb.Len() > 0 {
			line = "\n" + line
		}
		if sb.Len()+len(line) <= maxBytes {
			sb.WriteString(line)
		}
	}

	if sb.Len() == 0 {
		return fmt.Sprintf("(no matches for %q)", query)
	}
	// Defensive final cap: with 200-byte ripgrep snippets and 1500-byte hard-bounded
	// BM25 chunks (see targetChunkChars), the budgets above already keep the total
	// under maxBytes in every realistic case, but this guarantees the result never
	// exceeds it even in a pathological edge case.
	out := sb.String()
	if len(out) > maxBytes {
		out = out[:runeSafeCut(out, maxBytes)]
	}
	return out
}
