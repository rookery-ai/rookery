package vault

import (
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/ilijad1/rookery/internal/convert"
)

// Scored is a chunk with its relevance score.
type Scored struct {
	Chunk
	Score float64
}

// BM25 parameters. These are the standard defaults and are not worth tuning
// without a relevance benchmark to tune against.
const (
	bm25K1 = 1.2
	bm25B  = 0.75

	// Weights for where a term matched. A term in the filename or a heading is
	// stronger evidence of aboutness than one occurrence in a body paragraph:
	// "the ATC report" should find atc-report.md even if the body never repeats
	// those words.
	pathMatchBoost    = 2.5
	headingMatchBoost = 1.5

	// maxIndexFileBytes bounds what is read INTO MEMORY for indexing. A file over
	// this cap is never read — its content contributes nothing to BM25 — but it is
	// still represented in the index as a name/path-only entry (see nameOnlyEntry),
	// so it stays findable via fieldBoost's filename/path match. Dropping the file
	// entirely here would be silently wrong: a design turn would be told "no
	// existing notes matched" about a document the user does have.
	maxIndexFileBytes = 4 << 20
)

// Indexer holds per-workspace retrieval state for the process's lifetime.
//
// The index is NOT persisted. At this vault size (hundreds of files, a few
// hundred KB) a rebuild is trivial, and keeping it in memory removes a schema,
// a migration, a corruption mode, and a staleness bug — the reliability
// argument for the whole feature.
//
// Cost control is what makes this safe to call on every design turn: a search
// revalidates by stat-ing files and comparing (mtime, size). Unchanged files
// reuse their cached chunks, so extracting text from a PDF or spreadsheet
// happens ONCE PER FILE VERSION, never per query.
type Indexer struct {
	v *Vault

	// mapMu guards ONLY the ws map itself — looking up or creating a
	// workspace's entry. It is never held across a refresh or a search: each
	// workspace's own wsIndex.mu does that, scoped to that workspace alone,
	// so a slow PDF conversion for workspace A never blocks an unrelated
	// search in workspace B.
	mapMu sync.Mutex
	ws    map[string]*wsIndex
}

type wsIndex struct {
	// mu makes the reserve/refresh/snapshot sequence atomic for THIS
	// workspace, the same guarantee the old process-wide Indexer.mu gave,
	// just scoped down to one workspace instead of all of them.
	mu sync.Mutex

	files map[string]*fileEntry // vault-relative path → cached chunks
	// Corpus statistics, recomputed when the file set changes.
	chunks []Chunk
	df     map[string]int // term → number of chunks containing it
	avgLen float64
}

type fileEntry struct {
	modTime int64
	size    int64
	chunks  []Chunk
	terms   []map[string]int // per-chunk term frequencies, aligned with chunks
}

// Indexer returns the vault's retrieval index, creating it on first use.
func (v *Vault) Indexer() *Indexer {
	v.indexOnce.Do(func() {
		v.indexer = &Indexer{v: v, ws: map[string]*wsIndex{}}
	})
	return v.indexer
}

// wsIndexFor returns the wsIndex for a workspace, creating it on first use.
// Only the map lookup/insert is guarded — the returned pointer is then used
// (and locked) entirely outside mapMu, which is what lets two workspaces
// proceed independently.
func (i *Indexer) wsIndexFor(workspaceID string) *wsIndex {
	i.mapMu.Lock()
	defer i.mapMu.Unlock()
	idx := i.ws[workspaceID]
	if idx == nil {
		idx = &wsIndex{files: map[string]*fileEntry{}}
		i.ws[workspaceID] = idx
	}
	return idx
}

// Invalidate drops a workspace's cached index, forcing a full rebuild on the
// next search. Callers do not normally need this — revalidation is automatic —
// but a bulk import can use it to skip a stat walk. An in-flight Search that
// already holds the old wsIndex is unaffected and completes against it; the
// NEXT Search call gets a fresh, empty entry.
func (i *Indexer) Invalidate(workspaceID string) {
	i.mapMu.Lock()
	delete(i.ws, workspaceID)
	i.mapMu.Unlock()
}

// Search returns the top-scoring chunks for a query, best first, over the
// WHOLE vault. This is the path `search_files` (the LLM tool) uses — its
// behavior must stay whole-vault, so it calls this, never SearchExcluding.
func (i *Indexer) Search(workspaceID, query string, limit int) []Scored {
	return i.search(workspaceID, query, limit, nil)
}

