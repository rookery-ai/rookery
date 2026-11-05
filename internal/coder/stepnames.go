package coder

import (
	"net/url"
	"strings"
)

// stepDetail says how much of a tool call's subject a chat app may show.
type stepDetail int

const (
	stepNoDetail stepDetail = iota // the subject is withheld
	stepText                       // a search query or vault-relative path
	stepHost                       // a URL, reduced to its host
)

type stepLabel struct {
	icon, doing, did string
	detail           stepDetail
	// fallback completes the label when a detail-bearing call has no usable
	// detail, so "Reading" never stands alone.
	fallback string
}

// stepLabels lists every host tool. A tool absent from it is a connector action
// or an MCP tool, whose arguments are arbitrary, so it shows its name only.
var stepLabels = map[string]stepLabel{
	"search_files":     {"📚", "Searching knowledge base:", "Searched knowledge base:", stepText, ""},
	"glob":             {"📁", "Finding files:", "Found files:", stepText, ""},
	"list_dir":         {"📁", "Looking in", "Looked in", stepText, "a folder"},
	"read_file":        {"📄", "Reading", "Read", stepText, "a file"},
	"write_file":       {"✏️", "Writing", "Wrote", stepText, "a file"},
	"edit_file":        {"✏️", "Editing", "Edited", stepText, "a file"},
	"kb_file_map":      {"📄", "Checking the outline of", "Checked the outline of", stepText, "a file"},
	"kb_table_query":   {"📊", "Querying table", "Queried table", stepText, "data"},
	"save_to_kb":       {"💾", "Saving to knowledge base", "Saved to knowledge base", stepNoDetail, ""},
	"web_search":       {"🌐", "Searching the web:", "Searched the web:", stepText, ""},
	"web_fetch":        {"🌐", "Fetching", "Fetched", stepHost, "a page"},
	"browser_read":     {"🌐", "Opening", "Opened", stepHost, "a page"},
	"browser_open":     {"🌐", "Opening", "Opened", stepHost, "a page"},
	"browser_page":     {"🖱️", "Checking the page", "Checked the page", stepNoDetail, ""},
	"browser_click":    {"🖱️", "Clicking on the page", "Clicked on the page", stepNoDetail, ""},
	"browser_fill":     {"🖱️", "Filling in the page", "Filled in the page", stepNoDetail, ""},
	"browser_press":    {"🖱️", "Pressing a key", "Pressed a key", stepNoDetail, ""},
	"browser_wait":     {"⏳", "Waiting on the page", "Waited on the page", stepNoDetail, ""},
	"bash":             {"💻", "Running a command", "Ran a command", stepNoDetail, ""},
	"run_script":       {"💻", "Running a script", "Ran a script", stepNoDetail, ""},
	"run_in_env":       {"💻", "Running a script", "Ran a script", stepNoDetail, ""},
	"install_packages": {"📦", "Installing packages", "Installed packages", stepNoDetail, ""},
	"get_state":        {"🧠", "Reading agent memory", "Read agent memory", stepNoDetail, ""},
	"set_state":        {"🧠", "Saving agent memory", "Saved agent memory", stepNoDetail, ""},
}

// FriendlyStep turns a milestone line into the two forms a chat app shows: what
// is happening now, and what happened once the run has moved on. It reads the
// line toolMilestone or browserMilestone produced, and any other line (the
// engine's own "🔁 retrying…") comes back unchanged in both forms.
//
// This is deliberately less than the web trace shows. A chat-app message passes
// through a third party and a group shows it to every member, so a command, a
// URL's path and query, a typed value and a connector's arguments never appear.
func FriendlyStep(line string) (doing, did string) {
	rest, ok := strings.CutPrefix(line, "🔧 ")
	if !ok {
		return line, line
	}
	name, detail := rest, ""
	if i := strings.IndexAny(rest, "( "); i >= 0 {
		name = rest[:i]
		if rest[i] == '(' {
			detail = strings.TrimSuffix(rest[i+1:], ")")
		}
	}
	label, known := stepLabels[name]
	if !known {
		span := codeSpan(name)
		return "🔌 Using " + span, "🔌 Used " + span
	}
	suffix := ""
	switch label.detail {
	case stepText:
		suffix = usableDetail(detail)
	case stepHost:
		suffix = hostOf(detail)
	}
	if suffix != "" {
		suffix = " " + codeSpan(suffix)
	} else if label.fallback != "" {
		suffix = " " + label.fallback
	}
	doing, did = label.icon+" "+label.doing, label.icon+" "+label.did
	if suffix == "" {
		// "Searching the web:" with nothing after it.
		return strings.TrimSuffix(doing, ":"), strings.TrimSuffix(did, ":")
	}
	return doing + suffix, did + suffix
}

// usableDetail rejects toolMilestone's fallback, which is the call's raw JSON
// arguments and can hold anything the model passed.
func usableDetail(detail string) string {
	detail = strings.Join(strings.Fields(detail), " ")
	if strings.HasPrefix(detail, "{") || strings.HasPrefix(detail, "[") {
		return ""
	}
	return detail
}

func hostOf(detail string) string {
	if !strings.Contains(detail, "://") {
		return ""
	}
	u, err := url.Parse(strings.TrimSuffix(detail, "…"))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// codeSpan is how a detail stays literal on every platform. Backslash-escaping
// does not survive the renderers: Telegram doubles the backslash and Slack shows
// it, while stripping punctuation would corrupt a name like my_file.md.
func codeSpan(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}
