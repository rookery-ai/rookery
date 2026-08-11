# Backup destination and documentation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the local backup folder setting the hardened service cannot honour, warn once when backups are unconfigured, and rewrite the backup documentation as a runbook filed under Operations.

**Architecture:** `internal/backup` stops storing a local directory at all — a new `DefaultLocalDir(dataDir)` resolves `<data_dir>/backups` for the CLI, the scheduler and the web API alike, and `Config.Local` is deleted so an existing stored value is dropped by `encoding/json`'s unknown-key behaviour with no migration code. The API's `local_dir` flips from an input to a resolved read-only output the UI displays. A one-shot `localStorage`-gated banner on the owner-workspaces section points at the backup settings. The website's backup page moves from Concepts to Operations and is rewritten around three procedures: turn it on, keep a copy elsewhere, restore onto a new machine.

**Tech Stack:** Go 1.x (`internal/backup`, `web`, `cmd/rookery`), React 19 + TypeScript + Tailwind v4 + TanStack Query + react-router (`web/ui`), vitest + Testing Library, Astro/Starlight (`~/rookery-web`).

## Global Constraints

- **Conventional Commits** on every commit: `type(scope): summary`. Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **Never commit to `main`.** All work is on the current branch `worktree-backup-destination-and-docs`; it merges via PR.
- **No new dependencies.** Not in Go, not in the SPA, not on the website.
- **The product repo is a git worktree** at `/home/rookie/rookery/.claude/worktrees/backup-destination-and-docs`. The website repo is a separate checkout at `/home/rookie/rookery-web` on its own branch.
- **`make docs-sync-check` must be run with `ROOKERY_WEB_DIR=/home/rookie/rookery-web`.** From a worktree the resolver otherwise finds no website checkout and **skips every website assertion silently**, which reads as a pass.
- **Tailwind: never a hardcoded pixel font size** (`text-[13px]` fails `density.test.ts`). Use the `text-*` tokens.
- **Every action button carries a leading lucide icon**, `currentColor` only. Dialog footer pairs and `link`-variant buttons are the two carve-outs.
- **The local backup directory is `<data_dir>/backups` everywhere.** Exactly one function computes it: `backup.DefaultLocalDir`.

---

### Task 1: `internal/backup` — one resolved local directory, no stored one

**Files:**
- Modify: `internal/backup/settings.go` (delete `LocalConfig` + `Config.Local`; add `DefaultLocalDir`; change `Validate` and `BuildDestination`)
- Modify: `internal/backup/schedule.go:118` (call site)
- Modify: `web/api_backup.go:168` (call site — compile fix only; the DTO change is Task 2)
- Modify: `cmd/rookery/backup_cmd.go` (`localDestFor` uses the new helper)
- Test: `internal/backup/settings_test.go`, `internal/backup/schedule_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `func DefaultLocalDir(dataDir string) string` — returns `filepath.Join(dataDir, "backups")`.
  - `func (c *Config) BuildDestination(dataDir string, systemKey []byte) (Destination, error)` — note the **new leading `dataDir` parameter**; Tasks 2 uses this signature.
  - `Config` no longer has a `Local` field. `LocalConfig` no longer exists.

- [ ] **Step 1: Write the failing tests**

In `internal/backup/settings_test.go`, replace `TestBuildDestinationLocal` with the following, and add the two new tests after it:

```go
func TestBuildDestinationLocalUsesTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	c := &Config{Destination: DestLocal}
	d, err := c.BuildDestination(dataDir, testKey())
	if err != nil {
		t.Fatalf("BuildDestination: %v", err)
	}
	want := "local:" + filepath.Join(dataDir, "backups")
	if d.Name() != want {
		t.Fatalf("got %q, want %q", d.Name(), want)
	}
}

// A local destination needs no configuration at all now, so an otherwise
// complete config must validate without one.
func TestConfigValidateAcceptsLocalWithNoDirectory(t *testing.T) {
	c := &Config{
		Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily,
		Hour: 3, Retention: 7, EncryptedPassphrase: "x",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("a local destination needs no directory: %v", err)
	}
}