// SearchExcluding is Search scoped away from paths under the given prefixes
// (vault-relative, e.g. "chats", "agents"). It exists as an explicit,
// documented capability rather than something callers bolt on by filtering
// Search's result slice: filtering after the top-N are already chosen would
// silently return fewer than `limit` results whenever excluded paths would
// otherwise have placed in the top N. Excluding at the candidate stage, before
// ranking picks the top N, means the N results returned are the true top N of
// the searchable (non-excluded) set.
func (i *Indexer) SearchExcluding(workspaceID, query string, limit int, excludePrefixes []string) []Scored {
	return i.search(workspaceID, query, limit, excludePrefixes)
}

func (i *Indexer) search(workspaceID, query string, limit int, excludePrefixes []string) []Scored {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}

	idx := i.wsIndexFor(workspaceID)
	idx.mu.Lock()
	i.refresh(workspaceID, idx)
	// Snapshot under the lock. `chunks := idx.chunks` would NOT be safe: a slice
	// header shares its backing array, and recompute() re-appends into that same
	// array — so a concurrent refresh (a scheduled run calling search_files while
	// a design turn retrieves) would tear reads out from under the scorer.
	// recompute() also allocates a fresh slice for the same reason.
	n := len(idx.chunks)
	chunks := make([]Chunk, n)
	copy(chunks, idx.chunks)
	tf := make([]map[string]int, 0, n)
	for _, path := range sortedKeys(idx.files) {
		tf = append(tf, idx.files[path].terms...)
	}
	// df is captured by reference (not copied) — that is only safe because
	// recompute() always assigns idx.df a BRAND NEW map (`idx.df = map[string]int{}`)
	// rather than clearing the existing one in place. If recompute() ever
	// changed to reuse/clear the old map, this reference would let a
	// concurrent refresh mutate df out from under the scorer running below,
	// unlocked. Preserve the fresh-map property in recompute() or copy df here.
	df, avgLen := idx.df, idx.avgLen
	idx.mu.Unlock()

	if n == 0 || len(tf) != n {
		return nil
	}

	scored := make([]Scored, 0, n)
	for ci, c := range chunks {
		if hasPathPrefix(c.Path, excludePrefixes) {
			continue
		}
		score := bm25Score(terms, tf[ci], df, avgLen, n)
		score += fieldBoost(terms, c)
		if score > 0 {
			scored = append(scored, Scored{Chunk: c, Score: score})
		}
	}
	// Stable ordering: score desc, then path, then line — so an identical query
	// always returns an identical list, which the tests pin and users rely on.
	sort.SliceStable(scored, func(a, b int) bool {
		if scored[a].Score != scored[b].Score {
			return scored[a].Score > scored[b].Score
		}
		if scored[a].Path != scored[b].Path {
			return scored[a].Path < scored[b].Path
		}
		return scored[a].Line < scored[b].Line
	})
	scored = applyScoreFloor(scored)
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// scoreFloorFrac drops a result scoring below this fraction of the TOP result's
// score. Without it, any nonzero term overlap — even one common word shared by
// chance — earns a nonzero BM25 score, so a query like "zzz-nothing-here"
// against a near-empty vault can surface a scaffold README as a "Related
// passages" hit purely because it happens to contain the word "here" (see the
// stopwords addition above, which fixes the specific word but not the general
// shape of the bug: SOME other word will always slip through eventually). A
// relative floor catches that general case: a chunk whose score is a tiny
// fraction of the best match is noise, not a genuine candidate, regardless of
// which word caused the overlap. 10% is a starting point — permissive enough
// not to prune a second/third genuinely-relevant chunk that merely scores lower
// than the top one (field boosts and idf already produce a wide legitimate
// score spread), while still cutting the common case of "the only overlap is
// one stray function word".
const scoreFloorFrac = 0.10

// applyScoreFloor drops every result scoring below scoreFloorFrac of the top
// result's score, EXCEPT the top result itself, which always survives if any
// result does — the floor is relative to it, so it can never be the one thing
// it excludes. scored must already be sorted best-first. Builds a fresh slice
// rather than filtering in place: scored[:0]-style in-place compaction would
// alias the same backing array this function still needs to read from for
// later (unfiltered) elements.
func applyScoreFloor(scored []Scored) []Scored {
	if len(scored) == 0 {
		return scored
	}
	floor := scored[0].Score * scoreFloorFrac
	kept := make([]Scored, 0, len(scored))
	kept = append(kept, scored[0])
	for _, s := range scored[1:] {
		if s.Score >= floor {
			kept = append(kept, s)
		}
	}
	return kept
}

