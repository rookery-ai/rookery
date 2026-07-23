package websearch

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Production endpoints. Each is overridable in tests via DefaultProviders'
// baseOverride map, keyed by provider name.
const (
	ddgHTMLEndpoint = "https://html.duckduckgo.com/html/"
	ddgLiteEndpoint = "https://lite.duckduckgo.com/lite/"
	mojeekEndpoint  = "https://www.mojeek.com/search"
	bingEndpoint    = "https://www.bing.com/search"
)

// linkProvider scrapes an engine whose results are plain anchors matched by a
// regexp over the result list. Mojeek and Bing both fit this shape.
type linkProvider struct {
	name    string
	base    string
	param   string
	pattern *regexp.Regexp // submatch 1 = href, 2 = title html
}

func (p *linkProvider) Name() string { return p.name }

func (p *linkProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	body, err := getPage(ctx, hc, p.base+"?"+p.param+"="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, m := range p.pattern.FindAllStringSubmatch(body, -1) {
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		if !strings.HasPrefix(href, "http") {
			continue
		}
		title := stripTags(m[2])
		if title == "" {
			continue
		}
		out = append(out, Result{Title: title, URL: href})
	}
	return out, nil
}

// DefaultProviders returns the keyless cascade in priority order. baseOverride
// maps a provider name to a replacement base URL (tests point these at httptest
// servers); a nil or missing entry uses the production endpoint.
func DefaultProviders(baseOverride map[string]string) []Provider {
	pick := func(name, prod string) string {
		if b, ok := baseOverride[name]; ok && b != "" {
			return b
		}
		return prod
	}
	return []Provider{
		&ddgProvider{name: "ddg-html", base: pick("ddg-html", ddgHTMLEndpoint)},
		&ddgProvider{name: "ddg-lite", base: pick("ddg-lite", ddgLiteEndpoint)},
		&linkProvider{
			name: "mojeek", base: pick("mojeek", mojeekEndpoint), param: "q",
			pattern: regexp.MustCompile(`(?is)<a[^>]*class="ob"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`),
		},
		&linkProvider{
			name: "bing", base: pick("bing", bingEndpoint), param: "q",
			pattern: regexp.MustCompile(`(?is)<h2><a[^>]*href="([^"]+)"[^>]*>(.*?)</a></h2>`),
		},
	}
}
