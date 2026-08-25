package browser

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// urlPattern finds http(s) URLs the user has typed into the design conversation.
// Deliberately conservative: trailing punctuation is trimmed rather than
// matched, since a URL at the end of a sentence usually arrives with a full
// stop attached and probing "https://example.com." fails DNS.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// FeasibilityCache remembers probe results for one design session, so a
// conversation that mentions the same site five times pays for one render.
type FeasibilityCache struct {
	mu   sync.Mutex
	seen map[string]string
}

// maxProbesPerSession bounds how many distinct sites one design conversation
// will render. A design turn is a BLOCKING request with no progress stream, so
// every probe is time the user spends watching a spinner; a conversation that
// pasted a dozen links would otherwise stall badly.
const maxProbesPerSession = 3

// probeTimeout keeps one probe short. Feasibility is a hint, not a
// verification: a site slow enough to exceed this is a site the build will
// struggle with anyway, and reporting "it timed out" is itself useful.
const probeTimeout = 20 * time.Second

// loadFeasibility renders any URL the user has just mentioned and reports what
// stands in the way of automating it.
//
// This exists because both designers are WithNoTools for their own reasoning
// and cannot investigate a site themselves — the same structural gap
// vault.BuildKBContext fills for knowledge-base retrieval, and it is solved the
// same way: the work is done FOR the designer and injected as a block, rather
// than handing the designer a browser.
//
// The point is to find the blocker BEFORE the user approves a build. A captcha
// or a Cloudflare interstitial cannot be worked around, so an agent planned
// against one is a six-minute build that was never going to succeed; saying so
// during the conversation costs one page render.
//
// Best-effort throughout: no browser, no URL, or any failure yields an empty
// block and the conversation proceeds exactly as it did before this existed.
func Feasibility(ctx context.Context, r Renderer, cache **FeasibilityCache, userMessage string) string {
	if r == nil || !r.Available().OK {
		return ""
	}
	urls := extractURLs(userMessage)
	if len(urls) == 0 {
		return ""
	}
	if *cache == nil {
		*cache = &FeasibilityCache{seen: map[string]string{}}
	}
	c := *cache

	var b strings.Builder
	probes := 0
	for _, u := range urls {
		if probes >= maxProbesPerSession {
			break
		}
		c.mu.Lock()
		cached, ok := c.seen[u]
		c.mu.Unlock()
		if ok {
			b.WriteString(cached)
			continue
		}

		line := probeSite(ctx, r, u)
		if line == "" {
			continue
		}
		probes++
		c.mu.Lock()
		c.seen[u] = line
		c.mu.Unlock()
		b.WriteString(line)
	}
	if b.Len() == 0 {
		return ""
	}
	return "<site_feasibility>\n" +
		"You looked at the site(s) the user mentioned. This is what is actually there — " +
		"trust it over any assumption about how the site works:\n\n" +
		b.String() +
		"\nIf a site is behind a captcha or a bot check, an agent CANNOT get past it. " +
		"Say so plainly now and suggest another route (an official API, an email or " +
		"export the site offers, a different site) rather than planning an agent that will fail.\n" +
		"</site_feasibility>\n\n"
}

func probeSite(ctx context.Context, r Renderer, rawURL string) string {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	res, err := r.Render(pctx, Request{URL: rawURL, WaitFor: "networkidle", Limit: 400})
	if err != nil {
		slog.Debug("designer feasibility probe failed", "url", rawURL, "err", err)
		return fmt.Sprintf("- %s — could not be opened (%s). It may be down, or unreachable from this server.\n",
			rawURL, shortErr(err))
	}

	switch res.Blocked {
	case "cloudflare", "captcha", "bot-check":
		return fmt.Sprintf("- %s — BLOCKED: %s. An agent cannot get past this.\n", rawURL, res.BlockedNote)
	case "login":
		return fmt.Sprintf("- %s — reachable, but behind a sign-in form. An agent can sign in only if the "+
			"user stores the username and password as secrets, and the site does not demand a code from a phone.\n", rawURL)
	}

	// Not blocked. Say what the agent will be working with, since that is what
	// decides the tier and therefore the plan.
	forms := 0
	for _, e := range res.Elements {
		if e.Role == "textbox" || e.Role == "searchbox" {
			forms++
		}
	}
	desc := fmt.Sprintf("- %s — reachable and readable", rawURL)
	if res.Title != "" {
		desc += fmt.Sprintf(" (%q)", strings.TrimSpace(res.Title))
	}
	if forms > 0 {
		desc += fmt.Sprintf("; it has %d form field(s), so filling it in is possible", forms)
	}
	return desc + ".\n"
}

// extractURLs pulls distinct http(s) URLs out of one message, preserving order.
func extractURLs(msg string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range urlPattern.FindAllString(msg, -1) {
		u := strings.TrimRight(m, ".,;:!?")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// shortErr keeps a probe failure to one clause. The full Playwright error runs
// to several lines of stack-shaped text, which would crowd out the rest of the
// block for no gain — the designer needs to know it failed, not how.
func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