// hasPathPrefix reports whether a vault-relative path (forward-slash
// separated, as Chunk.Path always is) falls under one of the given top-level
// prefixes. A plain strings.HasPrefix would wrongly match "chats-export/x.md"
// against prefix "chats" — the exact-segment-or-slash check avoids that.
func hasPathPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// refresh revalidates the given workspace's index against the filesystem.
// Must be called with idx.mu held.
func (i *Indexer) refresh(workspaceID string, idx *wsIndex) {
	root := i.v.Root(workspaceID)
	if root == "" {
		return
	}

	seen := map[string]bool{}
	changed := false

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip every dot-directory, not just .kb: this vault is
			// Obsidian-style, so .obsidian/ (app.json, workspace.json) is
			// routine, and a vault living inside a git working tree would
			// otherwise surface .git/COMMIT_EDITMSG as "content". None of
			// that is user knowledge, and .kb specifically would leak DB
			// exports into retrieval results. path != root guards against a
			// workspace root itself living under a dot-prefixed path.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := i.v.Rel(workspaceID, path)
		if relErr != nil {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			// Distinct from the oversized case below: a stat failure (permission
			// denied, a symlink that vanished mid-walk, ...) means we don't even
			// know the file's size, so there's nothing safe to index for it. Log
			// it rather than swallowing it silently — a systemic permissions
			// problem should be visible, not just a quietly incomplete index.
			slog.Warn("vault: index: stat failed, skipping file", "path", rel, "error", statErr)
			return nil
		}
		seen[rel] = true

		// (mtime, size) is not a content hash: a same-size edit landing within
		// the same mtime tick would be missed and the stale cached chunks
		// reused. This is a real limitation of the cache key, but not a live
		// bug for anything written through this package: writeFileAtomic
		// always writes a fresh temp file and renames it into place, and a
		// rename always advances mtime on ext4/xfs/btrfs, so every write made
		// through the vault's own API gets a fresh cache key. It would only
		// bite a write made by some other process that intentionally
		// preserved both mtime and size.
		if prev, ok := idx.files[rel]; ok &&
			prev.modTime == info.ModTime().UnixNano() && prev.size == info.Size() {
			return nil // unchanged: reuse cached chunks, no re-read, no re-extract
		}

		var entry *fileEntry
		if info.Size() > maxIndexFileBytes {
			// Never read an oversized file's body into memory — that's the whole
			// point of the cap — but still represent it by name/path so it stays
			// findable (see maxIndexFileBytes's doc comment). Dropping it here
			// silently was the actual bug: the file simply vanished from every
			// search result, including a query naming it by filename.
			slog.Warn("vault: file exceeds index size cap; indexing name only",
				"path", rel, "size", info.Size(), "cap", maxIndexFileBytes)
			entry = nameOnlyEntry(rel, info.ModTime().UnixNano(), info.Size())
		} else {
			entry = i.buildEntry(path, rel, info.ModTime().UnixNano(), info.Size())
		}
		if entry == nil {
			delete(idx.files, rel)
			changed = true
			return nil
		}
		idx.files[rel] = entry
		changed = true
		return nil
	})

	for rel := range idx.files {
		if !seen[rel] {
			delete(idx.files, rel)
			changed = true
		}
	}
	if changed || idx.df == nil {
		idx.recompute()
	}
}

// nameOnlyEntry builds a degraded index entry for a file whose body is deliberately
// never read — too large to safely load into memory (see maxIndexFileBytes). It
// mirrors the degraded entry buildEntry itself falls back to for an unconvertible
// format: an empty-text chunk that still carries Path, so fieldBoost's filename/path
// match keeps the file findable by name even though it contributes zero BM25 term
// signal from its content.
func nameOnlyEntry(rel string, modTime, size int64) *fileEntry {
	return &fileEntry{
		modTime: modTime,
		size:    size,
		chunks:  []Chunk{{Path: rel, Text: "", Line: 1}},
		terms:   []map[string]int{{}},
	}
}

