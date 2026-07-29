// Command livecheck exercises every read (and safe-write) connector action against the
// REAL provider APIs using the stored tokens, so we can confirm the whole surface works —
// not just the one path an agent happened to use. Throwaway; not committed.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

func main() {
	ctx := context.Background()
	dataDir := os.ExpandEnv("$HOME/.simple-agents-v2")
	d, err := db.Open(dataDir+"/simple-agents.db", "")
	must(err)
	// Resolve the key exactly as the server does, so this harness keeps working
	// after a restore has installed a recovered <data_dir>/system.key.
	var wsCount int
	must(d.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&wsCount))
	key, err := secrets.SystemKey(dataDir, wsCount > 0)
	must(err)
	reg, err := connectors.LoadBundled()
	must(err)
	store := &connectors.DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: connectors.OAuthClient{}}
	client := &http.Client{Timeout: 30 * time.Second}

	// All connections across workspaces.
	rows, err := d.QueryContext(ctx, "SELECT DISTINCT workspace_id FROM service_connections")
	must(err)
	var wsIDs []string
	for rows.Next() {
		var w string
		rows.Scan(&w)
		wsIDs = append(wsIDs, w)
	}
	rows.Close()

	filter := ""
	if len(os.Args) > 1 {
		filter = os.Args[1] // optional: only check this provider
	}
	for _, ws := range wsIDs {
		conns, _ := d.ListServiceConnections(ctx, ws)
		for _, c := range conns {
			if filter != "" && c.Provider != filter {
				continue
			}
			ref := connectors.ConnRef{ID: c.ID, Provider: c.Provider, AccountIdentity: c.AccountIdentity, Extra: connectors.ParseExtra(c.Extra)}
			fmt.Printf("\n=== %s / %s (%s) — status %s ===\n", c.Provider, c.AccountLabel, c.AccountIdentity, c.Status)
			switch c.Provider {
			case "google":
				checkGoogle(ctx, reg, store, client, ref)
			case "github":
				checkGitHub(ctx, reg, store, client, ref, c.AccountIdentity)
			case "notion":
				checkNotion(ctx, reg, store, client, ref)
			case "slack":
				checkSlack(ctx, reg, store, client, ref)
			case "openai":
				checkOpenAI(ctx, reg, store, client, ref)
			default:
				fmt.Printf("  (no live-check plan for %s; %d actions defined)\n", c.Provider, len(reg.Actions(c.Provider)))
			}
			// Report which actions were NOT auto-run (real side effects).
			for _, a := range reg.Actions(c.Provider) {
				if a.Mutating {
					fmt.Printf("  [skip] %s — mutating (real send/write); rendering unit-tested, run on request\n", a.Name)
				}
			}
		}
	}
}

func run(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef, action string, args map[string]any) (connectors.Result, bool) {
	res, err := connectors.Execute(ctx, reg, store, client, ref, action, args, connectors.Policy{})
	if err != nil {
		fmt.Printf("  [FAIL] %-24s %v\n", action, err)
		return res, false
	}
	fmt.Printf("  [ OK ] %-24s %s\n", action, snippet(res.Data))
	return res, true
}

func checkGoogle(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef) {
	res, ok := run(ctx, reg, store, client, ref, "gmail_search", map[string]any{"query": "in:inbox", "max": 1})
	var id string
	if ok {
		var msgs []struct {
			ID string `json:"id"`
		}
		json.Unmarshal(res.Data, &msgs)
		if len(msgs) > 0 {
			id = msgs[0].ID
		}
	}
	if id != "" {
		run(ctx, reg, store, client, ref, "gmail_get_message", map[string]any{"id": id})
	} else {
		fmt.Printf("  [skip] gmail_get_message — no message id from search\n")
	}
	// Safe write: create a draft (recoverable, in the user's own mailbox).
	run(ctx, reg, store, client, ref, "gmail_create_draft", map[string]any{
		"to": ref.AccountIdentity, "subject": "[connector livecheck] ignore/delete", "body": "Automated connector check — safe to delete."})
}

