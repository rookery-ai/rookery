# Public Release and Organization Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move `rookery` and `rookery-web` from the personal account `ilijad1` to the `rookery-ai` organization and publish both, destroying every artifact built under the personal account so versioning restarts cleanly at v0.1.0.

**Architecture:** Three gated phases. Phase A destroys existing releases/tags/packages and prepares content while both repositories are private. Phase B transfers, renames the Go module and every `ilijad1` link, and configures the organization. Phase C flips visibility, cuts v0.1.0, and verifies the installers — which cannot be tested earlier, because release assets on a private repository return 404 to an anonymous request.

**Tech Stack:** Go 1.26, GitHub Actions, release-please, goreleaser, cosign, Astro (website), `gh` CLI, POSIX shell.

**Spec:** `docs/superpowers/specs/2026-08-12-public-release-and-org-migration-design.md`

## Global Constraints

- **Both repositories stay PRIVATE until Phase C.** No task in Phase A or B may change visibility.
- **Every file change lands as a PR into `main`.** `main` only advances through merged PRs. Never commit directly, never force-push, never merge to `main` outside a PR.
- **Conventional Commits** on every commit and every PR title: `type(scope): summary`. Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **Branch names** must match `^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$`.
- **New owner:** `rookery-ai`. **New module path:** `github.com/rookery-ai/rookery`.
- **Maintainer / security contact:** `Rookery <security@rookery.cloud>`.
- **First release under the org:** `v0.1.0`. `bump-minor-pre-major` stays `true`.
- **Git history is NOT rewritten.** Binaries are deleted at HEAD only. Never run `git filter-repo` or `git rebase` over published history.
- **`make ci` must pass before opening any PR** in `rookery`.
- **Run the `docs-sync` skill before opening any PR** that touches `README.md`, `CLAUDE.md`, or the website.
- **Generic RFC1918 examples in connector YAML stay.** Only *this developer's* addresses are scrubbed. Do not flatten this into "remove all private IPs".
- Owner-only steps are marked **🔴 OWNER GATE** and block until confirmed.

---

## Phase A — Teardown and preparation (repositories stay private)

### Task 1: Destroy releases and tags

**Files:** none (remote operations only)

**Interfaces:**
- Produces: a repository with zero releases and zero tags, which Task 3 depends on before resetting the version manifest.

- [ ] **Step 1: Record what exists, for the verification step**

```bash
gh release list --repo ilijad1/rookery --limit 20
git tag
```

Expected: six releases (`v0.4.0`, `v0.3.2`, `v0.3.1`, `v0.3.0`, `v0.2.0`, `v0.1.0`) and the same six tags.

- [ ] **Step 2: Close the stale release-please PR**

```bash
gh pr close 114 --repo ilijad1/rookery \
  --comment "Closing: all releases and tags are being deleted ahead of the transfer to the rookery-ai organization. release-please will open a fresh release PR from a reset manifest."
```

- [ ] **Step 3: Delete all six releases including their tags**

```bash
for t in v0.4.0 v0.3.2 v0.3.1 v0.3.0 v0.2.0 v0.1.0; do
  gh release delete "$t" --repo ilijad1/rookery --yes --cleanup-tag
done
```

`--cleanup-tag` deletes the remote tag with the release. Without it the tag survives and release-please would compute the next version from it.

- [ ] **Step 4: Delete any local tags left behind**

```bash
git tag -d v0.1.0 v0.2.0 v0.3.0 v0.3.1 v0.3.2 v0.4.0 2>/dev/null || true
git fetch --prune --prune-tags origin
```

- [ ] **Step 5: Verify nothing remains**

```bash
gh release list --repo ilijad1/rookery --limit 20
git ls-remote --tags origin
git tag
```

Expected: all three produce **no output**. If any tag remains, delete it with `git push origin :refs/tags/<tag>` before continuing.

---

### Task 2: 🔴 OWNER GATE — delete the GHCR container package

**Files:** none

The authenticated token holds `repo`, `workflow`, `read:org`, `gist`, `admin:public_key`. It has **no `read:packages` or `delete:packages`**, so `GET /user/packages` returns 403 and the package can be neither listed nor deleted from this session.

- [ ] **Step 1: Confirm the gap**

```bash
gh api user/packages?package_type=container
```

Expected: `403 — You need at least read:packages scope to list packages.`

- [ ] **Step 2: Owner grants scope, OR deletes manually**

Either:

```bash
gh auth refresh -h github.com -s read:packages,delete:packages
```

Or delete it in the web UI at `https://github.com/users/ilijad1/packages` → `rookery` → Package settings → Delete package.

- [ ] **Step 3: If scope was granted, delete via API**

```bash
gh api user/packages?package_type=container --jq '.[].name'
gh api -X DELETE user/packages/container/rookery
```

- [ ] **Step 4: Verify**

```bash
gh api user/packages?package_type=container --jq '.[].name'
```

Expected: no `rookery` entry. The package must be gone before the transfer, or a stale `ghcr.io/ilijad1/rookery` stays published under the personal account with no repository behind it.

---

### Task 3: Reset the version line

**Files:**
- Delete: `CHANGELOG.md`
- Modify: `.release-please-manifest.json`

**Interfaces:**
- Consumes: zero tags and zero releases from Task 1.
- Produces: a manifest at `0.0.0`, so the first `feat:` after the migration cuts `v0.1.0`.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b chore/reset-version-line
```

- [ ] **Step 2: Delete the changelog**

```bash
git rm CHANGELOG.md
```

It documents releases that no longer exist and every entry links to `github.com/ilijad1`. release-please regenerates it from the first conventional commit after the reset.

- [ ] **Step 3: Reset the manifest**

Write `.release-please-manifest.json`:

```json
{
  ".": "0.0.0"
}
```

- [ ] **Step 4: Verify the config already targets 0.1.0**

```bash
grep -n 'initial-version\|bump-minor-pre-major' release-please-config.json
```

Expected: `"initial-version": "0.1.0"` and `"bump-minor-pre-major": true`. Both are already correct — do not change `release-please-config.json`.

- [ ] **Step 5: Verify the build is unaffected**

```bash
make ci-test
```

Expected: PASS. (`CHANGELOG.md` is not read by any test.)

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: reset the version line for the rookery-ai migration

Every release and tag built under the personal account is deleted, so the
changelog documents releases that no longer exist and every entry links to
github.com/ilijad1. Resetting the manifest to 0.0.0 makes the first
conventional commit under the organization cut v0.1.0."
```

---

### Task 4: Remove development artifacts

**Files:**
- Delete: `simple-agents`, `livecheck`, `.server.pid`, `CHANGES.md`, `AGENT_DESIGNER_TEST_PROMPTS.md`, `plans/agent-draft-save.md`, `plans/composio-reliability.md`
- Modify: `.gitignore`, `.dockerignore`, `internal/brandcheck/brandcheck_test.go:34-41`

**Interfaces:**
- Produces: a tree with no committed binaries, and a `brandcheck` exemption list that no longer names deleted paths.

**Do NOT delete `cmd/livecheck/`** — the directory is a live dev harness referenced by eight provider YAMLs, `internal/connectors/registry.go`, and three `//go:build livecheck` tests. Only the compiled `livecheck` binary at the repository root goes.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b chore/remove-development-artifacts
```

- [ ] **Step 2: Delete the artifacts**

```bash
git rm simple-agents livecheck .server.pid CHANGES.md AGENT_DESIGNER_TEST_PROMPTS.md
git rm -r plans
```

- [ ] **Step 3: Confirm the livecheck SOURCE survived**

```bash
git ls-files cmd/livecheck
```

Expected: `cmd/livecheck/README.md` and `cmd/livecheck/main.go`. If these are gone, restore them — the deletion was wrong.

- [ ] **Step 4: Add the ignore rules**

Append to `.gitignore`:

```
# compiled dev harnesses — never commit (48MB of these once shipped in the tree)
/livecheck
/simple-agents
*.pid
```

Append to `.dockerignore` the same three lines, under a comment reading `# compiled dev harnesses — keep them out of the build context`.

- [ ] **Step 5: Write the failing test — brandcheck must not name deleted paths**

`internal/brandcheck/brandcheck_test.go` currently exempts paths that no longer exist. Add this test to the same file:

```go
// TestAllowedPrefixesExist fails when the exemption list names a path that is no
// longer in the tree. A stale exemption is worse than a missing one: it silently
// widens the scan's blind spot if a future file lands at that path.
func TestAllowedPrefixesExist(t *testing.T) {
	root := repoRoot(t)
	for _, p := range allowedPrefixes {
		if p == "internal/brandcheck/" {
			continue // always present; it is this package
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("allowedPrefixes names %q, which does not exist: %v", p, err)
		}
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

```bash
go test ./internal/brandcheck/ -run TestAllowedPrefixesExist -v
```

Expected: FAIL, naming `CHANGELOG.md`, `CHANGES.md` and `plans/` as missing.

- [ ] **Step 7: Remove the stale exemptions**

In `internal/brandcheck/brandcheck_test.go`, change `allowedPrefixes` from:

```go
var allowedPrefixes = []string{
	"CHANGELOG.md",
	"CHANGES.md",
	"docs/superpowers/",
	"plans/", // the pre-superpowers plan archive, same class as docs/superpowers
	"internal/brandcheck/",
}
```

to:

```go
// CHANGELOG.md, CHANGES.md and plans/ were removed in the rookery-ai migration:
// the changelog was regenerated from a reset manifest and the other two were
// stale pre-rename artifacts. release-please recreates CHANGELOG.md at the first
// release, so its exemption is restored then, not now — an exemption for a file
// that does not exist is a blind spot waiting for a future file to land in it.
var allowedPrefixes = []string{
	"docs/superpowers/",
	"internal/brandcheck/",
}
```

- [ ] **Step 8: Run the full brandcheck package**

```bash
go test ./internal/brandcheck/ -v
```

Expected: PASS, both `TestNoLegacyBrandStrings` and `TestAllowedPrefixesExist`.

- [ ] **Step 9: Run the full suite**

```bash
make ci
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "chore: remove committed binaries and stale development files

simple-agents (32MB) and livecheck (16MB) are build artifacts; the first has
no source in the tree at all and the second is the output of cmd/livecheck,
which stays. Together they entered the Docker build context on every image
build. CHANGES.md and plans/ predate the rookery rename and still reference
cmd/simple-agents paths that stopped existing.

History is deliberately NOT rewritten — GitHub retains refs/pull/N/head for
all 153 PRs, so a filter-repo pass would rewrite main and leave every blob
fetchable. See the design spec.

