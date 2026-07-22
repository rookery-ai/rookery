package vault

import (
	"fmt"
	"strings"
)

// maxKBContextBytes caps the knowledge-base block injected into a design turn.
const maxKBContextBytes = 6 * 1024

// maxKBContextChunks is how many retrieved passages are shown.
const maxKBContextChunks = 5

// BuildKBContext assembles the <knowledge_base> block for a design turn: a
// folder summary describing the vault's shape, plus the passages most relevant
// to what the user is asking for.
//
// This replaces an exhaustive path list (the old NotePaths) that capped at 60
// files in walk order and rendered 30 of them — arbitrary, truncated, and
// content-free. Retrieval runs over the WHOLE vault and every file type,
// matched on filename and path as well as body text (via the Indexer's BM25
// index), so a request naming "expenses.csv" resolves even though the
// designer is text-only and cannot call a search tool itself.
//
// When nothing matches, the block says so in as many words. A designer that
// invents a plausible note path is worse than one that asks.
func BuildKBContext(v *Vault, workspaceID, query string) string {
	if v == nil || workspaceID == "" {
		return ""
	}
	var sb strings.Builder

	summary := v.FolderSummary(workspaceID)
	if summary == "" {
		summary = "The knowledge base is empty."
	}
	sb.WriteString("Knowledge base structure:\n")
	sb.WriteString(summary)

	hits := v.Indexer().Search(workspaceID, query, maxKBContextChunks)
	var shown int
	for _, h := range hits {
		if strings.TrimSpace(h.Text) == "" {
			continue
		}
		if shown == 0 {
			sb.WriteString("\nExisting notes relevant to this request:\n")
		}
		location := h.Path
		if h.Heading != "" {
			location += " — " + h.Heading
		}
		entry := fmt.Sprintf("\n[%s]\n%s\n", location, strings.TrimSpace(h.Text))
		if sb.Len()+len(entry) > maxKBContextBytes {
			break
		}
		sb.WriteString(entry)
		shown++
	}
	if shown == 0 {
		sb.WriteString("\nNo existing notes matched this request. Do not guess a file path — if the user refers to a document you cannot see here, ask them where it is or whether they still need to add it.\n")
	}

	out := sb.String()
	if len(out) > maxKBContextBytes {
		out = out[:maxKBContextBytes] + "\n…(truncated)\n"
	}
	return out
}