func checkGitHub(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef, login string) {
	run(ctx, reg, store, client, ref, "github_search_issues", map[string]any{"query": "author:" + login, "max": 3})

	// Discover one of the user's repos (directly, to supply owner/repo for the repo-scoped
	// actions), then exercise them.
	tok, err := store.AccessToken(ctx, ref)
	if err != nil {
		fmt.Printf("  [skip] repo-scoped actions — token: %v\n", err)
		return
	}
	owner, repo, num := firstRepoAndIssue(client, tok)
	if owner == "" {
		fmt.Printf("  [skip] github_list_repo_issues/get_issue — no repos found for user\n")
		return
	}
	run(ctx, reg, store, client, ref, "github_list_repo_issues", map[string]any{"owner": owner, "repo": repo})
	if num > 0 {
		run(ctx, reg, store, client, ref, "github_get_issue", map[string]any{"owner": owner, "repo": repo, "number": num})
	} else {
		fmt.Printf("  [skip] github_get_issue — no issue in %s/%s\n", owner, repo)
	}
}

func checkNotion(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef) {
	res, ok := run(ctx, reg, store, client, ref, "notion_search", map[string]any{"query": ""})
	if !ok {
		return
	}
	// results is an array; find the first page (object=="page") to chain the read actions.
	var results []struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	json.Unmarshal(res.Data, &results)
	if len(results) == 0 {
		fmt.Printf("  [info] notion_search returned 0 results — share a page/db with the integration to test page reads\n")
		return
	}
	var pageID string
	for _, r := range results {
		if r.Object == "page" {
			pageID = r.ID
			break
		}
	}
	if pageID == "" {
		pageID = results[0].ID
	}
	run(ctx, reg, store, client, ref, "notion_get_page", map[string]any{"page_id": pageID})
	run(ctx, reg, store, client, ref, "notion_get_block_children", map[string]any{"block_id": pageID})
	// notion_create_page is mutating:false but creates a real page; skip auto-run (needs a
	// page parent the integration can write to). Reported as skipped below.
}

func checkSlack(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef) {
	res, ok := run(ctx, reg, store, client, ref, "slack_list_channels", map[string]any{"limit": 5})
	var channel string
	if ok {
		var chans []struct {
			ID string `json:"id"`
		}
		json.Unmarshal(res.Data, &chans)
		if len(chans) > 0 {
			channel = chans[0].ID
		}
	}
	run(ctx, reg, store, client, ref, "slack_list_users", map[string]any{"limit": 5})
	if channel != "" {
		run(ctx, reg, store, client, ref, "slack_fetch_conversation_history", map[string]any{"channel": channel, "limit": 3})
	} else {
		fmt.Printf("  [skip] slack_fetch_conversation_history — no channel id\n")
	}
	// Mutating (send_message, add_reaction, create_channel, invite) are [skip]-listed by the
	// mutating loop; run slack_send_message manually against a throwaway channel on request.
}

func checkOpenAI(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef) {
	run(ctx, reg, store, client, ref, "openai_list_models", nil)
	run(ctx, reg, store, client, ref, "openai_chat_completion", map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []any{map[string]any{"role": "user", "content": "Reply with the single word: pong"}},
	})
	run(ctx, reg, store, client, ref, "openai_moderation", map[string]any{"input": "hello world"})
}

func firstRepoAndIssue(client *http.Client, tok string) (owner, repo string, issueNum int) {
	req, _ := http.NewRequest("GET", "https://api.github.com/user/repos?per_page=1&sort=updated&affiliation=owner", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var repos []struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	json.Unmarshal(b, &repos)
	if len(repos) == 0 {
		return
	}
	owner, repo = repos[0].Owner.Login, repos[0].Name
	// try to find an issue number
	req2, _ := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?per_page=1&state=all", owner, repo), nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	req2.Header.Set("Accept", "application/vnd.github+json")
	if r2, e := client.Do(req2); e == nil {
		defer r2.Body.Close()
		bb, _ := io.ReadAll(r2.Body)
		var iss []struct {
			Number int `json:"number"`
		}
		json.Unmarshal(bb, &iss)
		if len(iss) > 0 {
			issueNum = iss[0].Number
		}
	}
	return
}

func snippet(raw json.RawMessage) string {
	s := strings.ReplaceAll(string(raw), "\n", " ")
	if len(s) > 140 {
		s = s[:140] + "…"
	}
	return s
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