brandcheck's exemption list drops the three deleted paths, and a new test
fails the build if it ever names a path that is not there."
```

---

### Task 5: Community health files for `rookery`

**Files:**
- Create: `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/bug_report.yml`, `.github/ISSUE_TEMPLATE/feature_request.yml`, `.github/ISSUE_TEMPLATE/config.yml`

**Interfaces:**
- Produces: `CONTRIBUTING.md`, which Task 10 extends with the `make hooks` instruction.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b docs/community-health-files
```

- [ ] **Step 2: Write `SECURITY.md`**

```markdown
# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Report it privately through GitHub's [private vulnerability reporting](https://github.com/rookery-ai/rookery/security/advisories/new),
which is enabled on this repository. If you cannot use that, email
**security@rookery.cloud**.

Please include the version (`rookery version`), how the instance is deployed
(native binary, container, `.deb`/`.rpm`), and enough detail to reproduce.

We aim to acknowledge a report within 72 hours.

## Supported versions

Rookery is pre-1.0. Only the latest release receives fixes.

## Scope

Rookery is **self-hosted**: you run the server, hold the keys, and own the data.
There is no Rookery-operated service to attack. Reports we are most interested in:

- Anything crossing a workspace boundary — one workspace reaching another's vault, secrets, or connections
- Escaping the coder sandbox (Landlock) or the vault path guard (`vault.Resolve`)
- Leaking a decrypted secret, OAuth token, or master password into a log, prompt, API response, or agent-visible file
- Bypassing the owner/workspace session split, or the approval gate on public-write connector actions
- The private-address dial guard (`internal/nethttp`) failing to block a request built from untrusted content

Known and documented, so not vulnerabilities in themselves — see `CLAUDE.md`:

- **Connectors and MCP deliberately reach private addresses.** Self-hosted services live at RFC1918 and Tailscale addresses; the URL comes from vendored YAML or from the owner's own typing.
- **The Python AST guardrail is a filter, not a security boundary.** Landlock and the skill-vetter audit are the enforcement.
- **Off Linux there is no filesystem sandbox at all.** `/healthz` reports this.
```

- [ ] **Step 3: Write `CONTRIBUTING.md`**

