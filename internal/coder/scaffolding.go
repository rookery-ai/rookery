package coder

import (
	"regexp"
	"strings"
)

// markupToken matches a tag-like construct: an ASCII <…> tag, or one using the
// full-width bars some providers wrap their markup in (｜DSML｜ and relatives).
// Deliberately loose — it is only ever the SECOND half of the decision.
var markupToken = regexp.MustCompile(`<[^<>\n]{1,120}>|｜[^｜\n]{1,60}｜`)

// LooksLikeToolScaffolding reports whether text is a model's tool-call machinery
// rather than a message meant for a person.
//
// It exists because a model with no structured tool channel will sometimes express
// the intent as text instead — and our prose fallback, meant to rescue a forgotten
// [CHAT] marker, faithfully delivered that markup to a user's phone. The trigger is
// our own grace turn: it strips the tools field while the model still has work
// queued, removing the well-formed way to express an intent without removing the
// intent.
//
// The test is keyed on OUR registry rather than on provider dialects:
//
//  1. Markup is present AND the text names one of the tools we offered on this run.
//     Decisive, and it cannot go stale — we do not have to recognise DeepSeek's
//     syntax, only our own tool names appearing alongside markup. Searched across
//     the whole string, not only inside the matched tokens: a provider that puts
//     the tool name in a JSON body between tags
//     (<tool_call>{"name":"web_search"}</tool_call>) would otherwise slip through,
//     and that is a common shape.
//  2. The text is mostly markup rather than sentences. A backstop for scaffolding
//     that happens to name no tool.
//
// offeredTools may be empty (a CLI coder reports none), in which case only rule 2
// applies. When in doubt this returns false: withholding a real message is worse
// than the warning a suppressed one produces.
func LooksLikeToolScaffolding(text string, offeredTools []string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	tokens := markupToken.FindAllString(s, -1)
	if len(tokens) == 0 {
		return false // no markup at all: it is prose, whatever it mentions
	}

	// Rule 1 — markup is present and the text names one of the tools we offered.
	// Searched across the whole string, not only inside the matched tokens: a
	// provider that puts the tool name in a JSON body between tags
	// (<tool_call>{"name":"web_search"}</tool_call>) would otherwise slip through,
	// and that is a common shape.
	//
	// The cost is a possible false positive — a genuine message containing both a
	// tool name and an unrelated bracketed aside is suppressed. That is the
	// direction the design chose: a withheld message leaves the user a warning they
	// can act on, a forwarded one costs their trust in the channel.
	//
	// Matched as a WHOLE TOKEN, not as a bare substring. Every API run offers a tool
	// literally named `glob`, so a substring test suppressed any genuine message
	// containing "global" or "globe" that also happened to carry a bracketed aside.
	for _, name := range offeredTools {
		if name != "" && containsWholeToken(s, name) {
			return true
		}
	}

	// Rule 2 — predominantly markup. Measured as a share of the text, so a sentence
	// containing one bracketed aside stays deliverable.
	var markupLen int
	for _, t := range tokens {
		markupLen += len(t)
	}
	return markupLen*2 >= len(s)
}

// containsWholeToken reports whether name occurs in s delimited by non-word bytes on
// both sides — i.e. as a token rather than as a fragment of a longer word.
//
// Scanned by hand rather than with a `\b…\b` regexp because this runs on the delivery
// path, once per offered tool: a per-call compile (or a cache keyed on a slice) buys
// nothing over a byte comparison.
//
// A multi-byte rune counts as a boundary, which is what we want: the full-width bars
// some providers wrap their markup in (｜web_search｜) must delimit a name, not hide it.
func containsWholeToken(s, name string) bool {
	for i := 0; i+len(name) <= len(s); {
		j := strings.Index(s[i:], name)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(name)
		beforeOK := j == 0 || !isWordByte(s[j-1])
		afterOK := end == len(s) || !isWordByte(s[end])
		if beforeOK && afterOK {
			return true
		}
		i = j + 1
	}
	return false
}

// isWordByte reports whether b is an ASCII word byte ([0-9A-Za-z_]). Underscore counts,
// so `web_search` is one token and `my_web_search` does not contain it.
func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}
