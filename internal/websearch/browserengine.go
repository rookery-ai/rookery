package websearch

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// PageRenderer renders a URL in a real browser and returns its HTML.
//
// Declared here as a one-method interface rather than importing
// internal/browser, so websearch keeps no dependency on the browser subsystem
// and its tests need no Chromium. internal/coder supplies the real
// implementation.
type PageRenderer interface {
	RenderHTML(ctx context.Context, url string) (string, error)
}

// browserProvider is the cascade's last resort.
//
// The keyless engines fail in a way this package already treats as normal: a
// 200-OK JS challenge is indistinguishable from genuine no-results, which is
// exactly why "zero results" means "try the next engine" here. When every
// scraper has been exhausted, rendering one of those same pages in a real
// browser usually succeeds, because the challenge is precisely what a browser
// is able to satisfy.
//
// It is LAST because it is by far the slowest — several seconds and a browser
// process, against a single HTTP request for the others. Placing it earlier
// would tax every search to help the minority that need it.
type browserProvider struct {
	base     string
	renderer PageRenderer
}

func (p *browserProvider) Name() string { return "browser-ddg" }

func (p *browserProvider) Search(ctx context.Context, _ *http.Client, query string) ([]Result, error) {
	if p.renderer == nil {
		return nil, nil
	}
	body, err := p.renderer.RenderHTML(ctx, p.base+"?q="+urlQueryEscape(query))
	if err != nil {
		return nil, err
	}
	return parseDDGRendered(body), nil
}

// ddgResultLink matches the anchors DuckDuckGo's rendered results page uses.
// The rendered DOM differs from the html-only endpoint the scraper reads, so
// this cannot reuse that parser.
var ddgResultLink = regexp.MustCompile(`(?is)<a[^>]+href="(https?://[^"]+)"[^>]*data-testid="result-title-a"[^>]*>(.*?)</a>`)

// ddgAnyResultLink is the fallback shape: a plain result anchor with no test id.
// DuckDuckGo has changed this markup repeatedly, and a single brittle pattern
// is how a provider silently starts returning nothing — which the cascade would
// read as "no results" rather than "this parser is stale".
var ddgAnyResultLink = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result__a[^"]*"[^>]*href="(https?://[^"]+)"[^>]*>(.*?)</a>`)

func parseDDGRendered(body string) []Result {
	out := matchResults(ddgResultLink, body)
	if len(out) == 0 {
		out = matchResults(ddgAnyResultLink, body)
	}
	return out
}

func matchResults(re *regexp.Regexp, body string) []Result {
	var out []Result
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		title := stripTags(m[2])
		if href == "" || title == "" || seen[href] {
			continue
		}
		// DuckDuckGo's own domains are chrome, not results.
		if strings.Contains(href, "duckduckgo.com/") {
			continue
		}
		seen[href] = true
		out = append(out, Result{Title: title, URL: href})
	}
	return out
}

// WithBrowser appends the browser-backed provider to a cascade.
//
// Returns the list unchanged when no renderer is available, so an install with
// no browser runtime behaves exactly as it did before this existed.
func WithBrowser(providers []Provider, renderer PageRenderer) []Provider {
	if renderer == nil {
		return providers
	}
	return append(providers, &browserProvider{base: ddgRenderedEndpoint, renderer: renderer})
}

// ddgRenderedEndpoint is the ordinary, JavaScript-driven results page — not the
// html-only endpoint the scrapers use, which is the very thing that serves the
// challenge page this provider exists to get past.
const ddgRenderedEndpoint = "https://duckduckgo.com/"

func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('+')
		default:
			for _, c := range []byte(string(r)) {
				b.WriteString("%")
				const hex = "0123456789ABCDEF"
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
			}
		}
	}
	return b.String()
}