```markdown
# Contributing to Rookery

## Getting set up

```bash
git clone https://github.com/rookery-ai/rookery.git
cd rookery
make hooks     # installs the commit-msg hook — see "Hooks" below
make build     # builds the SPA into the binary, then the binary
make ci        # the full local gate; run this before opening a PR
```

`make ci` covers `gofmt`, `go vet`, `go test -race`, cross-compilation for all
six GOOS/GOARCH pairs, the frontend typecheck/lint/tests, and the documentation
sync check. It does **not** run the security scan, the container smoke test, or
the package smoke test — those run in CI, and the last is available locally as
`make ci-package` (kept out of `make ci` because a snapshot build takes minutes).

## Branching

Always branch off `main`. `main` only ever advances through merged pull requests.

Branch names must match:

```
^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$
```

For example `feat/oauth-redirect-pinning`, `fix/slack-socket-reconnect`.
This is enforced in CI. Bot branches (`release-please--*`, `dependabot/*`) are
exempt, because neither bot lets you name its branches.

## Commits and pull request titles

Every commit message and every PR title must be a
[Conventional Commit](https://www.conventionalcommits.org/): `type(scope): summary`.

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
Scope is optional but preferred — `feat(connectors):`, `fix(web/chat):`.

**The PR title matters more than the individual commits**, because merges are
squashes: the title becomes the commit that lands on `main` and the input
release-please reads to compute the next version.

## Hooks

`make hooks` points `core.hooksPath` at the committed `.githooks/` directory,
installing a `commit-msg` hook that rejects two things:

1. **Credentials and local-environment leakage** — home directory paths, `.lan`
   hostnames, RFC1918 literals, and provider key formats. GitHub's push
   protection scans file *content* only; it never reads a commit message or a PR
   description, so this hook and its CI counterpart cover what GitHub does not.
2. **Non-conventional commit messages.**

The hook is pure `grep -E` with no external dependency, deliberately: a hook that
shells out to a tool the contributor does not have installed silently succeeds,
which is worse than no hook. Patterns live in `.githooks/patterns.txt` and are
read by both the hook and the CI job, so local and CI enforcement cannot drift.

## Tests

Write the failing test first. `make ci-test` runs the Go suite with `-race`.
AST guardrail tests shell out to `python3` and self-skip without it — a skipped
security test is worse than a failing one, so install python3.

## Documentation

Four surfaces describe this project and each can be wrong without anything
failing: `README.md`, `CLAUDE.md`, the documentation site and the landing page
(the last two in [`rookery-ai/rookery-web`](https://github.com/rookery-ai/rookery-web)).

`make docs-sync-check` mechanises the checkable half — counts, variable names,
command names, provider names — against the source rather than against other
prose. Verify every claim against source, never against another document.

## Adding a connector

A connector is two YAML files — `internal/connectors/providers/<name>.yaml`
(auth) and `internal/connectors/connectors/<name>.yaml` (actions) — and no Go
code. Read the existing ones first; `CLAUDE.md` records the traps (a connector
answers in JSON, credentials cannot go in a request body, sending email is
`public_write` rather than merely `mutating`).

Mark a provider `unverified: true` unless you have exercised it against the live
API.

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
```

- [ ] **Step 4: Write `CODE_OF_CONDUCT.md`**

Fetch Contributor Covenant 2.1 verbatim and substitute the one placeholder it ships with:

```bash
curl -fsSL https://www.contributor-covenant.org/version/2/1/code_of_conduct.md -o CODE_OF_CONDUCT.md
sed -i 's#\[INSERT CONTACT METHOD\]#security@rookery.cloud#' CODE_OF_CONDUCT.md
grep -n 'security@rookery.cloud\|INSERT CONTACT' CODE_OF_CONDUCT.md
```

Expected: the `security@rookery.cloud` line present, and **no** remaining `INSERT CONTACT` — an unsubstituted placeholder is the single most common way this file ships broken. If the upstream fetch fails, copy the text from <https://www.contributor-covenant.org/version/2/1/code_of_conduct/> and make the same substitution by hand.

- [ ] **Step 5: Write `.github/PULL_REQUEST_TEMPLATE.md`**

```markdown
<!--
The PR TITLE must be a Conventional Commit — `type(scope): summary` — because
merges are squashes and the title becomes the commit that lands on main.
-->

## What this changes

## Why

## How it was verified

<!-- Commands actually run and their outcome. Not "tests pass" — which tests. -->

## Checklist

- [ ] `make ci` passes locally
- [ ] Branch name matches `type/short-description`
- [ ] Documentation updated if this touches a connector provider, a `ROOKERY_*`
      variable, a CLI subcommand, a core skill, a chat adapter, a backup
      destination, an `/api/v1` route, or a packaging target
- [ ] No credentials, home directory paths, `.lan` hostnames or private IPs in
      the diff, the commit messages, or this description
```

- [ ] **Step 6: Write `.github/ISSUE_TEMPLATE/bug_report.yml`**

```yaml
name: Bug report
description: Something does not work as documented
labels: [bug]
body:
  - type: textarea
    id: what-happened
    attributes:
      label: What happened
      description: What you expected, and what happened instead.
    validations:
      required: true
  - type: textarea
    id: reproduce
    attributes:
      label: Steps to reproduce
    validations:
      required: true
  - type: input
    id: version
    attributes:
      label: Version
      description: Output of `rookery version`.
    validations:
      required: true
  - type: dropdown
    id: install
    attributes:
      label: How is it installed
      options: [native binary, container, .deb, .rpm, built from source]
    validations:
      required: true
  - type: textarea
    id: healthz
    attributes:
      label: /healthz output
      description: >
        `curl -s localhost:8080/healthz`. Reports sandbox status, coder mode and
        host-tool presence — booleans only, never paths. Redact anything you are
        unsure about.
      render: json
  - type: checkboxes
    id: no-secrets
    attributes:
      label: Confirmation
      options:
        - label: This report contains no API keys, tokens, passwords or private URLs
          required: true
```

- [ ] **Step 7: Write `.github/ISSUE_TEMPLATE/feature_request.yml`**

```yaml
name: Feature request
description: Suggest a capability or a connector
labels: [enhancement]
body:
  - type: textarea
    id: problem
    attributes:
      label: What problem does this solve
      description: The situation you are in, not the solution you have in mind.
    validations:
      required: true
  - type: textarea
    id: proposal
    attributes:
      label: What you would like to happen
    validations:
      required: true
  - type: textarea
    id: alternatives
    attributes:
      label: What you tried instead
```

- [ ] **Step 8: Write `.github/ISSUE_TEMPLATE/config.yml`**

```yaml
blank_issues_enabled: false
contact_links:
  - name: Security vulnerability
    url: https://github.com/rookery-ai/rookery/security/advisories/new
    about: Report privately. Never open a public issue for a security problem.
  - name: Documentation
    url: https://rookery.cloud/docs
    about: Installation, configuration and connector reference.
```

- [ ] **Step 9: Verify the YAML parses**

```bash
python3 -c "import yaml,glob,sys; [yaml.safe_load(open(f)) for f in glob.glob('.github/ISSUE_TEMPLATE/*.yml')]; print('ok')"
```

Expected: `ok`.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "docs: add community health files for the public repository

CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, a PR template and issue forms.
CONTRIBUTING derives its rules from CLAUDE.md rather than inventing new ones.
SECURITY names GitHub private vulnerability reporting first and states the
scope, including the three documented non-vulnerabilities (connectors reach
private addresses on purpose, the AST guardrail is a filter not a boundary,
and there is no filesystem sandbox off Linux)."
```

---

### Task 6: Scrub local-environment references from code

**Files:**
- Modify: `internal/connectors/providers/github.yaml:8`, `packaging/README.md:70-71`, `internal/publicurl/policy_test.go:42,43,88,111`, `internal/coder/api_engine.go:541`, `internal/coder/api_engine_test.go:300,327,328,375,387`, `internal/coder/detect_test.go:9`, `internal/coder/smoke_test.go:16,43`, `internal/db/auditfilter_test.go:83`, `internal/gateway/router_test.go:48`, `web/api_connectors_test.go:178,185`, `web/api_services_preflight_test.go:159`

**Interfaces:**
- Produces: a tree free of this developer's own hostnames, IPs, home paths and Telegram id — which Task 9's guard test then pins.

Substitutions, applied consistently:

| From | To |
|---|---|
| `agents.rookie.lan` | `rookery.example.com` |
| `192.168.1.194` | `192.168.1.50` |
| `/home/rookie` | `/home/user` |
| `1843540314` | `100000001` |

**Do not touch** `internal/connectors/providers/*.yaml` hints such as
`http://192.168.1.10:8123`, `http://192.168.1.1:3000`, `http://192.168.1.10:6767`,
`http://192.168.1.10:8686`, `http://192.168.1.10:9696`, or
`internal/coder/netguard_test.go` and `internal/connectors/baseurl_test.go`.
Those are generic documentation of a supported deployment, not this developer's
address. The rule is whether the value is *this developer's* address or *an*
address.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b chore/scrub-local-environment-references
```

- [ ] **Step 2: Fix the user-facing one first**

`internal/connectors/providers/github.yaml:8` is read by every user connecting GitHub. Change:

```yaml
  - "Homepage URL: your dashboard URL (e.g. https://agents.rookie.lan)."
```

to:

```yaml
  - "Homepage URL: your dashboard URL (e.g. https://rookery.example.com)."
```

- [ ] **Step 3: Write the failing test for the smoke test's hardcoded path**

`internal/coder/smoke_test.go` hardcodes `/home/rookie/.opencode/bin/opencode` twice, so it can only ever run on one machine. Add to `internal/coder/smoke_test.go`:

```go
// TestOpencodeBinResolves proves the host-gated tests locate opencode by PATH
// lookup rather than by one developer's absolute home directory. The previous
// hardcoded /home/rookie/... path meant these tests skipped on every other
// machine while appearing to be host-gated rather than machine-gated.
func TestOpencodeBinResolves(t *testing.T) {
	got := opencodeBin()
	if strings.HasPrefix(got, "/home/") {
		t.Fatalf("opencodeBin() returned a hardcoded home path: %q", got)
	}
	if got != "" && !strings.HasSuffix(got, "opencode") {
		t.Fatalf("opencodeBin() = %q, want a path ending in opencode or empty", got)
	}
}
```

- [ ] **Step 4: Run it to verify it fails**

```bash
go test ./internal/coder/ -run TestOpencodeBinResolves -v
```

Expected: FAIL — `undefined: opencodeBin`.

- [ ] **Step 5: Add the helper and use it**

Add to `internal/coder/smoke_test.go` (and add `"os/exec"` and `"strings"` to its imports):

```go
// opencodeBin locates the opencode binary for the host-gated tests: PATH first,
// then the two directories npm and the official installer actually use. Returns
// "" when it is not installed, which the callers treat as "skip".
func opencodeBin() string {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, c := range []string{
		filepath.Join(home, ".opencode", "bin", "opencode"),
		filepath.Join(home, ".local", "bin", "opencode"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
```

Then in `TestSmokeOpencodeHostGated` replace:

```go
	bin := "/home/rookie/.opencode/bin/opencode"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("opencode not installed; skipping host-gated smoke")
	}
```

with:

```go
	bin := opencodeBin()
	if bin == "" {
		t.Skip("opencode not installed; skipping host-gated smoke")
	}
```

And in `TestOpencodeLiveGenerate` replace:

```go
	bin := "/home/rookie/.opencode/bin/opencode"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("opencode not installed")
	}
```

with:

```go
	bin := opencodeBin()
	if bin == "" {
		t.Skip("opencode not installed")
	}
```

Add `"path/filepath"` to the imports if absent.

- [ ] **Step 6: Run to verify it passes**

```bash
go test ./internal/coder/ -run TestOpencodeBinResolves -v
```

Expected: PASS.

- [ ] **Step 7: Apply the remaining substitutions**

```bash
grep -rIl -e 'agents\.rookie\.lan' -e '192\.168\.1\.194' -e '/home/rookie' -e '1843540314' \
  --include='*.go' --include='*.md' --include='*.yaml' . \
  | grep -v '^./docs/superpowers/' \
  | xargs sed -i \
      -e 's#agents\.rookie\.lan#rookery.example.com#g' \
      -e 's#192\.168\.1\.194#192.168.1.50#g' \
      -e 's#/home/rookie#/home/user#g' \
      -e 's#1843540314#100000001#g'
```

`docs/superpowers/` is excluded here and handled separately in Task 7, so the two diffs stay reviewable.

- [ ] **Step 8: Verify nothing was missed outside docs/superpowers**

```bash
grep -rIn -e 'agents\.rookie\.lan' -e '192\.168\.1\.194' -e '/home/rookie' -e '1843540314' . \
  --exclude-dir=.git --exclude-dir=docs | grep -v '^./docs/superpowers/'
```

Expected: **no output**.

- [ ] **Step 9: Verify the generic examples SURVIVED**

```bash
grep -rn '192\.168\.1\.10\|192\.168\.1\.1:3000' internal/connectors/providers/ | head
```

Expected: several hits in `home_assistant.yaml`, `adguard.yaml`, `bazarr.yaml`, `lidarr.yaml`, `prowlarr.yaml`. If these are gone, the sed was too broad — revert and redo.

- [ ] **Step 10: Run the full suite**

```bash
make ci
```

Expected: PASS. `internal/publicurl/policy_test.go` asserts a `.lan` host is rejected as `non_public_host`; `rookery.example.com` is a *public* host, so if that test now fails, its expectation must change to a case that still exercises the reserved-suffix branch — keep one `.lan` literal there for that purpose and re-run.

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "chore: replace developer-specific hosts, paths and ids

agents.rookie.lan, 192.168.1.194, /home/rookie and a real Telegram user id
appear across tests, packaging docs and — worst — the user-facing GitHub
connector setup steps, which every user reads while connecting.

Generic RFC1918 examples in the self-hosted connector YAMLs are deliberately
untouched: those document a supported deployment. The rule is whether the
value is THIS developer's address or AN address.

smoke_test.go's hardcoded /home/rookie/.opencode path made two tests
machine-gated while appearing host-gated; they now resolve via PATH with a
fallback, pinned by a test that rejects any /home/ prefix."
```

---

### Task 7: Scrub `docs/superpowers/`

**Files:**
- Modify: the 28 files under `docs/superpowers/` carrying the four identifiers

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b docs/scrub-design-doc-references
```

- [ ] **Step 2: List what will change**

```bash
grep -rIl -e 'agents\.rookie\.lan' -e '192\.168\.1\.194' -e '/home/rookie' -e '1843540314' docs/superpowers/ | tee /tmp/scrub-list.txt | wc -l
```

- [ ] **Step 3: Apply the same substitutions**

```bash
xargs -a /tmp/scrub-list.txt sed -i \
  -e 's#agents\.rookie\.lan#rookery.example.com#g' \
  -e 's#192\.168\.1\.194#192.168.1.50#g' \
  -e 's#/home/rookie#/home/user#g' \
  -e 's#1843540314#100000001#g'
```

- [ ] **Step 4: Verify**

```bash
grep -rIn -e 'agents\.rookie\.lan' -e '192\.168\.1\.194' -e '/home/rookie' -e '1843540314' docs/superpowers/
```

Expected: **no output**.

- [ ] **Step 5: Run the suite**

```bash
make ci-test
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "docs: scrub developer-specific references from the design archive

Same four substitutions as the code scrub, applied to the 28 design documents
that carry them. Kept as a separate commit so the code diff stays reviewable."
```

---

### Task 8: Set the package maintainer

**Files:**
- Modify: `.goreleaser.yaml:42-44`

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b chore/set-package-maintainer
```

- [ ] **Step 2: Change vendor and maintainer**

In `.goreleaser.yaml`, change:

```yaml
    vendor: Ilija Dimitrovski
    homepage: https://github.com/ilijad1/rookery
    maintainer: Ilija Dimitrovski <ilija.dimitrovski@kroute.ai>
```

to:

```yaml
    vendor: Rookery
    homepage: https://github.com/rookery-ai/rookery
    maintainer: Rookery <security@rookery.cloud>
```

This address is compiled into every `.deb` and `.rpm`. The `homepage` line is changed here rather than in Task 18 because it sits in the same three-line block.

- [ ] **Step 3: Verify the config still parses**

```bash
grep -n 'vendor\|maintainer\|homepage' .goreleaser.yaml
```

Expected: the three new values, no remaining `kroute.ai`.

- [ ] **Step 4: Confirm kroute.ai is gone from the whole tree**

```bash
grep -rIn 'kroute\.ai' . --exclude-dir=.git
```

Expected: **no output**. (`AGENT_DESIGNER_TEST_PROMPTS.md` carried the other occurrence and was deleted in Task 4; the two `docs/superpowers/plans/` hits are dated records quoting the old config — if they still appear, leave them, they are historical.)

If the two `docs/superpowers/plans/` hits remain, that is expected and acceptable; adjust the expectation to "no hits outside `docs/superpowers/`".

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: set the package maintainer to the project address

Ilija Dimitrovski <ilija.dimitrovski@kroute.ai> is compiled into every .deb
and .rpm goreleaser builds, putting a work address on a personal project's
packages. The project address survives adding maintainers later and matches
the contact in SECURITY.md."
```

---

### Task 9: Guard test — no personal-account references

**Files:**
- Create: `internal/brandcheck/owner_test.go`

**Interfaces:**
- Consumes: `repoRoot(t)`, `skipNames`, `maxScanBytes` from `internal/brandcheck/brandcheck_test.go`.
- Produces: a build failure whenever `ilijad1` reappears outside the dated archive — the mechanism that keeps the rename from eroding.

This is the long-term prevention for links, and it is written **now** so Task 18's rename has a test that fails first.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b test/guard-against-personal-account-references
```

- [ ] **Step 2: Write the failing test**

Create `internal/brandcheck/owner_test.go`:

```go
package brandcheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// personalOwner is the account the project was developed under before moving to
// the rookery-ai organization.
//
// This guard exists because the rename touched 188 Go files, a cosign
// certificate-identity regexp, two install scripts, a container image path and
// the website — and NONE of those fail visibly when stale. A wrong cosign
// identity produces a fully green pipeline and an unverifiable release; a stale
// image path produces documentation that pulls a package nobody publishes.
const personalOwner = "ilijad1"

// ownerAllowedPrefixes are exempt: dated design records describing what was true
// when written. Rewriting them would falsify the archive.
var ownerAllowedPrefixes = []string{
	"docs/superpowers/",
	"internal/brandcheck/",
}

func TestNoPersonalAccountReferences(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skipNames[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, p := range ownerAllowedPrefixes {
			if strings.HasPrefix(rel, p) {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxScanBytes {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		if bytes.Contains(data, []byte(personalOwner)) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d file(s) still reference the personal account %q:\n  %s\n\n"+
			"The project lives at github.com/rookery-ai/rookery. Stale references here "+
			"fail silently: a wrong cosign certificate-identity regexp still produces a "+
			"green pipeline and an unverifiable release.",
			len(offenders), personalOwner, strings.Join(offenders, "\n  "))
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
go test ./internal/brandcheck/ -run TestNoPersonalAccountReferences -v
```

Expected: FAIL, listing `go.mod`, `.goreleaser.yaml`, `README.md`, `Makefile`, `Dockerfile`, `install.sh`, `install.ps1`, `docs/ci-setup.md`, `CLAUDE.md` and ~188 Go files. **This is correct** — the rename has not happened yet.

- [ ] **Step 4: Mark the test as expected-failing until Task 18**

Add immediately after the `func TestNoPersonalAccountReferences(t *testing.T) {` line:

```go
	// TODO(task-18): remove this skip in the module-rename commit, which is what
	// makes this test pass. It is committed ahead of that change deliberately, so
	// the rename has a test that fails first.
	t.Skip("unskipped by the rookery-ai module rename — see Task 18")
```

- [ ] **Step 5: Verify the suite is green with the skip**

```bash
make ci-test
```

Expected: PASS, with `TestNoPersonalAccountReferences` reported as skipped.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "test: guard against personal-account references

The rookery-ai rename touches 188 Go files, a cosign certificate-identity
regexp, both install scripts, the container image path and the website — and
none of those fail visibly when stale. A wrong cosign identity yields a fully
green pipeline and an unverifiable release.

Committed ahead of the rename and skipped, so the rename has a test that fails
first. The skip is removed in the rename commit itself."
```

---

### Task 10: Commit-message hook and shared patterns

**Files:**
- Create: `.githooks/commit-msg`, `.githooks/patterns.txt`, `.githooks/README.md`
- Modify: `Makefile` (add `hooks` target)

**Interfaces:**
- Produces: `.githooks/patterns.txt`, consumed verbatim by Task 11's CI job via `grep -Ef`.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b ci/commit-message-and-pattern-guard
```

- [ ] **Step 2: Write `.githooks/patterns.txt`**

```
[Aa][Pp][Ii][_-]?[Kk][Ee][Yy][[:space:]]*[:=][[:space:]]*[^[:space:]<{$]
[Pp][Aa][Ss][Ss][Ww][Oo][Rr][Dd][[:space:]]*[:=][[:space:]]*[^[:space:]<{$]
[Ss][Ee][Cc][Rr][Ee][Tt][[:space:]]*[:=][[:space:]]*[^[:space:]<{$]
sk-[A-Za-z0-9_-]{16,}
gh[pousr]_[A-Za-z0-9]{20,}
xox[baprs]-[A-Za-z0-9-]{10,}
AKIA[0-9A-Z]{16}
AIza[0-9A-Za-z_-]{20,}
-----BEGIN [A-Z ]*PRIVATE KEY-----
[0-9]{8,10}:[A-Za-z0-9_-]{30,}
/home/[a-z][a-z0-9_-]*
/Users/[a-z][a-z0-9_-]*
[a-z0-9-]+\.lan\b
192\.168\.[0-9]{1,3}\.[0-9]{1,3}
10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}
172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}
100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.[0-9]{1,3}\.[0-9]{1,3}
```

These match **commit messages and PR descriptions only**, never file content — file content is covered by gitleaks and GitHub push protection. That is why RFC1918 literals can be banned here without breaking the connector YAML examples, which are files.

- [ ] **Step 3: Write `.githooks/commit-msg`**

```sh
#!/bin/sh
# commit-msg — rejects credentials, local-environment leakage, and
# non-conventional messages.
#
# Pure grep -E with NO external dependency, deliberately. gitleaks is not
# installed on most machines, and a hook that shells out to a missing binary
# silently succeeds — the same failure CLAUDE.md records for the gitleaks
# [[allowlists]] syntax: a check that loads, does nothing, and reports success.
#
# GitHub's push protection scans file CONTENT only. It never reads a commit
# message. This hook and its pr.yml counterpart cover what GitHub does not.
#
# Install with: make hooks
set -eu

msg_file="$1"
hook_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
patterns="$hook_dir/patterns.txt"

# Comment lines are stripped by git after this hook runs, so strip them here too
# or a scissors-line diff would be scanned as though it were the message.
body=$(grep -v '^#' "$msg_file" || true)

# A merge or revert commit is generated by git; do not lint its shape.
case "$body" in
  "Merge "*|"Revert "*) exit 0 ;;
esac

if [ -f "$patterns" ]; then
  if printf '%s\n' "$body" | grep -nEf "$patterns"; then
    echo >&2
    echo "commit-msg: the lines above look like a credential or a local-environment path." >&2
    echo "Commit messages are permanently public and GitHub's push protection never reads them." >&2
    echo "Rewrite the message. If this is a false positive, quote the value or describe it." >&2
    exit 1
  fi
fi

subject=$(printf '%s\n' "$body" | sed '/^[[:space:]]*$/d' | head -n 1)
if ! printf '%s' "$subject" | grep -qE '^(feat|fix|refactor|docs|test|chore|perf|build|ci)(\([a-z0-9/_-]+\))?!?: .+'; then
  echo "commit-msg: not a Conventional Commit:" >&2
  echo "  $subject" >&2
  echo >&2
  echo "Expected: type(scope): summary" >&2
  echo "Types: feat fix refactor docs test chore perf build ci" >&2
  exit 1
fi
```

- [ ] **Step 4: Write `.githooks/README.md`**

```markdown
# Git hooks

Installed with `make hooks`, which points `core.hooksPath` here. Git does not
share hooks through a clone, so this is opt-in per checkout — `CONTRIBUTING.md`
names it in the setup block.

`patterns.txt` is read by BOTH `commit-msg` and the `pr-description` job in
`.github/workflows/pr.yml`, so local and CI enforcement cannot drift apart.
Add a pattern here and both surfaces gain it.

The patterns apply to **commit messages and PR descriptions only**, never to
file content. File content is covered by gitleaks and GitHub push protection.
That separation is why RFC1918 literals can be banned here without breaking the
connector YAML examples, which document self-hosted deployments and are files.
```

- [ ] **Step 5: Make the hook executable and add the Makefile target**

```bash
chmod +x .githooks/commit-msg
```

Add to `Makefile`, immediately before the `docs-sync-check` target:

```makefile
## hooks: install the committed git hooks (commit-msg credential + conventional-commit guard)
hooks:
	git config core.hooksPath .githooks
	@echo "core.hooksPath -> .githooks"
```

Add `hooks` to the `.PHONY` list if the Makefile declares one.

- [ ] **Step 6: Test the hook rejects a credential**

```bash
make hooks
printf 'chore: test\n\nAPI_KEY=abcd1234efgh5678\n' > /tmp/m.txt
.githooks/commit-msg /tmp/m.txt; echo "exit=$?"
```

Expected: the offending line printed, and `exit=1`.

- [ ] **Step 7: Test it rejects a home path and a non-conventional subject**

```bash
printf 'chore: test\n\nran it from /home/rookie/rookery\n' > /tmp/m2.txt
.githooks/commit-msg /tmp/m2.txt; echo "exit=$?"

printf 'updated some stuff\n' > /tmp/m3.txt
.githooks/commit-msg /tmp/m3.txt; echo "exit=$?"
```

Expected: `exit=1` for both.

- [ ] **Step 8: Test it accepts a good message**

```bash
printf 'feat(connectors): add the Pushover provider\n\nVerified against the live API.\n' > /tmp/m4.txt
.githooks/commit-msg /tmp/m4.txt; echo "exit=$?"
```

Expected: no output, `exit=0`.

- [ ] **Step 9: Test the merge-commit exemption**

```bash
printf 'Merge branch main into feat/x\n' > /tmp/m5.txt
.githooks/commit-msg /tmp/m5.txt; echo "exit=$?"
```

Expected: `exit=0`.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "ci: add a commit-message credential and conventional-commit hook

GitHub push protection scans file content only — it never reads a commit
message or a PR description. This hook plus the pr.yml job added next cover
the two surfaces GitHub does not.

Pure grep -E with no gitleaks dependency, deliberately: gitleaks is not
installed on most machines and a hook shelling out to a missing binary
silently succeeds, which is the failure CLAUDE.md already records for the
gitleaks [[allowlists]] syntax.

patterns.txt is read by the hook AND the CI job, so the two cannot drift."
```

---

### Task 11: CI jobs for PR descriptions and branch names

**Files:**
- Modify: `.github/workflows/pr.yml:1-15` (the `on:` block) and the `jobs:` map

**Interfaces:**
- Consumes: `.githooks/patterns.txt` from Task 10.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull
git checkout -b ci/branch-name-and-pr-description-checks
```

- [ ] **Step 2: Extend the trigger so an edited description is re-scanned**

In `.github/workflows/pr.yml`, change:

```yaml
on:
  pull_request:
    branches: [main]
```

to:

```yaml
on:
  pull_request:
    branches: [main]
    # `edited` is load-bearing: without it a description that passed on open can
    # be edited to add a credential afterwards and never be scanned again.
    types: [opened, edited, reopened, synchronize]
```

Naming `types` replaces the default `[opened, synchronize, reopened]`, so all three are listed explicitly.

- [ ] **Step 3: Add both jobs after the `commit-lint` job**

```yaml
  branch-name:
    name: Branch name
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      # Bot branches are exempt because neither release-please nor Dependabot
      # lets you name its branches — without the exemption every bot PR fails
      # permanently.
      - name: Check branch name
        env:
          BRANCH: ${{ github.head_ref }}
        run: |
          case "$BRANCH" in
            release-please--*|dependabot/*)
              echo "bot branch '$BRANCH' — exempt"; exit 0 ;;
          esac
          if printf '%s' "$BRANCH" | grep -qE '^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$'; then
            echo "ok: $BRANCH"
          else
            echo "::error::Branch '$BRANCH' does not match ^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$"
            echo "Examples: feat/oauth-redirect-pinning, fix/slack-socket-reconnect, docs/install-notes"
            exit 1
          fi

  pr-description:
    name: PR description scan
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v5
      # The same patterns.txt the local commit-msg hook uses, so the two cannot
      # drift. GitHub's push protection scans file content only and never reads
      # a PR description.
      - name: Scan the PR description
        env:
          BODY: ${{ github.event.pull_request.body }}
        run: |
          printf '%s\n' "$BODY" > /tmp/pr-body.txt
          if grep -nEf .githooks/patterns.txt /tmp/pr-body.txt; then
            echo "::error::The PR description contains what looks like a credential or a local-environment path."
            echo "Edit the description. Note that GitHub retains and displays edit history, so a real credential must be ROTATED, not merely removed."
            exit 1
          fi
          echo "ok"
```

`BODY` is passed through the environment rather than interpolated into the `run:` script, so a description containing backticks or `$(...)` cannot execute as shell.

- [ ] **Step 4: Verify the YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/pr.yml')); print('ok')"
```

Expected: `ok`.

- [ ] **Step 5: Verify the branch regex against real cases**

```bash
for b in feat/oauth-redirect-pinning fix/slack-socket-reconnect ci/branch-name-and-pr-description-checks \
         release-please--branches--main--components--rookery dependabot/go_modules/x \
         Feature/Thing main patch-1; do
  case "$b" in release-please--*|dependabot/*) echo "EXEMPT $b"; continue ;; esac
  if printf '%s' "$b" | grep -qE '^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$'
    then echo "PASS   $b"; else echo "FAIL   $b"; fi
done
```

Expected: `PASS` for the first three, `EXEMPT` for the two bot branches, `FAIL` for `Feature/Thing`, `main` and `patch-1`.

- [ ] **Step 6: Verify the description scan catches a planted credential**

```bash
printf 'Deployed and serving at https://agents.rookie.lan\n' > /tmp/pr-body.txt
grep -nEf .githooks/patterns.txt /tmp/pr-body.txt; echo "exit=$?"
```

Expected: the line printed, `exit=0` from grep (a match), which the job turns into a failure.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "ci: check branch names and scan PR descriptions

Two surfaces GitHub's push protection does not cover — it reads file content
only. The pull_request trigger gains `edited` because a description that
passed on open can be edited afterwards and would otherwise never be
rescanned.

Bot branches are exempted as a requirement rather than a preference: neither
release-please nor Dependabot lets you name its branches.

The description is passed via env rather than interpolated into the run
script, so backticks in a PR body cannot execute."
```

---

### Task 12: Clean the seven PR descriptions and sweep comments

**Files:** none (GitHub API only)

- [ ] **Step 1: Re-confirm which PRs are affected**

```bash
gh pr list --repo ilijad1/rookery --state all --limit 300 --json number,title,body > /tmp/prs.json
python3 - <<'PY'
import json, re
pats = {
 'lan': r'[a-z0-9-]+\.lan\b',
 'home': r'/home/[a-z][a-z0-9_-]*',
 'rfc1918': r'\b(192\.168\.|10\.89\.|100\.116\.)[0-9.]+',
}
for pr in json.load(open('/tmp/prs.json')):
    body = pr.get('body') or ''
    hits = {k: len(re.findall(p, body)) for k, p in pats.items() if re.findall(p, body)}
    if hits:
        print(pr['number'], hits, pr['title'][:60])
PY
```

Expected: PRs 16, 20, 21, 56, 92, 117, 137.

- [ ] **Step 2: Edit each description**

For each, `gh pr view <n> --json body -q .body`, replace the offending value with the Task 6 substitution (`agents.rookie.lan` → `rookery.example.com`, `/home/rookie` → `/home/user`, `192.168.1.194` → `192.168.1.50`), and write it back:

```bash
gh pr view 21 --repo ilijad1/rookery --json body -q .body > /tmp/b.md
sed -i -e 's#agents\.rookie\.lan#rookery.example.com#g' \
       -e 's#/home/rookie#/home/user#g' \
       -e 's#192\.168\.1\.194#192.168.1.50#g' /tmp/b.md
gh pr edit 21 --repo ilijad1/rookery --body-file /tmp/b.md
```

This is **tidying, not a security control** — GitHub retains and displays edit history. It is acceptable only because nothing found is a credential.

- [ ] **Step 3: Sweep review and issue comments, which the body scan missed**

```bash
gh api --paginate repos/ilijad1/rookery/pulls/comments --jq '.[] | "\(.pull_request_url|split("/")|last) \(.id) \(.body)"' > /tmp/review-comments.txt
gh api --paginate repos/ilijad1/rookery/issues/comments --jq '.[] | "\(.issue_url|split("/")|last) \(.id) \(.body)"' > /tmp/issue-comments.txt
grep -nEf .githooks/patterns.txt /tmp/review-comments.txt /tmp/issue-comments.txt || echo "clean"
```

- [ ] **Step 4: Edit any comment that matched**

```bash
gh api -X PATCH repos/ilijad1/rookery/pulls/comments/<id> -f body='<cleaned>'
gh api -X PATCH repos/ilijad1/rookery/issues/comments/<id> -f body='<cleaned>'
```

- [ ] **Step 5: Repeat both sweeps for `rookery-web`**

```bash
gh api --paginate repos/ilijad1/rookery-web/pulls/comments --jq '.[].body' > /tmp/web-rc.txt
gh api --paginate repos/ilijad1/rookery-web/issues/comments --jq '.[].body' > /tmp/web-ic.txt
grep -nEf .githooks/patterns.txt /tmp/web-rc.txt /tmp/web-ic.txt || echo "clean"
```

- [ ] **Step 6: Verify the original scan is now clean**

Re-run Step 1. Expected: **no output**.

---

### Task 13: `rookery-web` — community files, CI, and release-please

**Files (in `~/rookery-web`):**
- Create: `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `.github/workflows/pr.yml`, `.github/workflows/release-please.yml`, `.github/workflows/release.yml`, `release-please-config.json`, `.release-please-manifest.json`, `.githooks/commit-msg`, `.githooks/patterns.txt`
- Modify: `package.json` (add a `hooks` script)

**Interfaces:**
- Consumes: the `commit-msg` hook and `patterns.txt` from Task 10, copied verbatim.
- Produces: a `release.yml` attaching `dist.tar.gz` as a release asset, which deployment scripts fetch by version.

- [ ] **Step 1: Create the branch**

```bash
cd ~/rookery-web
git checkout main && git pull
git checkout -b ci/public-repo-readiness
```

- [ ] **Step 2: Add the licence and community files**

Copy `LICENSE` (Apache-2.0) from the product repository verbatim. Write `SECURITY.md` and `CODE_OF_CONDUCT.md` mirroring Task 5's, with the scope section replaced by:

```markdown
## Scope

This repository is the Rookery website — a static Astro site with no server, no
database and no user accounts. The interesting reports here are content
injection in the built output, a dependency vulnerability reaching the built
bundle, or a `_redirects` rule that could be abused to redirect a user somewhere
unintended.

Vulnerabilities in the Rookery product itself belong in
[rookery-ai/rookery](https://github.com/rookery-ai/rookery/security/advisories/new).
```

Write `CONTRIBUTING.md`:

```markdown
# Contributing to the Rookery website

This repository is the [rookery.cloud](https://rookery.cloud) landing page and
documentation site. It is an Astro + Starlight static site and is **separate from
the product** — code and issues for Rookery itself belong in
[rookery-ai/rookery](https://github.com/rookery-ai/rookery).

## Getting set up

```bash
npm ci
npm run hooks     # installs the commit-msg hook
npm run dev       # local dev server
npm run check     # astro check — typecheck and content validation
npm run build     # production build into dist/
```

Run `npm run check && npm run build` before opening a PR; CI runs both.

## Branching

Always branch off `main`. Branch names must match:

```
^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$
```

Bot branches (`release-please--*`, `dependabot/*`) are exempt.

## Commits and pull request titles

Every commit message and PR title must be a
[Conventional Commit](https://www.conventionalcommits.org/): `type(scope): summary`.
The PR title matters most — merges are squashes, so it becomes the commit that
lands on `main` and the input release-please reads to compute the next version.

## Versioning

release-please maintains a release PR on `main`. Merging it tags the repository,
which builds the site and attaches `dist` as a release asset — deployments target
a released **version**, not a branch.

## Content rules

- **No third-party requests.** Search is Pagefind, built statically at build
  time; fonts are vendored. Do not add a CDN, an analytics script or an embed.
- **The Rookery mark is inlined SVG, never an `<img>`.** An image cannot inherit
  `currentColor`, which is exactly how the mark once painted black and vanished
  on the dark theme. See `src/overrides/SiteTitle.astro`.
- **The install scripts have exactly one copy**, in the product repository.
  `public/_redirects` serves them by redirect; never vendor a copy here, or the
  two silently drift.

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
```

- [ ] **Step 3: Copy the hooks**

```bash
mkdir -p .githooks
cp ~/rookery/.githooks/commit-msg ~/rookery/.githooks/patterns.txt .githooks/
chmod +x .githooks/commit-msg
```

Add to `package.json` `scripts`:

```json
    "hooks": "git config core.hooksPath .githooks"
```

- [ ] **Step 4: Write `.github/workflows/pr.yml`**

```yaml
name: PR

on:
  pull_request:
    branches: [main]
    types: [opened, edited, reopened, synchronize]

concurrency:
  group: pr-${{ github.event.pull_request.number }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  commit-lint:
    name: Conventional commit title
    runs-on: ubuntu-latest
    timeout-minutes: 5
    permissions:
      pull-requests: read
    steps:
      # The PR TITLE is linted rather than individual commits because merges are
      # squashes: the title becomes the commit that lands on main and the input
      # release-please reads to compute the next version.
      - uses: amannn/action-semantic-pull-request@v6
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        with:
          types: |
            feat
            fix
            refactor
            docs
            test
            chore
            perf
            build
            ci
          requireScope: false

  branch-name:
    name: Branch name
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - name: Check branch name
        env:
          BRANCH: ${{ github.head_ref }}
        run: |
          case "$BRANCH" in
            release-please--*|dependabot/*)
              echo "bot branch '$BRANCH' — exempt"; exit 0 ;;
          esac
          if printf '%s' "$BRANCH" | grep -qE '^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$'; then
            echo "ok: $BRANCH"
          else
            echo "::error::Branch '$BRANCH' does not match ^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$"
            exit 1
          fi

  pr-description:
    name: PR description scan
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v5
      - name: Scan the PR description
        env:
          BODY: ${{ github.event.pull_request.body }}
        run: |
          printf '%s\n' "$BODY" > /tmp/pr-body.txt
          if grep -nEf .githooks/patterns.txt /tmp/pr-body.txt; then
            echo "::error::The PR description contains what looks like a credential or a local-environment path."
            exit 1
          fi
          echo "ok"

  build:
    name: Build
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-node@v7
        with:
          node-version-file: .nvmrc
          cache: npm
      - run: npm ci
      - name: Typecheck
        run: npm run check
      - name: Build
        run: npm run build
```

- [ ] **Step 5: Add `.nvmrc` if absent**

```bash
node --version | sed 's/^v//' > .nvmrc
cat .nvmrc
```

If `.nvmrc` already exists, leave it and skip this step.

- [ ] **Step 6: Write `release-please-config.json`**

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "packages": {
    ".": {
      "release-type": "simple",
      "package-name": "rookery-web",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "bump-patch-for-minor-pre-major": false,
      "include-component-in-tag": false,
      "include-v-in-tag": true,
      "changelog-sections": [
        { "type": "feat", "section": "Features" },
        { "type": "fix", "section": "Bug Fixes" },
        { "type": "perf", "section": "Performance" },
        { "type": "refactor", "section": "Refactoring" },
        { "type": "docs", "section": "Documentation" },
        { "type": "build", "section": "Build" },
        { "type": "ci", "section": "CI", "hidden": true },
        { "type": "test", "section": "Tests", "hidden": true },
        { "type": "chore", "section": "Chores", "hidden": true }
      ]
    }
  }
}
```

`release-type` is `simple` rather than `node`: the package is `"private": true` and is never published to npm, so there is no `package.json` version to bump.

- [ ] **Step 7: Write `.release-please-manifest.json`**

```json
{
  ".": "0.0.0"
}
```

- [ ] **Step 8: Write `.github/workflows/release-please.yml`**

```yaml
name: release-please

# Maintains a release pull request on main, accumulating Conventional Commits
# and computing the next semantic version. Merging that PR creates the tag,
# which fires release.yml.
on:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  release-please:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      # An organization-owned GitHub App rather than GITHUB_TOKEN: a pull request
      # opened with GITHUB_TOKEN does not trigger other workflows, so merging the
      # release PR would create a tag that release.yml never sees and no assets
      # would be produced. An App rather than a PAT because GitHub has no
      # org-owned PAT — every PAT belongs to a user account.
      - uses: actions/create-github-app-token@v2
        id: app-token
        with:
          app-id: ${{ secrets.ROOKERY_APP_ID }}
          private-key: ${{ secrets.ROOKERY_APP_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json
```

- [ ] **Step 9: Write `.github/workflows/release.yml`**

```yaml
name: release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-node@v7
        with:
          node-version-file: .nvmrc
          cache: npm
      - run: npm ci
      - run: npm run build

      # The built site is attached to the release so a deploy targets a VERSION
      # rather than checking out a branch. Deterministic flags (sorted names,
      # zeroed mtimes, fixed owner) keep the archive byte-identical for an
      # identical build.
      - name: Package the built site
        run: |
          tar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
              -czf "rookery-web-${GITHUB_REF_NAME}.tar.gz" -C dist .
          sha256sum "rookery-web-${GITHUB_REF_NAME}.tar.gz" > checksums.txt

      - name: Attach to the release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh release upload "$GITHUB_REF_NAME" \
            "rookery-web-${GITHUB_REF_NAME}.tar.gz" checksums.txt --clobber
```

- [ ] **Step 10: Verify all three workflows parse**

```bash
python3 -c "import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('.github/workflows/*.yml')]; print('ok')"
```

Expected: `ok`.

- [ ] **Step 11: Verify the build works locally**

```bash
npm ci && npm run check && npm run build
ls dist | head
```

Expected: `check` and `build` succeed, `dist/` is populated.

- [ ] **Step 12: Commit**

```bash
git add -A
git commit -m "ci: make rookery-web ready for a public repository

The repository had no CI at all, so nothing prevented a broken build from
merging. Adds the build gate, the conventional-commit title check, the branch
name check and the PR description scan, plus Apache-2.0 and the community
health files.

release-please uses release-type simple rather than node: the package is
private and never published to npm, so there is no version field to bump. The
release workflow attaches the built dist as a tarball so a deploy targets a
version rather than a branch."
```

---

### Task 14: Full-history secret scan

**Files:** none

- [ ] **Step 1: Scan the complete history of `rookery`**

```bash
podman run --rm -v "$PWD":/repo:z ghcr.io/gitleaks/gitleaks:latest \
  detect --source=/repo --config=/repo/.gitleaks.toml \
  --redact --report-format=json --report-path=/repo/gitleaks-report.json --log-opts="--all"
```

Use `docker` if podman is unavailable. `--log-opts="--all"` scans every ref, not just `HEAD`.

- [ ] **Step 2: Read the report**

```bash
python3 -c "import json;d=json.load(open('gitleaks-report.json'));print(len(d),'findings');[print(f['RuleID'],f['File'],f['Commit'][:8]) for f in d[:40]]"
```

Expected: `0 findings`. Any finding must be triaged individually — a **real** credential means **rotate it**, not merely remove it, because the commit is permanent.

- [ ] **Step 3: Remove the report so it is never committed**

```bash
rm -f gitleaks-report.json
git status --short
```

Expected: clean.

- [ ] **Step 4: Repeat for `rookery-web`**

```bash
cd ~/rookery-web
podman run --rm -v "$PWD":/repo:z ghcr.io/gitleaks/gitleaks:latest \
  detect --source=/repo --redact --report-format=json \
  --report-path=/tmp/web-gitleaks.json --log-opts="--all"
python3 -c "import json;print(len(json.load(open('/tmp/web-gitleaks.json'))),'findings')"
```

Expected: `0 findings`.

---

### Task 15: Documentation sync for Phase A

**Files:**
- Modify: `CLAUDE.md` (the `cmd/livecheck` claim at ~line 1751, the CI/CD release-history section), `README.md`, and whatever the `docs-sync` skill identifies

- [ ] **Step 1: Create the branch**

```bash
cd ~/rookery
git checkout main && git pull
git checkout -b docs/sync-after-phase-a
```

- [ ] **Step 2: Run the docs-sync skill**

Invoke the `docs-sync` skill. It holds the change-to-page trigger map and the cross-repository procedure.

- [ ] **Step 3: Fix the known-stale `cmd/livecheck` claim**

`CLAUDE.md` describes the harness as "uncommitted" when `cmd/livecheck/main.go` and `cmd/livecheck/README.md` are both tracked. Change:

```
A dev harness for this lives at `cmd/livecheck` (uncommitted; runs `connectors.Execute` against real stored tokens).
```

to:

```
A dev harness for this lives at `cmd/livecheck` (tracked; run with `go run ./cmd/livecheck <provider> <action> '<json-args>'` against real stored tokens).
```

- [ ] **Step 4: Update the documented artifact list**

`CLAUDE.md`'s CI/CD section describes a release history that no longer exists. Add, after the versioning paragraph:

```
Versioning restarted at **v0.1.0** when the project moved to the `rookery-ai`
organization: every release, tag and container image built under the previous
personal account was deleted rather than transferred, so nothing shippable
predates the organization.
```

- [ ] **Step 5: Run the checker**

```bash
make docs-sync-check
```

Expected: PASS. If it fails on a provider count or a variable name, fix the documentation against the source — never against another document.

- [ ] **Step 6: Run the selftest, which proves the checker can fail**

```bash
make docs-sync-selftest
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "docs: sync the documentation after the Phase A teardown

CLAUDE.md described cmd/livecheck as uncommitted when both its files are
tracked, and described a release history that no longer exists after every
release and tag was deleted ahead of the organization move."
```

---

## Phase B — Transfer, rename, and organization setup

### Task 16: Clear in-flight Dependabot PRs

**Files:** none

- [ ] **Step 1: List them with their check status**

```bash
for n in 149 150 151 152; do
  echo "--- PR #$n"
  gh pr view "$n" --repo ilijad1/rookery --json title,mergeable,statusCheckRollup \
    --jq '.title, .mergeable, ([.statusCheckRollup[]?.conclusion] | unique)'
done
```

- [ ] **Step 2: Merge the green ones**

```bash
gh pr merge <n> --repo ilijad1/rookery --squash --delete-branch
```

- [ ] **Step 3: Close the rest**

```bash
gh pr close <n> --repo ilijad1/rookery \
  --comment "Closing ahead of the transfer to the rookery-ai organization. Dependabot will re-raise this against the new repository."
```

- [ ] **Step 4: Verify nothing is open**

```bash
gh pr list --repo ilijad1/rookery --state open
gh pr list --repo ilijad1/rookery-web --state open
```

Expected: no output from either.

---

### Task 17: Transfer both repositories

**Files:** none

- [ ] **Step 1: Confirm the pre-transfer state is clean**

```bash
gh release list --repo ilijad1/rookery --limit 5
git ls-remote --tags origin
gh pr list --repo ilijad1/rookery --state open
```

Expected: all three empty. **Do not transfer** if a release or tag survives — Task 1 was incomplete.

- [ ] **Step 2: Transfer the product repository**

```bash
gh api -X POST repos/ilijad1/rookery/transfer -f new_owner=rookery-ai
```

- [ ] **Step 3: Transfer the website repository**

```bash
gh api -X POST repos/ilijad1/rookery-web/transfer -f new_owner=rookery-ai
```

- [ ] **Step 4: Verify both moved and are still PRIVATE**

```bash
gh repo view rookery-ai/rookery --json nameWithOwner,isPrivate
gh repo view rookery-ai/rookery-web --json nameWithOwner,isPrivate
```

Expected: `rookery-ai/rookery` and `rookery-ai/rookery-web`, both `"isPrivate": true`.

- [ ] **Step 5: Update the local remotes in BOTH checkouts**

```bash
cd ~/rookery       && git remote set-url origin git@github.com:rookery-ai/rookery.git       && git remote -v
cd ~/rookery-web   && git remote set-url origin git@github.com:rookery-ai/rookery-web.git   && git remote -v
```

- [ ] **Step 6: Confirm fetch works against the new remote**

```bash
cd ~/rookery && git fetch origin && git status -sb
```

Expected: fetch succeeds, branch tracks the new origin.

---

### Task 18: Rename `ilijad1` to `rookery-ai`

**Files:**
- Modify: `go.mod:1`, 188 `.go` files, `.goreleaser.yaml:43,121`, `Makefile`, `Dockerfile`, `README.md:8,64`, `install.sh`, `install.ps1`, `docs/ci-setup.md`, `cmd/rookery/*.go`, `internal/brandcheck/owner_test.go` (remove the skip)

**Interfaces:**
- Consumes: `TestNoPersonalAccountReferences` from Task 9, currently skipped.
- Produces: a module at `github.com/rookery-ai/rookery` and a guard test that now passes unskipped.

- [ ] **Step 1: Create the branch**

```bash
cd ~/rookery
git checkout main && git pull
git checkout -b refactor/rename-module-to-rookery-ai
```

- [ ] **Step 2: Remove the skip so the test fails first**

Delete these three lines from `internal/brandcheck/owner_test.go`:

```go
	// TODO(task-18): remove this skip in the module-rename commit, which is what
	// makes this test pass. It is committed ahead of that change deliberately, so
	// the rename has a test that fails first.
	t.Skip("unskipped by the rookery-ai module rename — see Task 18")
```

- [ ] **Step 3: Run it to verify it fails**

```bash
go test ./internal/brandcheck/ -run TestNoPersonalAccountReferences -v
```

Expected: FAIL listing ~200 files.

- [ ] **Step 4: Rewrite the module path**

```bash
go mod edit -module github.com/rookery-ai/rookery
grep -rl 'github.com/ilijad1/rookery' --include='*.go' . | xargs sed -i 's#github.com/ilijad1/rookery#github.com/rookery-ai/rookery#g'
gofmt -l .
```

Expected: `gofmt -l .` produces no output.

- [ ] **Step 5: Rewrite the non-Go references**

```bash
grep -rIl 'ilijad1' . --exclude-dir=.git --exclude-dir=docs --exclude-dir=node_modules \
  | xargs sed -i 's#ilijad1#rookery-ai#g'
```

- [ ] **Step 6: Verify the four load-bearing sites individually**

```bash
grep -n 'certificate-identity-regexp' .goreleaser.yaml
grep -n 'homepage:' .goreleaser.yaml
grep -n 'ghcr.io' README.md
grep -n 'rookery-ai' install.sh install.ps1 | head
```

Expected: the cosign regexp reads `'https://github\.com/rookery-ai/rookery/.*'`. A stale value here produces a **fully green pipeline and an unverifiable release** — nothing else catches it.

- [ ] **Step 7: Verify the guard test now passes**

```bash
go test ./internal/brandcheck/ -run TestNoPersonalAccountReferences -v
```

Expected: PASS.

- [ ] **Step 8: Build and run the full gate**

```bash
go build ./...
make ci
```

Expected: both PASS.

- [ ] **Step 9: Confirm the binary still works**

```bash
go build -o /tmp/rookery-rename-check ./cmd/rookery
/tmp/rookery-rename-check --help | head -20
rm /tmp/rookery-rename-check
```

Expected: the CLI help renders.

- [ ] **Step 10: Rename in `rookery-web` too**

```bash
cd ~/rookery-web
git checkout main && git pull
git checkout -b refactor/rename-links-to-rookery-ai
grep -rIl 'ilijad1' . --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=.astro \
  | xargs sed -i 's#ilijad1#rookery-ai#g'
grep -rIn 'ilijad1' . --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=.astro || echo "clean"
npm run build
```

Expected: `clean`, and the build succeeds. Confirm `public/_redirects` now points at `raw.githubusercontent.com/rookery-ai/rookery/main/...` and `src/content/docs/docs/installation/binary.md` carries the new cosign regexp.

- [ ] **Step 11: Run docs-sync**

Invoke the `docs-sync` skill — this change alters install commands and image names across all four surfaces at once.

- [ ] **Step 12: Commit both**

```bash
cd ~/rookery
git add -A
git commit -m "refactor: rename the module and every link to rookery-ai

go.mod plus 188 Go files, and four sites that fail silently when stale: the
cosign certificate-identity regexp (a wrong value produces a green pipeline
and an unverifiable release), the goreleaser homepage, the hardcoded
ghcr.io/ilijad1/rookery in the README, and the raw.githubusercontent
redirects in the website.

Performed AFTER the transfer on purpose: renaming while the repository still
lived under the personal account would verify the module path and the OIDC
identity against a location that did not yet exist.

Unskips TestNoPersonalAccountReferences, which now passes."

cd ~/rookery-web
git add -A
git commit -m "refactor: point every link at the rookery-ai organization"
```

---

### Task 19: Organization identity and per-repository security

**Files:** none

- [ ] **Step 1: Set the organization profile**

```bash
gh api -X PATCH orgs/rookery-ai \
  -f name='Rookery' \
  -f description='Self-hosted AI agents that live on your knowledge base and act through your connected services' \
  -f blog='https://rookery.cloud/'
```

- [ ] **Step 2: Confirm the security defaults are still set**

```bash
gh api orgs/rookery-ai --jq '{secret_scanning: .secret_scanning_enabled_for_new_repositories, push_protection: .secret_scanning_push_protection_enabled_for_new_repositories, dependabot: .dependabot_alerts_enabled_for_new_repositories, dependabot_updates: .dependabot_security_updates_enabled_for_new_repositories, dep_graph: .dependency_graph_enabled_for_new_repositories}'
```

Expected: all five `true` (set during design). If any is `false`, re-apply with `gh api -X PATCH orgs/rookery-ai -F <field>=true`.

- [ ] **Step 3: Enable the same settings ON the two repositories**

An organization default applies to **new** repositories only; a transferred one does not inherit it retroactively.

```bash
for r in rookery rookery-web; do
  gh api -X PATCH "repos/rookery-ai/$r" \
    -F security_and_analysis[secret_scanning][status]=enabled \
    -F security_and_analysis[secret_scanning_push_protection][status]=enabled
done
```

- [ ] **Step 4: Verify per-repository**

```bash
for r in rookery rookery-web; do
  echo "--- $r"
  gh api "repos/rookery-ai/$r" --jq '.security_and_analysis'
done
```

Expected: `secret_scanning` and `secret_scanning_push_protection` both `enabled`. If the API rejects this while the repository is private (Advanced Security gating), record it and re-run immediately after Phase C step 1, when both go public.

- [ ] **Step 5: Create the organization profile repository**

```bash
gh repo create rookery-ai/.github --public \
  --description "Organization-wide community health files and profile"
```

Add `profile/README.md` (the organization landing page shown on `github.com/rookery-ai`) and `SECURITY.md` as the org-wide default. Content mirrors the product's, pointing at both repositories.

---

### Task 20: 🔴 OWNER GATE — create the release GitHub App

**Files:**
- Modify: `.github/workflows/release-please.yml:26-30` (in `rookery`)
- Modify: `docs/ci-setup.md` (§1, rewritten for the App)

**This is the step to hand back to the owner.** GitHub Apps cannot be created through the API — it is the web UI or the app-manifest browser flow.

- [ ] **Step 1: Owner creates the App**

At `https://github.com/organizations/rookery-ai/settings/apps/new`:

| Field | Value |
|---|---|
| Owner | `rookery-ai` |
| GitHub App name | `Rookery Release` (must be globally unique — append a suffix if taken) |
| Homepage URL | `https://rookery.cloud` |
| Webhook → Active | **uncheck** — the App is never called, it only issues tokens |
| Repository permissions → Contents | Read and write |
| Repository permissions → Pull requests | Read and write |
| Where can this GitHub App be installed | Only on this account |

- [ ] **Step 2: Owner installs it on BOTH repositories**

Install page → *Only select repositories* → `rookery` **and** `rookery-web`.

An App created but never installed on one of the two fails the token mint with a permissions error that reads like a bad private key. Installing on both now avoids that.

- [ ] **Step 3: Owner generates a private key and records the App ID**

On the App's settings page: note the **App ID**, then *Generate a private key* — a `.pem` downloads once and is never shown again.

- [ ] **Step 4: Owner stores both as ORGANIZATION secrets**

At `https://github.com/organizations/rookery-ai/settings/secrets/actions`, create two secrets with *Selected repositories* → `rookery`, `rookery-web`:

- `ROOKERY_APP_ID` — the numeric App ID
- `ROOKERY_APP_PRIVATE_KEY` — the entire `.pem` contents including the `-----BEGIN` and `-----END` lines

Paste the key directly into GitHub; do not route it through any other tool.

- [ ] **Step 5: Verify the secrets exist**

```bash
gh api orgs/rookery-ai/actions/secrets --jq '.secrets[].name'
```

Expected: `ROOKERY_APP_ID` and `ROOKERY_APP_PRIVATE_KEY`. This proves they **exist**; it cannot prove the App is installed or the key matches. Only a real run does that.

- [ ] **Step 6: Switch the product workflow to the App**

```bash
cd ~/rookery
git checkout main && git pull
git checkout -b ci/release-please-uses-the-org-app
```

In `.github/workflows/release-please.yml`, replace the single `- uses: googleapis/release-please-action@v4` step with:

```yaml
      # An organization-owned GitHub App rather than a PAT: GitHub has no
      # org-owned PAT — every PAT belongs to a user account, expires on a
      # calendar and dies if that user leaves. The App is owned by the org and
      # mints a short-lived installation token per run.
      #
      # Still not GITHUB_TOKEN, for the original reason: a pull request opened
      # with GITHUB_TOKEN does not trigger other workflows, so merging the
      # release PR would create a tag that release.yml never sees and no
      # artifacts would be produced. See docs/ci-setup.md.
      - uses: actions/create-github-app-token@v2
        id: app-token
        with:
          app-id: ${{ secrets.ROOKERY_APP_ID }}
          private-key: ${{ secrets.ROOKERY_APP_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json
```

- [ ] **Step 7: Rewrite `docs/ci-setup.md` §1**

Replace the `RELEASE_PLEASE_TOKEN` section with an App section carrying the settings table from Step 1, the two organization secret names, and both reasons — why not `GITHUB_TOKEN` (no workflow triggering) and why not a PAT (no org-owned PAT exists).

- [ ] **Step 8: Verify the YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release-please.yml')); print('ok')"
```

Expected: `ok`.

- [ ] **Step 9: Commit and merge**

```bash
git add -A
git commit -m "ci: authenticate release-please as the organization App

GitHub has no org-owned PAT — every PAT belongs to a user account, expires on
a calendar and dies if that user leaves. The App is owned by rookery-ai and
mints a short-lived installation token per run, so no long-lived credential is
stored and release PRs are authored by a bot rather than a person."
```

- [ ] **Step 10: Confirm the App actually works**

Merging Step 9 pushes to `main`, which fires `release-please.yml`.

```bash
gh run list --repo rookery-ai/rookery --workflow=release-please.yml --limit 3
gh pr list --repo rookery-ai/rookery --state open
```

Expected: the run succeeds and a release PR titled `chore(main): release 0.1.0` appears, authored by the App's bot identity. **If the run fails with a permissions error, check the App is installed on this repository before regenerating the key** — that is the more common cause.

- [ ] **Step 11: Delete the superseded secret**

Only now, after the App is proven:

```bash
gh secret delete RELEASE_PLEASE_TOKEN --repo rookery-ai/rookery
gh secret list --repo rookery-ai/rookery
```

Expected: `RELEASE_PLEASE_TOKEN` gone. Deleting it earlier would have left no working path to cut a release if the App needed a second attempt.

---

### Task 21: Post-transfer verification

**Files:** none

- [ ] **Step 1: Workflows registered in both repositories**

```bash
gh workflow list --repo rookery-ai/rookery
gh workflow list --repo rookery-ai/rookery-web
```

Expected: `PR`, `release-please`, `release`, `codeql` in the first; `PR`, `release-please`, `release` in the second.

- [ ] **Step 2: Build green against the new module path**

```bash
cd ~/rookery && go build ./... && make ci
```

Expected: PASS.

- [ ] **Step 3: Documentation checker green**

```bash
make docs-sync-check
```

Expected: PASS.

- [ ] **Step 4: Nothing shippable exists**

```bash
gh release list --repo rookery-ai/rookery --limit 10
git ls-remote --tags origin
gh api user/packages?package_type=container --jq '.[].name' 2>&1
```

Expected: the first two empty; the third either 403 (scope not granted) or no `rookery` entry.

- [ ] **Step 5: Both repositories still private**

```bash
gh repo view rookery-ai/rookery --json isPrivate
gh repo view rookery-ai/rookery-web --json isPrivate
```

Expected: `true` for both. **Phase C has not started yet.**

---

## Phase C — Publication and first release

🔴 **GATE: do not begin Phase C until the owner has reviewed both repositories and explicitly approved publication.** This is the one irreversible step.

### Task 22: Publish and protect

**Files:** none

- [ ] **Step 1: Flip both to public**

```bash
gh repo edit rookery-ai/rookery --visibility public --accept-visibility-change-consequences
gh repo edit rookery-ai/rookery-web --visibility public --accept-visibility-change-consequences
```

- [ ] **Step 2: Verify**

```bash
gh repo view rookery-ai/rookery --json isPrivate,visibility
gh repo view rookery-ai/rookery-web --json isPrivate,visibility
```

Expected: `"isPrivate": false`, `"visibility": "PUBLIC"`.

- [ ] **Step 3: Set the repository descriptions and topics**

```bash
gh repo edit rookery-ai/rookery \
  --description "Self-hosted AI agents that live on your knowledge base and act through your connected services" \
  --homepage "https://rookery.cloud"
gh repo edit rookery-ai/rookery-web \
  --description "The rookery.cloud website — landing page and documentation. Separate from the product binary." \
  --homepage "https://rookery.cloud"
```

The website description previously said `rookery.sh`, which is the wrong domain.

- [ ] **Step 4: Enable branch protection on `rookery`**

Now possible — it is free on public repositories and was returning 403 before.

```bash
gh api -X PUT repos/rookery-ai/rookery/branches/main/protection \
  --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Conventional commit title",
      "Branch name",
      "PR description scan",
      "Go build and test",
      "Frontend",
      "Security scan",
      "Container smoke test",
      "Package smoke test"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
```

`required_pull_request_reviews` is `null` because a solo maintainer cannot approve their own PR — requiring a review would block every merge. `enforce_admins` is `false` for the same reason.

Cross-compile is deliberately omitted from the contexts list: it is a matrix job producing six differently-named checks rather than one.

- [ ] **Step 5: Enable branch protection on `rookery-web`**

```bash
gh api -X PUT repos/rookery-ai/rookery-web/branches/main/protection \
  --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["Conventional commit title", "Branch name", "PR description scan", "Build"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
```

- [ ] **Step 6: Verify protection is live**

```bash
gh api repos/rookery-ai/rookery/branches/main/protection --jq '.required_status_checks.contexts, .allow_force_pushes.enabled'
```

Expected: the contexts list, and `false`.

- [ ] **Step 7: Enable private vulnerability reporting on both**

```bash
gh api -X PUT repos/rookery-ai/rookery/private-vulnerability-reporting
gh api -X PUT repos/rookery-ai/rookery-web/private-vulnerability-reporting
```

- [ ] **Step 8: Re-run the per-repository security settings if they failed in Task 19**

```bash
for r in rookery rookery-web; do
  gh api -X PATCH "repos/rookery-ai/$r" \
    -F security_and_analysis[secret_scanning][status]=enabled \
    -F security_and_analysis[secret_scanning_push_protection][status]=enabled
  gh api "repos/rookery-ai/$r" --jq '.security_and_analysis'
done
```

Expected: both `enabled`.

---

### Task 23: Cut v0.1.0

**Files:** none

- [ ] **Step 1: Confirm the release PR proposes 0.1.0**

```bash
gh pr list --repo rookery-ai/rookery --state open --json number,title
```

Expected: `chore(main): release 0.1.0`. **If it proposes anything else, stop** — the manifest reset in Task 3 did not take, and merging would publish a wrong version.

- [ ] **Step 2: Merge it**

```bash
gh pr merge <n> --repo rookery-ai/rookery --squash
```

- [ ] **Step 3: Watch the release workflow**

```bash
gh run list --repo rookery-ai/rookery --workflow=release.yml --limit 3
gh run watch <run-id> --repo rookery-ai/rookery
```

Expected: success. This publishes six binary archives, `.deb`/`.rpm`, `checksums.txt`, the cosign bundle, an SBOM per archive, and the multi-arch image.

- [ ] **Step 4: Verify the release and its assets**

```bash
gh release view v0.1.0 --repo rookery-ai/rookery --json tagName,assets --jq '.tagName, (.assets|length), [.assets[].name]'
```

Expected: `v0.1.0` and a populated asset list including `checksums.txt`.

---

### Task 24: Verify the published artifacts

**Files:** none

These checks cannot run before publication: release assets on a private repository require an authenticated request, so an anonymous download returns **404, not 401** — which is why both installers name that case first in their failure text.

- [ ] **Step 1: Run `install.sh` end to end, unauthenticated**

```bash
env -u GITHUB_TOKEN -u GH_TOKEN sh -c '
  curl -fsSL https://raw.githubusercontent.com/rookery-ai/rookery/main/install.sh -o /tmp/install.sh
  sh /tmp/install.sh
'
```

Expected: it detects the platform, downloads the archive, **verifies it against `checksums.txt`**, installs the binary, offers the four host tools, and hands off to `rookery onboard`.

- [ ] **Step 2: Confirm the installed binary runs**

```bash
rookery version
rookery healthcheck || true
```

Expected: the version prints `0.1.0`.

- [ ] **Step 3: Pull the image anonymously**

```bash
podman logout ghcr.io 2>/dev/null || true
podman pull ghcr.io/rookery-ai/rookery:v0.1.0
```

Expected: the pull succeeds without credentials. This — not the API listing — is what proves the package is publicly readable.

- [ ] **Step 4: Verify the cosign signature with the NEW identity**

```bash
cd /tmp && gh release download v0.1.0 --repo rookery-ai/rookery --pattern 'checksums.txt*'
cosign verify-blob checksums.txt \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/rookery-ai/rookery/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Expected: `Verified OK`. A failure here means `.goreleaser.yaml:121` still carries the old identity — Task 18 Step 6 missed it.

- [ ] **Step 5: Check `install.ps1` is fetchable**

```bash
curl -fsSI https://raw.githubusercontent.com/rookery-ai/rookery/main/install.ps1 | head -1
```

Expected: `HTTP/2 200`. It cannot be executed here — there is no PowerShell on this host, which `packaging/scripts_test.go` already records as a real gap.

- [ ] **Step 6: Confirm the README commands match reality**

```bash
grep -n 'ghcr.io' README.md
```

Expected: `ghcr.io/rookery-ai/rookery:latest`, matching the image just pulled.

---

### Task 25: Cut `rookery-web` v0.1.0

**Files:** none

- [ ] **Step 1: Confirm its release PR appeared**

```bash
gh pr list --repo rookery-ai/rookery-web --state open --json number,title
```

Expected: `chore(main): release 0.1.0`. If nothing is open, push a `feat:` or `fix:` commit — release-please needs at least one releasable commit since the reset.

- [ ] **Step 2: Merge it**

```bash
gh pr merge <n> --repo rookery-ai/rookery-web --squash
```

- [ ] **Step 3: Verify the tarball attached**

```bash
gh release view v0.1.0 --repo rookery-ai/rookery-web --json tagName,assets --jq '.tagName, [.assets[].name]'
```

Expected: `v0.1.0` with `rookery-web-v0.1.0.tar.gz` and `checksums.txt` — the artifact a deploy script fetches by version.

- [ ] **Step 4: Verify the tarball is a real site build**

```bash
cd /tmp && gh release download v0.1.0 --repo rookery-ai/rookery-web --pattern '*.tar.gz'
mkdir -p /tmp/web-check && tar xzf rookery-web-v0.1.0.tar.gz -C /tmp/web-check
ls /tmp/web-check | head
test -f /tmp/web-check/index.html && echo "index.html present"
```

Expected: `index.html present`.

---

### Task 26: 🔴 OWNER GATE — organization avatar and 2FA

**Files:** none

Neither is reachable from the API.

- [ ] **Step 1: Extract the mark into a standalone SVG**

`RookeryTile` in `web/ui/src/components/brand/RookeryMark.tsx` paints its glyph in the explicit brand cream rather than `currentColor`, because a tile supplies its own background and an inherited foreground would vanish into the fill. That is exactly the form an avatar needs.

Read the component, then write `/tmp/rookery-avatar.svg` as a standalone SVG: copy `RookeryTile`'s `<svg>` subtree, set `width="500" height="500"`, keep its `viewBox`, and replace the React gradient `id` prop with a literal id (the prop exists so two marks on one page do not collide — irrelevant in a standalone file).

```bash
head -60 web/ui/src/components/brand/RookeryMark.tsx
```

- [ ] **Step 2: Rasterise to a 500×500 PNG**

```bash
if command -v rsvg-convert >/dev/null; then
  rsvg-convert -w 500 -h 500 /tmp/rookery-avatar.svg -o ~/rookery-avatar.png
elif command -v magick >/dev/null; then
  magick -background none -density 400 /tmp/rookery-avatar.svg -resize 500x500 ~/rookery-avatar.png
elif command -v inkscape >/dev/null; then
  inkscape /tmp/rookery-avatar.svg -w 500 -h 500 -o ~/rookery-avatar.png
else
  echo "no SVG rasteriser found — install librsvg2-tools, ImageMagick, or Inkscape"
fi
file ~/rookery-avatar.png
```

Expected: `PNG image data, 500 x 500`. GitHub accepts PNG, JPG and GIF, wants a square, and downsamples for display. If no rasteriser is available, hand the owner `/tmp/rookery-avatar.svg` — GitHub does **not** accept SVG, so it must be converted somewhere.

- [ ] **Step 3: Owner uploads it**

`https://github.com/organizations/rookery-ai/settings/profile` → Profile picture → Upload. **GitHub exposes no REST endpoint for an organization avatar** — this cannot be scripted.

- [ ] **Step 4: Owner enables 2FA enforcement**

`https://github.com/organizations/rookery-ai/settings/security` → *Require two-factor authentication*.

**Do not attempt this via the API.** `PATCH /orgs/rookery-ai` with `two_factor_requirement_enabled=true` returns **200 with a full organization body** and the field reads back `false` — it is silently ignored, so a script checking only the HTTP status reports a success that never happened.

- [ ] **Step 5: Verify both landed**

```bash
gh api orgs/rookery-ai --jq '{avatar: .avatar_url, twofa: .two_factor_requirement_enabled, name: .name, description: .description}'
```

Expected: `twofa: true`, and an `avatar_url` whose identifier differs from the default `u/246282041?v=4`.

---

## Rollback

**Phase A** — every change is a PR; revert the merge commit. Deleted releases and tags are **not** recoverable, which is intended.

**Phase B** — a transfer can be reversed by transferring back to `ilijad1`; the module rename is a revertible commit.

**Phase C** — visibility can be flipped back to private, but **a public repository may already have been cloned or forked**. Treat step 1 of Task 22 as the point of no return.
