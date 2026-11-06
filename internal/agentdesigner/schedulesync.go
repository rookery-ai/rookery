package agentdesigner

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const (
	scheduleHeaderPrefix = "# Suggested schedule:"
	scheduleHeaderNone   = "none"
	// maxScheduleEdits bounds one pass. A file that needs more than this is being
	// rewritten rather than corrected, and the owner should do that deliberately.
	maxScheduleEdits = 10
	// maxScheduleReview keeps the card readable; it points somewhere, it is not an index.
	maxScheduleReview     = 10
	maxScheduleReviewRune = 300
)

// ReadScheduleHeader returns the raw value of AGENT.md's schedule header and whether
// the file has one. It reads what the file says, unlike parseSuggestedSchedule, which
// reports only a usable schedule and so cannot tell "none" from a typo or from no
// header at all.
func ReadScheduleHeader(agentMD string) (string, bool) {
	first, _, _ := strings.Cut(strings.TrimLeft(agentMD, " \t\r\n"), "\n")
	after, ok := strings.CutPrefix(strings.TrimSpace(first), scheduleHeaderPrefix)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(after), true
}

// SetScheduleHeader makes AGENT.md's first line state cronExpr, or "none" when it is
// empty, inserting the line when the file has no header. Everything else is untouched.
func SetScheduleHeader(agentMD, cronExpr string) string {
	if cronExpr == "" {
		cronExpr = scheduleHeaderNone
	}
	line := scheduleHeaderPrefix + " " + cronExpr
	rest := strings.TrimLeft(agentMD, " \t\r\n")
	first, tail, hasTail := strings.Cut(rest, "\n")
	if !strings.HasPrefix(strings.TrimSpace(first), scheduleHeaderPrefix) {
		return line + "\n" + agentMD
	}
	if !hasTail {
		return line
	}
	return line + "\n" + tail
}

// ReplaceCronLiteral replaces whole-token mentions of a cron expression. The boundary
// test is the whole point: replacing "0 9 * * *" must leave "10 9 * * *" alone, and a
// plain ReplaceAll would turn it into a different, confidently wrong schedule.
func ReplaceCronLiteral(text, oldExpr, newExpr string) string {
	if oldExpr == "" || oldExpr == newExpr {
		return text
	}
	var b strings.Builder
	rest := text
	offset := 0
	for {
		i := strings.Index(rest, oldExpr)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		start, end := offset+i, offset+i+len(oldExpr)
		b.WriteString(rest[:i])
		if cronTokenBoundary(text, start, end) {
			b.WriteString(newExpr)
		} else {
			b.WriteString(oldExpr)
		}
		rest = rest[i+len(oldExpr):]
		offset = end
	}
}

func cronTokenBoundary(text string, start, end int) bool {
	if start > 0 && isCronChar(text[start-1]) {
		return false
	}
	if end < len(text) && isCronChar(text[end]) {
		return false
	}
	// A neighbouring field means this is part of a longer expression, not this one.
	if start >= 2 && text[start-1] == ' ' && isCronFieldEnd(text[start-2]) {
		return false
	}
	if end+1 < len(text) && text[end] == ' ' && isCronFieldEnd(text[end+1]) {
		return false
	}
	return true
}

func isCronChar(c byte) bool {
	return isCronFieldEnd(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c == '/' || c == ',' || c == '-' || c == '_'
}

// isCronFieldEnd is deliberately narrower than isCronChar: an ordinary word beside the
// expression ("run 0 9 * * * daily") does not make it part of a longer one.
func isCronFieldEnd(c byte) bool {
	return c >= '0' && c <= '9' || c == '*'
}

// ScheduleEdit is one exact replacement a model proposed for a sentence stating when
// an agent runs.
type ScheduleEdit struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// ParseScheduleProse reads the model's answer. It tolerates a code fence or a sentence
// around the object, because models add them, and returns an error for anything else
// so the caller changes nothing.
func ParseScheduleProse(text string) (edits []ScheduleEdit, review []string, err error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, nil, errors.New("no JSON object in the answer")
	}
	var out struct {
		Edits  []ScheduleEdit `json:"edits"`
		Review []string       `json:"review"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, nil, err
	}
	for _, line := range out.Review {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > maxScheduleReviewRune {
			line = string(r[:maxScheduleReviewRune]) + "…"
		}
		if len(review) == maxScheduleReview {
			break
		}
		review = append(review, line)
	}
	return out.Edits, review, nil
}

var (
	protectedScheduleLine = regexp.MustCompile(`(?i)^\s*#\s*(suggested schedule|skills|connections|mcp|mcp servers|irreversible actions|required secrets)\b|^\s*#\s*-\s|\[CALL:`)
	protocolMarker        = regexp.MustCompile(`\[/?(CHAT|STATE|SILENT|CALL)\b`)
)

// ApplyScheduleEdits applies the edits that pass every guard and reports the rest as a
// count. These guards are why a model may touch an agent's instructions unattended: it
// never returns a file, so it can only replace single lines it quotes exactly, and it
// cannot reach the lines the platform acts on.
func ApplyScheduleEdits(agentMD string, edits []ScheduleEdit) (string, []ScheduleEdit, int) {
	applied := []ScheduleEdit{}
	refused := 0
	for i, e := range edits {
		if e.Find == e.Replace {
			continue
		}
		if i >= maxScheduleEdits || !scheduleEditAllowed(agentMD, e) {
			refused++
			continue
		}
		agentMD = strings.Replace(agentMD, e.Find, e.Replace, 1)
		applied = append(applied, e)
	}
	return agentMD, applied, refused
}

func scheduleEditAllowed(agentMD string, e ScheduleEdit) bool {
	if strings.TrimSpace(e.Find) == "" || strings.TrimSpace(e.Replace) == "" {
		return false
	}
	if strings.ContainsAny(e.Find, "\r\n") || strings.ContainsAny(e.Replace, "\r\n") {
		return false
	}
	if len(e.Replace) > 2*len(e.Find)+80 {
		return false
	}
	if protocolMarker.MatchString(e.Replace) {
		return false
	}
	if strings.Count(agentMD, e.Find) != 1 {
		return false
	}
	i := strings.Index(agentMD, e.Find)
	lineStart := strings.LastIndexByte(agentMD[:i], '\n') + 1
	lineEnd := len(agentMD)
	if n := strings.IndexByte(agentMD[i:], '\n'); n >= 0 {
		lineEnd = i + n
	}
	return !protectedScheduleLine.MatchString(agentMD[lineStart:lineEnd])
}

// RefreshDescription re-derives an agent's description after its AGENT.md changed. It
// declines when the current description is not what the old file derives to, because
// then something else wrote it and it is not this function's to replace.
func RefreshDescription(current, oldMD, newMD, fallback string) (string, bool) {
	if current != extractDescription(oldMD, fallback) {
		return "", false
	}
	next := extractDescription(newMD, fallback)
	return next, next != current
}
