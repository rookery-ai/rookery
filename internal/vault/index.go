package vault

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/ilijad1/simple-agents/internal/convert"
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

	// maxIndexFileBytes bounds what is read into the index.
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
	v  *Vault
	mu sync.Mutex
	ws map[string]*wsIndex
}

type wsIndex struct {
	files map[string]*fileEntry // vault-relative path → cached chunks
	// Corpus statistics, recomputed when the file set changes.
	chunks    []Chunk
	df        map[string]int // term → number of chunks containing it
	avgLen    float64
	corpusGen int64
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

// Invalidate drops a workspace's cached index, forcing a full rebuild on the
// next search. Callers do not normally need this — revalidation is automatic —
// but a bulk import can use it to skip a stat walk.
func (i *Indexer) Invalidate(workspaceID string) {
	i.mu.Lock()
	delete(i.ws, workspaceID)
	i.mu.Unlock()
}

// Search returns the top-scoring chunks for a query, best first.
func (i *Indexer) Search(workspaceID, query string, limit int) []Scored {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}

	i.mu.Lock()
	idx := i.refresh(workspaceID)
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
	df, avgLen := idx.df, idx.avgLen
	i.mu.Unlock()

	if n == 0 || len(tf) != n {
		return nil
	}

	scored := make([]Scored, 0, n)
	for ci, c := range chunks {
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
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// refresh revalidates the workspace index against the filesystem. Must be
// called with the mutex held.
func (i *Indexer) refresh(workspaceID string) *wsIndex {
	idx := i.ws[workspaceID]
	if idx == nil {
		idx = &wsIndex{files: map[string]*fileEntry{}}
		i.ws[workspaceID] = idx
	}

	root := i.v.Root(workspaceID)
	if root == "" {
		return idx
	}

	seen := map[string]bool{}
	changed := false

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// .kb holds internal sidecars — never user knowledge, and it would
			// leak DB exports into retrieval results.
			if d.Name() == InternalDir {
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
		if statErr != nil || info.Size() > maxIndexFileBytes {
			return nil
		}
		seen[rel] = true

		if prev, ok := idx.files[rel]; ok &&
			prev.modTime == info.ModTime().UnixNano() && prev.size == info.Size() {
			return nil // unchanged: reuse cached chunks, no re-read, no re-extract
		}

		entry := i.buildEntry(path, rel, info.ModTime().UnixNano(), info.Size())
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
	return idx
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
	idx.corpusGen++
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
func foldPlural(f string) string {
	if len(f) > 3 && strings.HasSuffix(f, "s") && !strings.HasSuffix(f, "ss") {
		return f[:len(f)-1]
	}
	return f
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"was": true, "are": true, "you": true, "your": true, "from": true, "has": true,
	"have": true, "not": true, "but": true, "all": true, "can": true, "its": true,
	"about": true, "into": true, "out": true, "our": true, "their": true,
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