// An install configured before the folder field was removed still has
// {"local":{"dir":"/mnt/backups"}} in its stored config. It must parse, and the
// value must not come back to life. encoding/json ignores unknown keys, so this
// costs no migration code — but it is exactly the kind of thing someone
// "restores" by re-adding the field, so it is pinned.
func TestLoadConfigIgnoresALegacyLocalDirectory(t *testing.T) {
	store := newMemStore()
	raw := `{"enabled":true,"destination":"local","schedule":"daily","hour":3,` +
		`"retention":7,"encrypted_passphrase":"x","local":{"dir":"/mnt/backups"}}`
	if err := store.SetSystemSetting(SettingsKey, raw); err != nil {
		t.Fatal(err)
	}

	c, err := LoadConfig(store, testKey())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Retention != 7 || c.Destination != DestLocal {
		t.Fatalf("the rest of the config must survive: %+v", c)
	}

	dataDir := t.TempDir()
	d, err := c.BuildDestination(dataDir, testKey())
	if err != nil {
		t.Fatalf("BuildDestination: %v", err)
	}
	if strings.Contains(d.Name(), "/mnt/backups") {
		t.Fatalf("the legacy directory must not be honoured: %q", d.Name())
	}
}
```

Then fix `TestConfigValidateRejectsBadValues`: **delete** the `"local with no dir"` case entirely and **remove** every `Local: LocalConfig{Dir: "/tmp/b"}` from the remaining cases, leaving:

```go
func TestConfigValidateRejectsBadValues(t *testing.T) {
	cases := map[string]*Config{
		"enabled with no passphrase": {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 7},
		"s3 with no bucket":          {Enabled: true, Destination: DestS3, Schedule: ScheduleDaily, Retention: 7, EncryptedPassphrase: "x"},
		"hour out of range":          {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Hour: 24, Retention: 7, EncryptedPassphrase: "x"},
		"retention below one":        {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 0, EncryptedPassphrase: "x"},
		"unknown schedule":           {Enabled: true, Destination: DestLocal, Schedule: "hourly", Retention: 7, EncryptedPassphrase: "x"},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
}
```

Fix `TestBuildDestinationS3NeedsSecret`'s call to pass a data dir it will not use:

```go
	if _, err := c.BuildDestination(t.TempDir(), testKey()); err == nil {
```

Ensure `internal/backup/settings_test.go` imports `path/filepath` and `strings`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/ -run 'TestBuildDestination|TestConfigValidate|TestLoadConfigIgnores' -count=1`
Expected: FAIL to **compile** — `c.BuildDestination` takes 1 argument, and `LocalConfig` is still referenced by `schedule_test.go`. A compile failure is the correct red here; the signature does not exist yet.

- [ ] **Step 3: Change `internal/backup/settings.go`**

Delete the `LocalConfig` type (lines 34–37) and the `Local LocalConfig` field from `Config`. Add the helper immediately above `Config`:

```go
// DefaultLocalDir is where local snapshots go: <data_dir>/backups, and nowhere
// else. The directory is deliberately not configurable. The packaged unit runs
// with ProtectSystem=strict and ReadWritePaths=<data_dir>, and the container
// mounts one volume at /data, so any other path fails at 03:00 with a
// permission error rather than at save time with an explanation.
func DefaultLocalDir(dataDir string) string {
	return filepath.Join(dataDir, "backups")
}
```

Add `"path/filepath"` to the imports.

In `Validate`, the local case no longer checks anything:

```go
	switch c.Destination {
	case DestLocal:
		// Nothing to validate: the destination is always <data_dir>/backups.
	case DestS3:
```

Replace `BuildDestination` entirely:

```go
// BuildDestination constructs the configured Destination. dataDir is required
// for the local kind, whose directory is derived rather than stored.
func (c *Config) BuildDestination(dataDir string, systemKey []byte) (Destination, error) {
	switch c.Destination {
	case DestLocal:
		return NewLocalDestination(DefaultLocalDir(dataDir)), nil
	case DestS3:
		secret, err := c.S3SecretKey(systemKey)
		if err != nil {
			return nil, err
		}
		return NewS3Destination(c.S3, secret), nil
	default:
		return nil, fmt.Errorf("backup: unknown destination %q", c.Destination)
	}
}
```

- [ ] **Step 4: Fix the three call sites**

`internal/backup/schedule.go:118`:

```go
	dest, err := c.BuildDestination(s.dataDir, s.systemKey)
```

`web/api_backup.go`, in `backupDestination`:

```go
	return cfg.BuildDestination(s.cfg.Data.Dir, s.systemKey)
```

`cmd/rookery/backup_cmd.go`, in `localDestFor` — the join moves onto the helper so there is one definition of the path:

```go
func localDestFor(cmd *cli.Command, cfg *config.Config) backup.Destination {
	dir := cmd.String("dir")
	if dir == "" {
		dir = backup.DefaultLocalDir(cfg.Data.Dir)
	}
	return backup.NewLocalDestination(dir)
}
```

Remove the now-unused `"path/filepath"` import from `cmd/rookery/backup_cmd.go` **only if** nothing else in that file uses it — check with `grep -n 'filepath\.' cmd/rookery/backup_cmd.go` and leave the import if there are other uses.

Note: `web/api_backup.go:58`'s `LocalDir: c.Local.Dir` and `:119`'s `cfg.Local.Dir = req.Local.Dir` will not compile now. Apply the minimal fix here — delete line 119 entirely, and set line 58 to `LocalDir: backup.DefaultLocalDir(dataDir),`. Task 2 covers the request-struct cleanup and the tests.

- [ ] **Step 5: Fix the scheduler tests**

In `internal/backup/schedule_test.go`:

`TestSchedulerRunOnceWritesSnapshotAndPrunes` (line ~81) — delete `destDir := t.TempDir()` and the `c.Local = LocalConfig{Dir: destDir}` line. Snapshots now land in `<dataDir>/backups`; if the rest of the test reads `destDir`, point it at `DefaultLocalDir(dataDir)` instead.

`TestSchedulerRefusesWithoutPassphrase` (line ~156) — delete the `c.Local = LocalConfig{Dir: t.TempDir()}` line.

`TestSchedulerRunOnceRecordsFailure` (line ~131) — the old test forced a failure with an unwritable directory. With the directory derived, force it by putting a **regular file** where the backups directory must be created, so `MkdirAll` fails with "not a directory":

```go
func TestSchedulerRunOnceRecordsFailure(t *testing.T) {
	dataDir := t.TempDir()
	database, _ := newTestDB(t, dataDir)
	key := testKey()
	store := newMemStore()

	// The destination is derived from the data dir now, so the way to make the
	// write fail is to occupy the path: MkdirAll refuses to create a directory
	// where a regular file already sits.
	if err := os.WriteFile(DefaultLocalDir(dataDir), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := DefaultConfig()
	c.Enabled = true
	c.Destination = DestLocal
	if err := c.SetPassphrase(key, "pw"); err != nil {
		t.Fatal(err)
	}
	SaveConfig(store, key, c)

	if _, err := NewScheduler(store, database, dataDir, key).RunOnce(context.Background()); err == nil {
		t.Fatal("expected the run to fail")
	}
	after, _ := LoadConfig(store, key)
	if after.LastStatus != "error" || after.LastError == "" {
		t.Fatalf("failure must be recorded for the settings banner: %+v", after)
	}
}
```

Ensure `os` is imported in `schedule_test.go`.

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/backup/ ./cmd/... -count=1`
Expected: PASS. If `retention_test.go`, `backup_test.go` or `restore_test.go` fail to compile, they reference `LocalConfig` too — they construct `NewLocalDestination` directly per the earlier survey, but fix any straggler the same way (drop the field; use `DefaultLocalDir`).

- [ ] **Step 7: Verify the whole tree still builds**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add internal/backup cmd/rookery/backup_cmd.go web/api_backup.go
git commit -m "refactor(backup): derive the local destination from the data dir"
```

---

### Task 2: API — `local_dir` becomes a resolved output

**Files:**
- Modify: `web/api_backup.go` (`backupConfigReq`, `toBackupDTO`, comments)
- Test: `web/api_backup_test.go`

**Interfaces:**
- Consumes: `backup.DefaultLocalDir(dataDir)` from Task 1.
- Produces: `GET /api/v1/backup/config` returns `local_dir` as an absolute path that is **always non-empty**. `PUT /api/v1/backup/config` no longer reads `local.dir`.

- [ ] **Step 1: Write the failing test**

Add to `web/api_backup_test.go`:

```go
// local_dir is an output now, not an input: it reports where snapshots actually
// go so the settings page can state it. A client that still posts a directory
// must not be able to move the destination.
func TestBackupConfigReportsResolvedLocalDirAndIgnoresAPostedOne(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	payload := map[string]any{
		"enabled": true, "destination": "local", "schedule": "daily",
		"hour": 3, "weekday": 0, "retention": 7,
		"passphrase": "hunter2",
		"local":      map[string]string{"dir": "/mnt/somewhere-else"},
	}
	rec := doJSON(t, s, http.MethodPut, "/api/v1/backup/config", payload, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/backup/config", nil, cookies)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := backup.DefaultLocalDir(s.cfg.Data.Dir)
	if body["local_dir"] != want {
		t.Fatalf("local_dir = %v, want %v", body["local_dir"], want)
	}
}
```

Ensure `web/api_backup_test.go` imports `github.com/ilijad1/rookery/internal/backup`.

In the same file, simplify `TestBackupSaveConfigStoresPassphraseAndDoesNotEchoIt`: delete the `"local"` key from its payload (and the now-unused `filepath` import if nothing else in the file uses it).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./web/ -run TestBackupConfigReportsResolvedLocalDir -count=1`
Expected: FAIL — `local_dir` is `""` because Task 1's minimal fix set it from `DefaultLocalDir` only if that edit landed; if it did, this test passes already and the remaining work is the request struct. Either way, proceed to Step 3.

- [ ] **Step 3: Clean up the request and DTO**

In `web/api_backup.go`, delete the `Local` block from `backupConfigReq`:

```go
type backupConfigReq struct {
	Enabled     bool   `json:"enabled"`
	Destination string `json:"destination"`
	Schedule    string `json:"schedule"`
	Hour        int    `json:"hour"`
	Weekday     int    `json:"weekday"`
	Retention   int    `json:"retention"`
	Passphrase  string `json:"passphrase"`
	S3          struct {
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Bucket    string `json:"bucket"`
		Prefix    string `json:"prefix"`
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
		PathStyle bool   `json:"path_style"`
	} `json:"s3"`
}
```

Confirm `cfg.Local.Dir = req.Local.Dir` is gone from `handleSaveBackupConfig`, and that `toBackupDTO` reads:

```go
		PassphraseSet: c.EncryptedPassphrase != "",
		// Resolved, not configured: the local directory is derived from the data
		// dir, and this field exists so the settings page can say where snapshots
		// land rather than ask.
		LocalDir: backup.DefaultLocalDir(dataDir),
```

- [ ] **Step 4: Run the web backup tests**

Run: `go test ./web/ -run TestBackup -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/api_backup.go web/api_backup_test.go
git commit -m "refactor(web): report the backup directory instead of accepting one"
```

---

### Task 3: Settings UI — the folder input becomes a statement

**Files:**
- Modify: `web/ui/src/lib/backup.ts` (drop `local` from `SaveBackupConfig`)
- Modify: `web/ui/src/pages/settings/BackupSection.tsx`
- Test: `web/ui/src/pages/settings/BackupSection.test.tsx`

**Interfaces:**
- Consumes: `BackupConfig.local_dir` (always populated) from Task 2.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Write the failing tests**

Add to `web/ui/src/pages/settings/BackupSection.test.tsx`, inside the existing `describe` block:

```tsx
  it("states where snapshots go and offers no folder field", async () => {
    mountWith(mockConfig({ local_dir: "/home/rookie/.rookery/backups" }));
    expect(await screen.findByText("/home/rookie/.rookery/backups")).toBeInTheDocument();
    expect(screen.queryByLabelText(/backup folder/i)).not.toBeInTheDocument();
  });

  it("does not send a local directory when saving", async () => {
    mountWith(mockConfig({ enabled: true, passphrase_set: true }));
    await screen.findByText(/passphrase is set/i);

    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => {
      const calls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls;
      const put = calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
      expect(put).toBeTruthy();
      const body = JSON.parse((put![1] as RequestInit).body as string);
      expect(body.local).toBeUndefined();
    });
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/settings/BackupSection.test.tsx`
Expected: FAIL — the resolved path is not rendered (it is an input's value, not text), and the PUT body still carries `local`.

- [ ] **Step 3: Drop `local` from the save type**

In `web/ui/src/lib/backup.ts`, remove `local: { dir: string };` from `SaveBackupConfig`. Leave `local_dir` on `BackupConfig` and update its comment:

```ts
  passphrase_set: boolean;
  // Resolved by the server, never sent back up: local snapshots always go to
  // <data_dir>/backups.
  local_dir: string;
```

- [ ] **Step 4: Replace the input in `BackupSection.tsx`**

Remove `localDir: string;` from `FormState`, the `localDir: data.local_dir ?? "",` line from the `useEffect` initialiser, and `local: { dir: form.localDir },` from the save payload.

Replace the whole `{form.destination === "local" ? (…) : (…)}` **local branch** — the `<label>` holding the "Backup folder" `Input` — with:

```tsx
        {form.destination === "local" ? (
          <div className="rounded-md bg-chrome px-3 py-2 text-sm">
            <span className="block text-muted-2">Snapshots are written to</span>
            <code className="mt-0.5 block break-all font-mono">
              {data?.local_dir}
            </code>
            <p className="mt-2 text-muted-2">
              A backup on the same disk as the install is not a backup. Download
              each one and keep it somewhere else.
            </p>
          </div>
        ) : (
```

Leave the S3 branch untouched.

- [ ] **Step 5: Add the download hint by the snapshot list**

Find the snapshot list heading in `BackupSection.tsx` (the block rendering `snapshots.data`) and add, directly beneath its heading:

```tsx
        <p className="text-sm text-muted-2">
          Download a snapshot and store it off this machine — a backup that dies
          with the machine it protects is not one.
        </p>
```

- [ ] **Step 6: Run the tests**

Run: `cd web/ui && npx vitest run src/pages/settings/BackupSection.test.tsx`
Expected: PASS. The existing `"does not resend the passphrase when one is already stored"` test passes `local_dir: "/mnt/b"` — it still works, since `local_dir` is now only displayed.

- [ ] **Step 7: Typecheck and lint**

Run: `cd web/ui && npx tsc -b && npx oxlint`
Expected: no errors. If `tsc` reports `data` possibly undefined in the new block, that is correct — the surrounding JSX already renders only once `form` exists, and `data?.local_dir` handles it.

- [ ] **Step 8: Commit**

```bash
git add web/ui/src/lib/backup.ts web/ui/src/pages/settings/BackupSection.tsx web/ui/src/pages/settings/BackupSection.test.tsx
git commit -m "feat(web/settings): state the backup directory instead of asking for one"
```

---

### Task 4: A one-shot warning when backups are not configured

**Files:**
- Create: `web/ui/src/pages/settings/BackupWarningBanner.tsx`
- Create: `web/ui/src/pages/settings/BackupWarningBanner.test.tsx`
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx` (mount it at the top of `WorkspacesSection`)

**Interfaces:**
- Consumes: `useBackupConfig()` from `@/lib/backup` — same query key `["backup", "config"]` as `BackupSection`, so the two share one request.
- Produces: `export function BackupWarningBanner(): JSX.Element | null`, and the exported constant `BACKUP_WARNING_DISMISSED_KEY = "sa.backupWarningDismissed"` for tests.

- [ ] **Step 1: Write the failing test**

Create `web/ui/src/pages/settings/BackupWarningBanner.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { BackupWarningBanner, BACKUP_WARNING_DISMISSED_KEY } from "./BackupWarningBanner";

const BASE = {
  enabled: false,
  destination: "local",
  schedule: "daily",
  hour: 3,
  weekday: 0,
  retention: 7,
  passphrase_set: false,
  local_dir: "/data/backups",
  s3: { endpoint: "", region: "", bucket: "", prefix: "", access_key: "", secret_key_set: false, path_style: false },
  last_run_at: "0001-01-01T00:00:00Z",
  last_status: "",
  last_error: "",
  last_size: 0,
  next_run_at: "0001-01-01T00:00:00Z",
  pending_restore: false,
};

function mount(config: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ ...BASE, ...config }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/settings?section=owner-workspaces"]}>
        <BackupWarningBanner />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

it("warns when backups are not configured", async () => {
  mount({});
  expect(await screen.findByText(/backups are not enabled/i)).toBeInTheDocument();
});

it("stays quiet when backups are enabled", async () => {
  mount({ enabled: true, passphrase_set: true });
  await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
  expect(screen.queryByText(/backups are not enabled/i)).not.toBeInTheDocument();
});

it("never returns once dismissed, even when backups are still off", async () => {
  const first = mount({});
  await screen.findByText(/backups are not enabled/i);
  await userEvent.click(screen.getByRole("button", { name: /dismiss/i }));
  expect(screen.queryByText(/backups are not enabled/i)).not.toBeInTheDocument();
  expect(localStorage.getItem(BACKUP_WARNING_DISMISSED_KEY)).toBe("1");
  first.unmount();

  // Dismissal is permanent by design: it is not cleared when backups are
  // enabled, so it must not come back when they are later turned off again.
  mount({});
  await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
  expect(screen.queryByText(/backups are not enabled/i)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/settings/BackupWarningBanner.test.tsx`
Expected: FAIL — cannot resolve `./BackupWarningBanner`.

- [ ] **Step 3: Write the component**

Create `web/ui/src/pages/settings/BackupWarningBanner.tsx`:

```tsx
import { useState } from "react";
import { AlertTriangle, ShieldCheck, X } from "lucide-react";
import { useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { useBackupConfig } from "@/lib/backup";

// Dismissal is permanent and is deliberately never cleared — not when backups
// are enabled, and not if they are later turned off again. An owner who has
// said "not now" once has answered, and a warning that reappears after being
// dismissed is what teaches people to ignore banners.
export const BACKUP_WARNING_DISMISSED_KEY = "sa.backupWarningDismissed";

function readDismissed() {
  try {
    return localStorage.getItem(BACKUP_WARNING_DISMISSED_KEY) === "1";
  } catch {
    // Storage disabled or full: show the warning rather than crash the section.
    return false;
  }
}

export function BackupWarningBanner() {
  const { data } = useBackupConfig();
  const [dismissed, setDismissed] = useState(readDismissed);
  const [searchParams, setSearchParams] = useSearchParams();

  // Both halves matter: a passphrase with automatic runs switched off means no
  // snapshot is ever taken, which is the case this warning exists for.
  const configured = Boolean(data?.passphrase_set) && Boolean(data?.enabled);
  if (dismissed || !data || configured) return null;

  function dismiss() {
    try {
      localStorage.setItem(BACKUP_WARNING_DISMISSED_KEY, "1");
    } catch {
      // Nothing to do — the banner still hides for this mount.
    }
    setDismissed(true);
  }

  function goToBackup() {
    const next = new URLSearchParams(searchParams);
    next.set("section", "owner-backup");
    setSearchParams(next);
  }

  return (
    <div className="mb-4 rounded-md bg-warn-soft p-3 text-sm text-warn">
      <div className="flex items-center gap-2 font-bold">
        <AlertTriangle className="size-4 shrink-0" />
        Backups are not enabled
      </div>
      <p className="mt-1">
        This install has no snapshot of its database or knowledge bases. Copying
        the data folder is not a substitute — it leaves the encryption key
        behind.
      </p>
      <div className="mt-2 flex gap-2">
        <Button size="sm" onClick={goToBackup}>
          <ShieldCheck />
          Set up backups
        </Button>
        <Button size="sm" variant="ghost" onClick={dismiss}>
          <X />
          Dismiss
        </Button>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web/ui && npx vitest run src/pages/settings/BackupWarningBanner.test.tsx`
Expected: PASS, all three.

- [ ] **Step 5: Mount it in the owner-workspaces section**

In `web/ui/src/pages/settings/OwnerSections.tsx`, add the import beside the existing ones:

```tsx
import { BackupWarningBanner } from "./BackupWarningBanner";
```

and render it as the first child of `WorkspacesSection`'s outer `<div>`, above the heading row:

```tsx
  return (
    <div>
      <BackupWarningBanner />
      <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-workspaces" />
```

- [ ] **Step 6: Run the settings suites**

Run: `cd web/ui && npx vitest run src/pages/settings/`
Expected: PASS. `OwnerSections.test.tsx` mocks `/api/v1/backup/config` with `enabled: false`, so the banner now renders in those tests — that is fine unless an assertion counts buttons or text globally. If one breaks, add `localStorage.setItem("sa.backupWarningDismissed", "1")` in that file's existing `beforeEach` **only** for the tests that are not about the banner, and clear it in `afterEach`.

- [ ] **Step 7: Typecheck, lint, and the full frontend suite**

Run: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run`
Expected: no errors, all tests pass.

- [ ] **Step 8: Commit**

```bash
git add web/ui/src/pages/settings/BackupWarningBanner.tsx web/ui/src/pages/settings/BackupWarningBanner.test.tsx web/ui/src/pages/settings/OwnerSections.tsx
git commit -m "feat(web/settings): warn once when backups are not configured"
```

---

### Task 5: Website — move the page to Operations and rewrite it as a runbook

**Files (all in `/home/rookie/rookery-web`):**
- Move: `src/content/docs/docs/concepts/backup-and-restore.md` → `src/content/docs/docs/operations/backup-and-restore.md`
- Modify: `astro.config.mjs` (sidebar entry + redirect)
- Modify: `src/content/docs/docs/concepts/knowledge-base.md:121`, `src/content/docs/docs/reference/cli.md:94`, `src/content/docs/docs/installation/windows.md:69`, `src/content/docs/docs/reference/api.md:62` (link targets)
- Modify: `src/content/docs/docs/operations/configuration.md:37` (drop "if configured")

**Interfaces:**
- Consumes: the CLI restore behaviour verified in the spec.
- Produces: the page at `/docs/operations/backup-and-restore`, which Task 6's `CLAUDE.md` edit and the `docs-sync` skill both reference.

- [ ] **Step 1: Create the branch and move the file**

```bash
cd /home/rookie/rookery-web
git checkout -b docs/backup-runbook
git mv src/content/docs/docs/concepts/backup-and-restore.md src/content/docs/docs/operations/backup-and-restore.md
```

- [ ] **Step 2: Move the sidebar entry**

In `astro.config.mjs`, **delete** this line from the `Concepts` items array:

```js
            { label: "Backup and restore", slug: "docs/concepts/backup-and-restore", attrs: { "data-icon": "backup" } },
```

and insert into the `Operations` items array, between Configuration and Health:

```js
            { label: "Backup and restore", slug: "docs/operations/backup-and-restore", attrs: { "data-icon": "backup" } },
```

- [ ] **Step 3: Add the redirect**

The old URL is public. Add a top-level `redirects` key to the `defineConfig({...})` object, as a sibling of `site` and `integrations`:

```js
  // The backup page moved from Concepts to Operations. The old URL was public,
  // so it redirects rather than 404s.
  redirects: {
    "/docs/concepts/backup-and-restore": "/docs/operations/backup-and-restore/",
  },
```

- [ ] **Step 4: Update the four inbound links**

```bash
cd /home/rookie/rookery-web
grep -rl "docs/concepts/backup-and-restore" src/ | xargs sed -i 's|/docs/concepts/backup-and-restore|/docs/operations/backup-and-restore|g'
grep -rn "concepts/backup-and-restore" src/ astro.config.mjs
```

The second command must print **nothing**. (`astro.config.mjs`'s redirect key is written with the leading `/docs/` form and is matched by the first grep pattern only inside `src/`, so it is untouched — verify by eye that the redirect source is still `/docs/concepts/backup-and-restore`.)

- [ ] **Step 5: Fix the data-directory listing**

In `src/content/docs/docs/operations/configuration.md`, line 37:

```diff
-  backups/            local backups, if configured
+  backups/            local backups
```

- [ ] **Step 6: Rewrite the page**

Replace the entire contents of `src/content/docs/docs/operations/backup-and-restore.md` with:

````markdown
---
title: Backup and restore
description: One encrypted file holding the database and every workspace's knowledge base.
icon: backup
---

A backup covers the **whole installation**: the database plus every workspace's
knowledge base, in a single passphrase-encrypted file.

Three things to do, in order: turn automatic backups on, keep a copy off this
machine, and know the restore command before you need it.

## Why a plain file copy is not enough

Rookery encrypts stored credentials — workspace passwords, connection tokens, chat
app tokens — with a key belonging to that installation.

Copy the data folder to new hardware without that key and you get an installation
that **starts, looks healthy, and has silently lost every scheduled agent and
every connection**. No error, just nothing working.

The backup carries the key inside the encrypted file. That is what makes moving
to a new machine one step — and it is why the passphrase is the one thing you
must not lose.

## 1. Turn on automatic backups

In the web interface: **Settings → Backup**.

1. Set a **passphrase**. Nothing is ever written unencrypted, so this is
   required. Put it in your password manager now — it cannot be recovered from
   the backup, the server, or us.
2. Choose **daily** or **weekly**, and an hour. Times are the server's local
   time.
3. Set how many snapshots to **keep**. Older ones are pruned automatically.
4. Turn the schedule **on** and save.

Missed runs collapse into one rather than piling up, so a machine that was
asleep at 03:00 takes one backup when it wakes, not seven.

Or from the command line, any time:

```bash
rookery backup now
```

## 2. Where snapshots go

**A local folder — `<data_dir>/backups`.** This is not configurable, and that is
deliberate. The service runs under `ProtectSystem=strict` with write access
granted only to its data directory, and the container image mounts a single
volume. A path anywhere else would not fail when you typed it; it would fail at
03:00 with a permission error nobody is watching.

**S3, or anything S3-compatible** — AWS, Backblaze B2, Cloudflare R2, MinIO,
Wasabi. Bucket, region and credentials go in the same settings page. This is the
destination to use if you want copies leaving the machine on their own.

Both filter strictly on Rookery's own naming, so a bucket or folder you share
with other data will never have a foreign file listed, downloaded or deleted.

## 3. Keep a copy off this machine

:::danger
A backup on the same disk as the installation is not a backup. The disk that
loses your knowledge base is the disk holding the snapshot of it.
:::

If you are not using S3, download each snapshot from **Settings → Backup** and
store it somewhere the machine cannot take with it — another computer, an
external drive, whatever cloud storage you already use. The file is encrypted
with your passphrase, so it is safe to keep somewhere you would not put plain
notes.

Doing this once, today, is worth more than a perfect schedule you never take a
copy from.

## Checking one

```bash
rookery backup list
rookery backup verify <file>
```

`verify` decrypts and reads the whole archive without restoring it. Worth doing
once after you set backups up, so you find out now rather than during a restore.

:::tip
A backup you have never verified is a hope, not a backup.
:::

## Restoring onto a new machine

This is the whole point of the copy you kept. Nothing needs to exist on the new
machine first — no owner account, no database. The snapshot brings both.

1. **Install Rookery** on the new machine. Use the same version as the snapshot
   or a newer one; a snapshot from a newer build is refused and tells you which
   version to upgrade to.
2. **Do not start the server**, and do not run `owner bootstrap`.
3. **Restore**, pointing at the file you downloaded:

   ```bash
   rookery backup restore ~/Downloads/rookery-2026-08-11T03-00-00.rkb
   ```

   It prompts for the passphrase with the echo off. To script it, pipe the
   passphrase in with `--passphrase-stdin`. The argument may be a path to a file
   anywhere on disk, or the name of a snapshot already in the local backups
   folder.

4. **Start the server** and sign in with the owner password from the old
   install.

Everything comes back: workspaces, agents, schedules, connections and knowledge.

:::caution
If you set `ROOKERY_SYSTEM_KEY` explicitly and it does not match the key inside
the snapshot, the restore stops and tells you. Unset it, or set it to the
snapshot's key.
:::

### The command applies the restore; the button schedules it

These two paths differ, and the difference matters if you use the wrong mental
model for the one you picked.

**`rookery backup restore`** stages the snapshot, verifies every checksum, and
then applies it, all in the one command. It prints `restore complete` when the
data is in place. Starting the server afterwards is how you use the restored
install, not how the restore happens.

**The Restore button in Settings** stages the restore, marks it, and shuts the
server down. The swap happens on the next start, before the database is opened.
So the server coming back up *is* the restore.

Either way the restore only ever runs against a stopped installation — the
command refuses while the server is running, rather than racing it.

Changed your mind before a staged restore fires:

```bash
rookery backup cancel-restore
```

Without this, a staged restore applies whenever the server next starts —
possibly weeks later.

## Rollback

Applying a restore moves the existing database, knowledge bases and encryption key
into a timestamped `.pre-restore-*` folder in the data directory before writing the
new ones. Only the most recent is kept.

The key is moved **with** the data, not left behind — otherwise the rollback copy
would be undecryptable the moment the restore landed.

## What is not included

**`claude-homes/`** — coder tool configuration and credentials. Regenerated on
demand, and deliberately never in a backup.

**`session.key`** — the key signing browser cookies. Leaving it out means
restoring onto new hardware does not also transplant live sessions. Losing it
costs one sign-in.

**`config.yaml`** and staging directories.

## What is not built

Per-workspace restore, incremental backups, and Google Drive or Dropbox
destinations. Restore is all-or-nothing today.
````

- [ ] **Step 7: Build the site**

```bash
cd /home/rookie/rookery-web && npm run build
```
Expected: a successful build. Starlight fails loudly on a sidebar `slug:` that does not resolve, so a missed rename surfaces here.

- [ ] **Step 8: Commit**

```bash
cd /home/rookie/rookery-web
git add -A
git commit -m "docs: move backup to Operations and rewrite it as a runbook"
```

---

### Task 6: `CLAUDE.md`, docs-sync, and the full local gate

**Files:**
- Modify: `CLAUDE.md` (Backup and restore section)
- Verify: everything

**Interfaces:**
- Consumes: all previous tasks.
- Produces: a branch ready for a PR.

- [ ] **Step 1: Update `CLAUDE.md`**

In the **Backup and restore** section, find the `Destination` interface sentence in the `internal/backup` table row and the surrounding prose. Add a paragraph after the "Snapshot contents" paragraph:

```markdown
**The local destination is not configurable, and the removal is the fix rather
than a simplification.** Snapshots go to `backup.DefaultLocalDir(dataDir)` =
`<data_dir>/backups`, computed in one place and used by the CLI, the scheduler
and the web API alike. The settings page used to offer a free-text folder, but
the packaged unit runs with `ProtectSystem=strict` and
`ReadWritePaths=<data_dir>` and the container mounts one volume, so every other
path failed at 03:00 with a permission error rather than at save time with an
explanation. `Config` therefore has no `Local` field: an install that stored one
before drops it silently, because `encoding/json` ignores unknown keys — which
is the entire migration. `local_dir` survives on the API as an **output**, the
resolved path the settings page displays. The CLI keeps `--dir`, since it runs
as the operator rather than as the confined unit, and `restore` must accept a
path to a downloaded snapshot.
```

Also correct the restore description in that section if it states the CLI stages for the next boot: `cmd/rookery`'s `restore` action calls `StageRestore` **and** `ApplyPendingRestore` in one command. Only the web button defers to the next start.

- [ ] **Step 2: Run the docs-sync skill**

Invoke the `docs-sync` skill. It holds the change-to-page trigger map; this change touches a backup destination and a CLI-adjacent behaviour, which are both listed triggers.

- [ ] **Step 3: Run the mechanised documentation check**

```bash
ROOKERY_WEB_DIR=/home/rookie/rookery-web make docs-sync-check
```
Expected: PASS **with website assertions actually running**. Without the variable it silently skips them from a worktree.

- [ ] **Step 4: Run the full local gate**

```bash
make ci
```
Expected: PASS — `ci-fmt`, `ci-vet`, `ci-test` (race, 900s), `ci-cross` (six GOOS/GOARCH pairs), `ci-ui`, `ci-docs`.

- [ ] **Step 5: Commit and push both repositories**

```bash
git add CLAUDE.md
git commit -m "docs: record why the backup folder setting is gone"
git push -u origin worktree-backup-destination-and-docs

cd /home/rookie/rookery-web && git push -u origin docs/backup-runbook
```

- [ ] **Step 6: Open the pull requests**

```bash
gh pr create --repo ilijad1/rookery \
  --title "fix(backup): remove a destination folder the service cannot write to" \
  --body "..."
```

The PR title must be a valid Conventional Commit — it becomes the squashed commit release-please reads. Open the matching website PR in `ilijad1/rookery-web`, and cross-link the two.

---

## Self-Review

**Spec coverage.** Spec §1 (settings.go, `DefaultLocalDir`, `BuildDestination`, no-migration strip, CLI `--dir` kept) → Task 1. §2 (`local_dir` output) → Task 2. §3 (input becomes a statement, download hint) → Task 3. §4 (banner, permanent dismissal, shared query key, owner-workspaces mount) → Task 4. §5 (move, sidebar, redirect, four links, configuration.md, rewritten runbook incl. the corrected CLI-vs-button restore semantics) → Task 5. Testing section → distributed across Tasks 1–4 plus Task 6's `make ci`. Implementation note about `ROOKERY_WEB_DIR` → Task 6 Step 3.

**Placeholders.** The PR body in Task 6 Step 6 is written as `"..."` because it is prose composed from the finished diff; every other step carries its literal content.

**Type consistency.** `DefaultLocalDir(dataDir string) string` and `BuildDestination(dataDir string, systemKey []byte)` are used with those exact signatures in Tasks 1, 2 and their tests. `BACKUP_WARNING_DISMISSED_KEY` is exported from the component and imported by its test under that name. `SaveBackupConfig` loses `local` in Task 3, matching the `backupConfigReq` field dropped in Task 2.