// buildEntry reads and chunks one file. Non-markdown files go through convert,
// which is why this runs once per file version and never per query.
func (i *Indexer) buildEntry(abs, rel string, modTime, size int64) *fileEntry {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	var chunks []Chunk
	if strings.EqualFold(filepath.Ext(rel), ".md") {
		chunks = ChunkMarkdown(rel, string(data))
	} else {
		res, convErr := convert.ToMarkdown(data, convert.Options{Filename: rel})
		if convErr != nil {
			// Not convertible (a binary blob, an image): it is still findable by
			// name via the path boost, so index it with an empty body rather
			// than dropping it entirely.
			chunks = []Chunk{{Path: rel, Text: "", Line: 1}}
		} else {
			chunks = ChunkPlain(rel, res.Markdown)
		}
	}
	if len(chunks) == 0 {
		chunks = []Chunk{{Path: rel, Text: "", Line: 1}}
	}
	terms := make([]map[string]int, len(chunks))
	for ci, c := range chunks {
		terms[ci] = termFreq(tokenize(c.Text))
	}
	return &fileEntry{modTime: modTime, size: size, chunks: chunks, terms: terms}
}

// recompute rebuilds corpus-wide statistics from the per-file caches.
func (idx *wsIndex) recompute() {
	// A FRESH slice, never idx.chunks[:0] — a reader may still be scoring the
	// old backing array (see the snapshot comment in Search).
	idx.chunks = make([]Chunk, 0, len(idx.chunks))
	idx.df = map[string]int{}
	total := 0
	for _, path := range sortedKeys(idx.files) {
		entry := idx.files[path]
		for ci, c := range entry.chunks {
			idx.chunks = append(idx.chunks, c)
			total += len(c.Text)
			for term := range entry.terms[ci] {
				idx.df[term]++
			}
		}
	}
	if n := len(idx.chunks); n > 0 {
		idx.avgLen = float64(total) / float64(n)
	}
}

// bm25Score is the standard Okapi BM25 score of one chunk for a query.
func bm25Score(queryTerms []string, tf map[string]int, df map[string]int, avgLen float64, n int) float64 {
	if avgLen <= 0 {
		avgLen = 1
	}
	docLen := 0.0
	for _, c := range tf {
		docLen += float64(c)
	}
	var score float64
	for _, term := range queryTerms {
		f := float64(tf[term])
		if f == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(df[term])+0.5)/(float64(df[term])+0.5))
		score += idf * (f * (bm25K1 + 1)) / (f + bm25K1*(1-bm25B+bm25B*docLen/avgLen))
	}
	return score
}

// fieldBoost rewards matches in the file path and the heading trail. Without
// it, a query naming a file ("expenses") would score zero against a document
// whose body never repeats its own name.
func fieldBoost(queryTerms []string, c Chunk) float64 {
	pathTerms := termFreq(tokenize(strings.ReplaceAll(c.Path, "/", " ")))
	headTerms := termFreq(tokenize(c.Heading))
	var boost float64
	for _, term := range queryTerms {
		if pathTerms[term] > 0 {
			boost += pathMatchBoost
		}
		if headTerms[term] > 0 {
			boost += headingMatchBoost
		}
	}
	return boost
}

// tokenize lowercases and splits on non-alphanumerics, dropping stopwords and
// single characters, then applies a light plural fold (see foldPlural).
// Deliberately NOT a linguistic stemmer (no vowel/suffix rules, no
// dictionary, no Porter algorithm): a deterministic, explainable tokenizer
// beats a clever one we cannot debug when a search "should" have matched.
// The one normalization it does apply is load-bearing — without it, a query
// for "appointment" cannot find a heading written "Appointments", which is
// exactly the kind of literal-match miss this index exists to fix.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 || stopwords[f] {
			continue
		}
		out = append(out, foldPlural(f))
	}
	return out
}

// foldPlural strips a single trailing "s" (but never from a word ending
// "ss", so "process"/"address" are left alone) to fold a simple plural onto
// its singular form. It is applied identically at index time and query time
// (both go through tokenize), so its only real requirement is internal
// consistency — not that the form it produces is linguistically correct.
//
// That bare heuristic collides on ordinary English words that end in "s"
// without being plurals: "news" -> "new" made a note mentioning "today's
// news" a top hit for someone searching for a "new roof", and "lens" ->
// "len" is the same shape of bug. foldExceptions is a pragmatic, manually
// curated patch for this — it is NOT a linguistic rule (there is no way to
// derive "news is not a plural" from the string alone without a
// dictionary), and it will never be exhaustive. That is the accepted cost
// of keeping the fold a cheap, explainable heuristic instead of a real
// stemmer: pay for recall (folding "appointments" -> "appointment" so the
// query "appointment" finds a heading written "Appointments", which is the
// entire reason this fold exists) with a short, growable list of precision
// exceptions, rather than not folding at all.
func foldPlural(f string) string {
	if foldExceptions[f] {
		return f
	}
	if len(f) > 3 && strings.HasSuffix(f, "s") && !strings.HasSuffix(f, "ss") {
		return f[:len(f)-1]
	}
	return f
}

