package websearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// browserUA is a browser-like User-Agent. Keyless search endpoints serve a
// JS-challenge interstitial (200 OK, zero result blocks) to unfamiliar agents,
// which is indistinguishable from "no results" — so every provider sends this.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// maxBody bounds how much of a result page is read.
const maxBody = 2 << 20

// ddgProvider scrapes a DuckDuckGo HTML endpoint. Both the full and lite
// endpoints use the same result markup, so one implementation covers both.
type ddgProvider struct {
	name string
	base string
}

func (p *ddgProvider) Name() string { return p.name }

func (p *ddgProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	body, err := getPage(ctx, hc, p.base+"?q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	return parseDDG(body), nil
}

// reDDGBlock matches one result block: the redirect anchor followed by the
// snippet anchor.
var reDDGBlock = regexp.MustCompile(`(?is)<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>.*?<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

func parseDDG(doc string) []Result {
	var out []Result
	for _, m := range reDDGBlock.FindAllStringSubmatch(doc, -1) {
		target := decodeDDGRedirect(m[1])
		if target == "" {
			continue
		}
		out = append(out, Result{
			Title:   stripTags(m[2]),
			URL:     target,
			Snippet: stripTags(m[3]),
		})
	}
	return out
}

// decodeDDGRedirect recovers the real URL from DuckDuckGo's
// "//duckduckgo.com/l/?uddg=<urlencoded>" redirect wrapper.
func decodeDDGRedirect(href string) string {
	href = strings.TrimSpace(html.UnescapeString(href))
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	uddg := u.Query().Get("uddg")
	if uddg == "" {
		// Some endpoints link directly rather than via the redirect.
		if u.Scheme == "http" || u.Scheme == "https" {
			return u.String()
		}
		return ""
	}
	if real, err := url.QueryUnescape(uddg); err == nil {
		return real
	}
	return uddg
}

// getPage performs one GET and classifies the outcome. 429/5xx/network are
// wrapped as transient so the caller retries the same provider; a definitive
// 4xx is a hard failure that moves on to the next provider.
func getPage(ctx context.Context, hc *http.Client, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := hc.Do(req)
	if err != nil {
		return "", Transient(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", Transient(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(data), nil
}

var reAnyTag = regexp.MustCompile(`(?s)<[^>]*>`)

func stripTags(s string) string {
	s = reAnyTag.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Join(strings.Fields(html.UnescapeString(s)), " "))
}