// foldExceptions are common English words ending in "s" that are not
// plurals, so folding them to a bare stem would produce a form that never
// occurs in ordinary text and collides the word with the wrong query (see
// foldPlural's doc comment). A few of these (e.g. "gas", "bus") are already
// safe via the length floor or the "ss" exclusion and are listed anyway so
// the set documents intent rather than relying on an incidental side effect
// of an unrelated guard.
var foldExceptions = map[string]bool{
	"news": true, "lens": true, "analysis": true, "basis": true, "crisis": true,
	"thesis": true, "status": true, "series": true, "species": true, "campus": true,
	"bonus": true, "focus": true, "virus": true, "census": true, "canvas": true,
	"atlas": true, "iris": true, "axis": true, "gas": true, "bus": true, "plus": true,
	"minus": true, "versus": true, "alias": true, "chaos": true, "ethos": true,
	"jeans": true, "glasses": true, "physics": true, "mathematics": true,
	"politics": true, "economics": true, "diabetes": true,
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"was": true, "are": true, "you": true, "your": true, "from": true, "has": true,
	"have": true, "not": true, "but": true, "all": true, "can": true, "its": true,
	"about": true, "into": true, "out": true, "our": true, "their": true,

	// Extended common function words: carry no retrieval signal on their own, so
	// leaving them out of the list is what lets a query like "zzz-nothing-here"
	// register a nonzero BM25 score against a scaffold note purely because it
	// contains "here" (see applyScoreFloor's doc comment — the score floor is
	// the general-case backstop; these are the specific words already known to
	// cause it in practice).
	"here": true, "there": true, "what": true, "when": true, "where": true,
	"how": true, "who": true, "why": true, "which": true, "some": true,
	"any": true, "more": true, "most": true, "other": true, "such": true,
	"only": true, "own": true, "same": true, "than": true, "too": true,
	"very": true, "just": true, "now": true, "then": true, "also": true,
	"been": true, "being": true, "does": true, "did": true, "had": true,
	"were": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "must": true, "one": true,
	"two": true, "get": true, "got": true, "make": true, "made": true,
	"see": true, "saw": true, "use": true, "used": true,
}

func termFreq(terms []string) map[string]int {
	out := make(map[string]int, len(terms))
	for _, t := range terms {
		out[t]++
	}
	return out
}

func sortedKeys(m map[string]*fileEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FolderSummary describes the shape of a workspace's knowledge base: which
// folders exist, how many files each holds, and which kinds. It replaces the
// old exhaustive path list (vault.NotePaths), which capped at 60 files in walk
// order and rendered the first 30 — so in a 153-note vault, 123 notes were
// invisible and the visible 30 were arbitrary.
//
// What this bounds: FILE COUNT WITHIN a folder collapses to one line (a count
// per extension), so a 500-file folder is still one line, not 500. What it
// does NOT bound: TOTAL output size as FOLDER COUNT grows — one line is
// emitted per distinct folder with no cap on how many folders there are, so a
// vault with hundreds of folders (many agents each with their own subfolder,
// per-day journal folders, ...) produces a proportionally unbounded string.
// Callers that need a byte-bounded result (BuildKBContext) must budget and
// truncate this output themselves.
func (v *Vault) FolderSummary(workspaceID string) string {
	root := v.Root(workspaceID)
	if root == "" {
		return ""
	}
	type folderStat struct {
		count int
		exts  map[string]int
	}
	folders := map[string]*folderStat{}

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == InternalDir {
				return filepath.SkipDir
			}
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := v.Rel(workspaceID, path)
		if relErr != nil {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = "(root)"
		}
		stat, ok := folders[dir]
		if !ok {
			stat = &folderStat{exts: map[string]int{}}
			folders[dir] = stat
		}
		stat.count++
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
		if ext == "" {
			ext = "no extension"
		}
		stat.exts[ext]++
		return nil
	})

	if len(folders) == 0 {
		return "The knowledge base is empty."
	}
	var sb strings.Builder
	for _, dir := range sortedFolderNames(folders) {
		f := folders[dir]
		kinds := make([]string, 0, len(f.exts))
		for _, ext := range sortedExtNames(f.exts) {
			kinds = append(kinds, fmt.Sprintf("%s×%d", ext, f.exts[ext]))
		}
		fmt.Fprintf(&sb, "- %s/ — %d files (%s)\n", dir, f.count, strings.Join(kinds, ", "))
	}
	return sb.String()
}

func sortedFolderNames[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedExtNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
