# Embedded Migrations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Compile the SQL migrations into the `rookery` binary so the deb, rpm and every release archive can open their own database, and add a CI gate that installs a package and runs it.

**Architecture:** The repo-root `migrations/` directory becomes a Go package exporting an `embed.FS`. `db.Open` loses its `migrationsDir` parameter and always migrates from that FS. The exe-relative/CWD-relative `resolveDir` lookup and the Dockerfile's compensating `COPY` are deleted, leaving one code path on every platform. A new `scripts/smoke-package.sh` installs each artifact in a throwaway container and runs it; both `make ci-package` and a new `pr.yml` job call that one script.

**Tech Stack:** Go 1.26.5 (`embed`, `io/fs`, `testing.T.Chdir`), goreleaser v2 (nfpm), GitHub Actions, podman/docker, GNU make.

**Spec:** `docs/superpowers/specs/2026-08-10-packaged-migrations-design.md`

## Global Constraints

- Module path is `github.com/ilijad1/rookery`; the new package is `github.com/ilijad1/rookery/migrations`.
- Embed pattern is `*.sql` — **not** `*.up.sql`. The `.down.sql` files must be reachable through `FS`.
- `db.Open` takes exactly one argument after this change. No `OpenWithMigrations`/`fs.FS` override is added — a second path is what this change exists to remove.
- Migration ordering and names are unchanged, so `schema_migrations` rows on existing installs must still match and nothing may re-apply.
- Conventional Commits on every commit (`type(scope): summary`). Never commit to `main`.
- `gofmt` clean and `go vet ./...` clean; `make ci` must pass at the end.
- Do not add a Go dependency. `embed` and `io/fs` are stdlib.
- Smoke-test scope is Linux only (deb, rpm, tar.gz). darwin/windows stay compile-verified by the existing cross-compile job.

---

### Task 1: Embed the migrations directory

Makes `migrations/` a Go package. Nothing consumes it yet, so this task compiles and tests on its own.

**Files:**
- Create: `migrations/embed.go`
- Test: `migrations/embed_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `package migrations` at `github.com/ilijad1/rookery/migrations`, exporting `var FS embed.FS` whose root holds every `*.sql` file in the directory (flat, no subdirectories). Read it with `fs.ReadDir(migrations.FS, ".")` and `fs.ReadFile(migrations.FS, name)`.

- [ ] **Step 1: Write the failing test**

Create `migrations/embed_test.go`:

```go
package migrations_test

import (
	"io/fs"
	"os"
	"testing"

	"github.com/ilijad1/rookery/migrations"
)

// The embedded set must equal what is on disk. A migration added to the
// directory but not reachable through FS would apply in development, where the
// files exist, and silently vanish from every shipped artifact — which is the
// exact failure this package exists to prevent.
func TestEmbedHoldsEverySQLFileOnDisk(t *testing.T) {
	onDisk, err := fs.Glob(os.DirFS("."), "*.sql")
	if err != nil {
		t.Fatalf("glob disk: %v", err)
	}
	if len(onDisk) == 0 {
		t.Fatal("no .sql files on disk; test is not running in the migrations directory")
	}

	embedded, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("glob embed: %v", err)
	}

	if len(embedded) != len(onDisk) {
		t.Fatalf("embedded %d files, disk has %d\nembedded: %v\ndisk: %v",
			len(embedded), len(onDisk), embedded, onDisk)
	}
	for i := range onDisk {
		if embedded[i] != onDisk[i] {
			t.Errorf("index %d: embedded %q, disk %q", i, embedded[i], onDisk[i])
		}
	}
}

// Down files are never executed today. They are embedded anyway so that wiring
// a down runner later cannot silently find them missing.
func TestEmbedIncludesDownMigrations(t *testing.T) {
	down, err := fs.Glob(migrations.FS, "*.down.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(down) == 0 {
		t.Fatal("no .down.sql files embedded; the embed pattern is too narrow")
	}
}

// A file present in the listing but empty would apply as a no-op migration and
// record itself as applied, which is unrecoverable without hand-editing
// schema_migrations.
func TestEmbeddedMigrationsAreNonEmpty(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		data, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", e.Name())
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./migrations/ -v`

Expected: FAIL — the package does not compile, because `migrations/embed.go` does not exist and there is no `migrations.FS`.

- [ ] **Step 3: Write the embed package**

Create `migrations/embed.go`:

```go
// Package migrations carries the SQL schema migrations compiled into the binary.
//
// They are embedded rather than read from disk because the deb, rpm and every
// release archive ship the binary alone. An on-disk lookup made all of them fail
// on first use with "read migrations dir: no such file or directory", while only
// the container image worked — and only because its Dockerfile copied this
// directory next to the binary purely to satisfy that lookup.
//
// The pattern matches *.sql rather than *.up.sql on purpose: the down files are
// never executed today, but the narrower pattern would silently drop them the
// moment a down runner is wired.
package migrations

import "embed"

// FS holds every .up.sql and .down.sql file in this directory, flat at the root.
// Read it with fs.ReadDir(FS, ".") and fs.ReadFile(FS, name).
//
// //go:embed fails the BUILD when its pattern matches nothing, so a missing
// migration set can no longer reach a user as a first-run runtime error.
//
//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./migrations/ -v`

Expected: PASS — all three tests.

- [ ] **Step 5: Verify the embed survives a build from an unrelated directory**

Run:

```bash
go build ./migrations/ && echo "builds clean"
gofmt -l migrations/
```

Expected: `builds clean`, and `gofmt -l` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add migrations/embed.go migrations/embed_test.go
git commit -m "feat(migrations): embed the SQL schema files into the binary"
```

---

### Task 2: Switch `db.Open` to the embedded FS

The signature change is compiler-forced to be atomic: every call site must move in the same commit or the repo does not build. That is why this task is larger than the others.

**Files:**
- Modify: `internal/db/db.go` (the `Open` and `migrate` functions)
- Modify: `cmd/rookery/main.go:121`, `:782`, `:809`
- Modify: `cmd/livecheck/main.go:24`
- Modify: 28 test call sites, listed in Step 5
- Modify: `internal/secrets/service_test.go` (delete `findMigrations`)
- Modify: `internal/coder/searchkey_wiring_test.go` (delete `findWiringMigrations`)
- Test: `internal/db/embed_test.go`

**Interfaces:**
- Consumes: `migrations.FS` from Task 1.
- Produces: `func Open(path string) (*DB, error)` — one argument, always migrates. `migrate` becomes `func (d *DB) migrate() error` with no parameter.

- [ ] **Step 1: Write the failing test**

Create `internal/db/embed_test.go`:

```go
package db_test

import (
	"path/filepath"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

// The packaged failure reproduced: a process whose working directory has no
// migrations/ anywhere above it, which is what a systemd user unit
// (WorkingDirectory unset, so CWD is $HOME) and any tar.gz user get.
func TestOpenMigratesWithNoMigrationsDirOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// One table from the initial schema and one from the newest migration, so
	// the assertion covers the whole ordered run rather than just the first file.
	for _, table := range []string{"workspaces", "pending_actions"} {
		var name string
		err := database.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migrate: %v", table, err)
		}
	}
}

// Opening the same database twice must be a no-op the second time. If the
// embedded names ever stopped matching the names already recorded in
// schema_migrations, every migration would re-apply and CREATE TABLE would fail.
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	first, err := db.Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	var applied int
	if err := first.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count: %v", err)
	}
	first.Close()

	second, err := db.Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	var again int
	if err := second.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&again); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if again != applied {
		t.Errorf("re-open changed applied count: %d then %d", applied, again)
	}
	if applied == 0 {
		t.Error("no migrations were applied at all")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/db/ -run 'TestOpenMigrates|TestOpenIsIdempotent' -v`

Expected: FAIL — compile error, `not enough arguments in call to db.Open`.

- [ ] **Step 3: Change `Open` and `migrate` in `internal/db/db.go`**

Add `"io/fs"` to the imports and add `"github.com/ilijad1/rookery/migrations"`. Remove `"os"` and `"path/filepath"` only if nothing else in the file uses them — `Open` still calls `os.MkdirAll` and `filepath.Dir`, so **keep both**.

Replace the `Open` doc comment and body:

```go
// Open opens (or creates) the SQLite database at path, applies WAL+FK pragmas,
// and runs any pending migrations.
//
// The migrations are compiled into the binary (see the root migrations package),
// not read from disk: the deb, rpm and release archives ship the binary alone, so
// a disk lookup failed on first use for every packaged install.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode and foreign key enforcement immediately after opening.
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON"} {
		if _, err := sqldb.Exec(pragma); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", pragma, err)
		}
	}

	d := &DB{sqldb}

	if err := d.migrate(); err != nil {
		d.Close()
		return nil, err
	}

	return d, nil
}
```

Then change `migrate`'s signature and its two filesystem reads. Everything else in the function — the tracker table, the `.up.sql` filter, the sort, the applied check, `splitStatements`, the insert — stays byte-for-byte as it is:

```go
func (d *DB) migrate() error {
	// Ensure the migrations tracker table exists.
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
```

and, inside the loop, replace the `os.ReadFile(filepath.Join(dir, name))` call with:

```go
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
```

`fs.ReadDir` returns entries already sorted by filename, and the existing
`sort.Strings(files)` call is kept so the ordering is stated locally rather than
inherited from a documented property of the caller.

- [ ] **Step 4: Update the three `cmd/rookery/main.go` call sites**

At each of lines 121, 782 and 809 the shape is identical. Delete the
`migrationsDir := resolveDir("migrations")` line immediately above and change the
call:

```go
			database, err := db.Open(cfg.Database.Path)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
```

Leave `resolveDir` itself in place — `cmd/rookery/backup_cmd.go` still calls it
and Task 3 removes both together. Go does not error on an unused package-level
function, so this compiles.

- [ ] **Step 5: Update `cmd/livecheck/main.go` and every test call site**

`cmd/livecheck/main.go:24` passed `""`, which used to mean *skip migrations*.
It opens a real data directory where running the idempotent migrations is
correct, so it simply loses the argument:

```go
	d, err := db.Open(dataDir + "/rookery.db")
```

Then drop the second argument at all 28 test call sites. Every one of them
passes a relative literal (`"../../migrations"` or `"../migrations"`) except the
two noted below:

```
internal/agentdesigner/edit_test.go:17
internal/agentrunner/inbox_test.go:15
internal/chat/autotitle_test.go:56
internal/coder/searchkey_wiring_test.go:22      <- passes findWiringMigrations(t)
internal/connectors/dbstore_test.go:27
internal/db/auditfilter_test.go:13
internal/db/connectors_test.go:16
internal/db/draft_used_conns_test.go:16
internal/db/inbox_test.go:15
internal/db/platform_connection_test.go:13
internal/db/platform_identity_test.go:12
internal/db/runs_test.go:15
internal/db/schedules_dashboard_test.go:14
internal/db/skills_v2_test.go:16
internal/gateway/dispatch_recover_test.go:56
internal/gateway/placeholder_test.go:71
internal/gateway/router_test.go:23
internal/gateway/router_test.go:214
internal/gateway/sendtouser_test.go:20
internal/reminder/tick_test.go:32
internal/secrets/service_test.go:19             <- passes a migrationsDir variable
internal/skilldesigner/designer_test.go:21
web/api_identity_test.go:25
web/api_skills_test.go:27
web/api_test_helpers_test.go:27
web/connectors_test.go:13
web/connectors_test.go:41
web/connectors_uniquebot_test.go:18
```

The mechanical edits can be done with:

```bash
grep -rl '"\.\./\.\./migrations"\|"\.\./migrations"' --include='*_test.go' . \
  | xargs sed -i -E 's/, "(\.\.\/)+migrations"\)/)/'
```

Verify afterwards with `grep -rn 'migrations"' --include='*_test.go' .`, which
must print nothing.

Then delete the two walk-up helpers, which existed only to locate a directory
the binary now carries:

- `internal/secrets/service_test.go` — delete `findMigrations` (lines 25-42) and
  change `newTestDB` to drop the `migrationsDir` local:

```go
func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return database
}
```

- `internal/coder/searchkey_wiring_test.go` — delete `findWiringMigrations`
  (lines 30-45) and simplify `newWiringTestDB`. Its comment references the
  deleted secrets helper, so rewrite it:

```go
// newWiringTestDB gives the test a real (temp-file) SQLite DB carrying the
// project's actual schema, so the secret really round-trips through
// storage+decryption rather than a mock.
func newWiringTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}
```

Both files will now have unused imports (`os`, and possibly `path/filepath`).
`goimports -w` on the two files, or remove them by hand, then confirm with
`go vet`.

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `go test ./internal/db/ -run 'TestOpenMigrates|TestOpenIsIdempotent' -v`

Expected: PASS — both tests.

- [ ] **Step 7: Verify the whole repo builds and the full suite passes**

Run:

```bash
gofmt -l . | grep -v '^web/ui/' || true
go vet ./...
go build ./...
go test ./... -count=1 -timeout 900s
```

Expected: `gofmt -l` prints nothing, `go vet` and `go build` are silent, and the
suite is green. Any remaining `not enough arguments in call to db.Open` means a
call site in Step 5 was missed.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "fix(db): run migrations from the embedded FS, not from disk"
```

---

### Task 3: Delete `resolveDir` and the Dockerfile workaround

**Files:**
- Modify: `cmd/rookery/backup_cmd.go` (`binarySchemaVersion`, around line 104)
- Modify: `cmd/rookery/main.go` (delete `resolveDir`, lines 854-863)
- Modify: `Dockerfile:74`
- Test: `cmd/rookery/schemaversion_test.go`

**Interfaces:**
- Consumes: `migrations.FS` from Task 1.
- Produces: `binarySchemaVersion() (string, error)` keeps its exact signature and
  return value — the newest `.up.sql` filename, currently
  `011_pending_actions.up.sql`. `.rkb` snapshots record this string, so it must
  not change.

- [ ] **Step 1: Write the failing test**

Create `cmd/rookery/schemaversion_test.go`. It is in `package main` because
`binarySchemaVersion` is unexported:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A .rkb snapshot records the schema version as this exact string and compares
// it on restore, so switching the source from disk to the embedded FS must not
// change what it returns. Comparing against the directory rather than a
// hardcoded name keeps the test correct when the next migration lands.
func TestBinarySchemaVersionMatchesNewestOnDisk(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	want := ""
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".up.sql") && name > want {
			want = name
		}
	}
	if want == "" {
		t.Fatal("no .up.sql files on disk")
	}

	got, err := binarySchemaVersion()
	if err != nil {
		t.Fatalf("binarySchemaVersion: %v", err)
	}
	if got != want {
		t.Errorf("binarySchemaVersion() = %q, newest on disk is %q", got, want)
	}
}

// It must not depend on the working directory — that dependency is the whole bug.
func TestBinarySchemaVersionWorksFromAnyDirectory(t *testing.T) {
	before, err := binarySchemaVersion()
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	t.Chdir(t.TempDir())

	after, err := binarySchemaVersion()
	if err != nil {
		t.Fatalf("after chdir: %v", err)
	}
	if after != before {
		t.Errorf("changed with CWD: %q then %q", before, after)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/rookery/ -run TestBinarySchemaVersion -v`

Expected: FAIL — `TestBinarySchemaVersionWorksFromAnyDirectory` fails with
`read migrations dir: open migrations: no such file or directory`, because
`resolveDir` falls back to a CWD-relative path. This is the packaged bug
reproduced as a unit test.

- [ ] **Step 3: Point `binarySchemaVersion` at the embedded FS**

In `cmd/rookery/backup_cmd.go`, add `"io/fs"` and
`"github.com/ilijad1/rookery/migrations"` to the imports, then replace the body:

```go
// binarySchemaVersion reports the newest migration this build ships, which is
// what a snapshot's schema version is compared against. It reads the embedded
// set, so it does not depend on the working directory or the install layout.
func binarySchemaVersion() (string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return "", fmt.Errorf("read embedded migrations: %w", err)
	}
	newest := ""
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".up.sql") && name > newest {
			newest = name
		}
	}
	if newest == "" {
		return "", errors.New("no migrations found")
	}
	return newest, nil
}
```

- [ ] **Step 4: Delete `resolveDir`**

`binarySchemaVersion` was its last caller. Remove the whole function from
`cmd/rookery/main.go` (lines 854-863):

```go
func resolveDir(sub string) string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), sub)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return sub
}
```

Confirm nothing references it: `grep -rn 'resolveDir' --include='*.go' .` must
print nothing.

- [ ] **Step 5: Delete the Dockerfile workaround**

`Dockerfile:74` reads:

```dockerfile
COPY migrations /usr/bin/migrations
```

Delete that line. It existed only to satisfy `resolveDir`'s exe-relative probe,
and leaving it would keep the container reading a directory the binary no longer
consults — the two-path drift this change removes.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./cmd/rookery/ -run TestBinarySchemaVersion -v`

Expected: PASS — both tests.

- [ ] **Step 7: Verify build, vet and image**

Run:

```bash
gofmt -l cmd/
go vet ./cmd/...
go build ./...
make docker-build
```

Expected: no gofmt or vet output, and the image builds with the `COPY` gone.

- [ ] **Step 8: Commit**

```bash
git add cmd/rookery/backup_cmd.go cmd/rookery/main.go cmd/rookery/schemaversion_test.go Dockerfile
git commit -m "refactor(cmd): drop resolveDir and the Dockerfile migrations copy"
```

---

### Task 4: Package smoke test — script, make target, CI job

One script is the single source so the local command and the CI job cannot
drift, mirroring the repo's existing "`make ci` mirrors the gate" rule. Every
install runs in a throwaway container, so the script needs no `sudo` and does not
pollute the developer's machine — which also means the rpm case runs on a
Debian-based CI runner and the deb case runs on a Fedora workstation.

**Files:**
- Create: `scripts/smoke-package.sh`
- Modify: `Makefile` (add `ci-package` to `.PHONY` and a target)
- Modify: `.github/workflows/pr.yml` (new `packages` job)

**Interfaces:**
- Consumes: goreleaser snapshot artifacts in `dist/` — `rookery_*_linux_amd64.deb`, `rookery-*.x86_64.rpm`, `rookery_*_linux_amd64.tar.gz`.
- Produces: `scripts/smoke-package.sh`, exit 0 on success and non-zero with a diagnostic on failure. Honours `CONTAINER_ENGINE` (default: podman, falling back to docker), matching the Makefile variable of the same name.

- [ ] **Step 1: Write the smoke script**

Create `scripts/smoke-package.sh` and `chmod +x` it:

```bash
#!/usr/bin/env bash
# Install each Linux artifact and run it.
#
# This is the gate that was missing when the deb, rpm and every archive shipped
# without their SQL migrations: nothing in CI had ever installed a package and
# started it, so all three failed on first use for the whole history of the repo.
#
# Every install happens inside a throwaway container, so no sudo is needed and
# the host is untouched — which also lets the rpm case run on a Debian CI runner
# and the deb case run on a Fedora workstation.
#
# Usage: scripts/smoke-package.sh [dist-dir]        (default: dist)
set -euo pipefail

DIST="${1:-dist}"
ENGINE="${CONTAINER_ENGINE:-$(command -v podman >/dev/null 2>&1 && echo podman || echo docker)}"

fail() { echo "::error::$*" >&2; exit 1; }

pick_artifact() {
	# Exactly one artifact must match, otherwise a stale dist/ would silently
	# smoke-test the previous build.
	#
	# .goreleaser.yaml sets no nfpm file_name_template, so goreleaser's default
	# applies and an rpm is named rookery_<ver>_linux_amd64.rpm — NOT the
	# rpm-conventional rookery-<ver>.x86_64.rpm. Both spellings are accepted so
	# that setting a template later cannot silently break this gate.
	local ext="$1"
	local -a hits
	mapfile -t hits < <(find "$DIST" -maxdepth 1 -name "*.$ext" \
		\( -name '*amd64*' -o -name '*x86_64*' \) | sort)
	[ "${#hits[@]}" -eq 1 ] \
		|| fail "expected exactly 1 amd64 .$ext in $DIST, found ${#hits[@]}: ${hits[*]:-none}"
	printf '%s\n' "${hits[0]}"
}

DEB="$(pick_artifact deb)"
RPM="$(pick_artifact rpm)"
TGZ="$(pick_artifact tar.gz)"

# Run inside the container: bootstrap from / (a systemd user unit has no
# WorkingDirectory, so its CWD is $HOME — never the source tree), then serve and
# probe. `rookery healthcheck` is used rather than curl because a minimal base
# image is not guaranteed to ship one.
readonly RUN_SCRIPT='
set -euo pipefail
cd /
rookery version
rookery owner bootstrap -u smoke -p "smoke-pw-12345"
rookery serve >/tmp/serve.log 2>&1 &
for i in $(seq 1 45); do
	if rookery healthcheck >/dev/null 2>&1; then
		echo "OK: healthy"
		exit 0
	fi
	sleep 1
done
echo "server never became healthy" >&2
cat /tmp/serve.log >&2
exit 1
'

smoke_in_container() {
	local label="$1" image="$2" artifact="$3" install_cmd="$4"
	echo "==> $label"
	"$ENGINE" run --rm \
		-v "$(realpath "$artifact")":/artifact:ro,Z \
		-e ROOKERY_DATA_DIR=/tmp/rookery-data \
		"$image" \
		bash -c "set -euo pipefail; $install_cmd; $RUN_SCRIPT" \
		|| fail "$label failed"
	echo "==> $label OK"
}

smoke_in_container "rpm on fedora" "fedora:latest" "$RPM" \
	'rpm -i /artifact'

smoke_in_container "deb on debian" "debian:stable-slim" "$DEB" \
	'apt-get update -qq >/dev/null && dpkg -i /artifact'

# The archive runs on the host, extracted to one directory and executed from a
# completely different one. That is the case the deleted exe-relative probe used
# to accidentally paper over whenever someone ran from the source tree.
echo "==> tar.gz from an unrelated CWD"
extract_dir="$(mktemp -d)"
run_dir="$(mktemp -d)"
data_dir="$(mktemp -d)"
trap 'rm -rf "$extract_dir" "$run_dir" "$data_dir"' EXIT

tar -xzf "$TGZ" -C "$extract_dir"
[ -x "$extract_dir/rookery" ] || fail "archive has no executable rookery at its root"

(
	cd "$run_dir"
	export ROOKERY_DATA_DIR="$data_dir" ROOKERY_PORT=18099
	"$extract_dir/rookery" owner bootstrap -u smoke -p 'smoke-pw-12345'
) || fail "tar.gz bootstrap failed from an unrelated CWD"
echo "==> tar.gz OK"

echo "all package smoke tests passed"
```

Then: `chmod +x scripts/smoke-package.sh`

- [ ] **Step 2: Run it to verify it fails without artifacts**

Run: `scripts/smoke-package.sh`

Expected: FAIL fast with `expected exactly 1 file matching dist/rookery_*_linux_amd64.deb, found 0` — proving the guard against a stale or empty `dist/` works before any container starts.

- [ ] **Step 3: Add the make target**

In `Makefile`, add `ci-package` to the `.PHONY` list on line 34-35:

```make
.PHONY: ui build-go build stop start deploy restart logs status clean test \
        ci ci-fmt ci-vet ci-test ci-cross ci-ui ci-package docker-build docker-run
```

and add the target after `ci-ui`:

```make
## ci-package: build a goreleaser snapshot and smoke-test the deb, rpm and tar.gz
##
## Deliberately NOT part of `make ci`: a snapshot rebuilds the SPA and all six
## binaries, so it runs in minutes rather than seconds. Run it when touching
## packaging, the Dockerfile, or anything the binary reads at startup.
ci-package:
	goreleaser release --clean --snapshot --skip=sign,sbom
	CONTAINER_ENGINE=$(CONTAINER_ENGINE) scripts/smoke-package.sh dist
```

- [ ] **Step 4: Add the CI job**

In `.github/workflows/pr.yml`, add a `packages` job after the `container` job,
at the same indentation as the other jobs:

```yaml
  packages:
    name: Package smoke test
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0 # goreleaser reads tags to compute the snapshot version

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - uses: actions/setup-node@v7
        with:
          node-version-file: .nvmrc
          cache: npm
          cache-dependency-path: web/ui/package-lock.json

      # A snapshot needs no tag and exercises the same builds/nfpm config the
      # release uses. Signing and SBOMs are release-only concerns.
      - uses: goreleaser/goreleaser-action@v7
        with:
          version: "~> v2"
          args: release --clean --snapshot --skip=sign,sbom

      # Installs each artifact in a throwaway container and actually runs it.
      # Nothing in CI had ever done this, which is why the deb, rpm and every
      # archive shipped unable to open their own database.
      - name: Smoke test the packages
        env:
          CONTAINER_ENGINE: docker
        run: scripts/smoke-package.sh dist
```

- [ ] **Step 5: Run the full gate locally**

Run:

```bash
make ci-package
```

Expected: goreleaser produces `dist/`, then three `==> ... OK` lines and
`all package smoke tests passed`. This is the first time an installed package has
been started; if the earlier tasks are correct it passes, and if `migrations`
regressed it fails with the original `read migrations dir` error.

- [ ] **Step 6: Verify the workflow file parses**

Run:

```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/pr.yml')); print('pr.yml parses')"
```

Expected: `pr.yml parses`.

- [ ] **Step 7: Commit**

```bash
git add scripts/smoke-package.sh Makefile .github/workflows/pr.yml
git commit -m "ci: smoke-test the deb, rpm and tar.gz artifacts"
```

---

### Task 5: Update the documentation the change invalidates

**Files:**
- Modify: `CLAUDE.md` (the "Container" section and the CI/CD job list)
- Modify: `packaging/README.md` (the rpm install command)

**Interfaces:**
- Consumes: the behaviour established in Tasks 1-4.
- Produces: no code.

- [ ] **Step 1: Fix the rpm filename in `packaging/README.md`**

Found while writing the smoke script: the documented command names a file
goreleaser never produces. `.goreleaser.yaml` sets no nfpm `file_name_template`,
so the default applies and the artifact is `rookery_<version>_linux_amd64.rpm`,
not the rpm-conventional `rookery-<version>.x86_64.rpm` the README shows. A user
following it verbatim gets "No such file or directory" before they ever reach the
migrations bug.

Replace the Fedora/RHEL block:

````markdown
## Fedora / RHEL

```bash
sudo rpm -i rookery_<version>_linux_amd64.rpm
```
````

Leave the Debian block alone — `rookery_<version>_linux_amd64.deb` already
matches what goreleaser emits.

- [ ] **Step 2: Fix the Container section**

`CLAUDE.md`'s Container section currently ends with a sentence that is now false:

> Two container notes worth knowing: **Podman ignores `HEALTHCHECK`** unless built
> with `--format docker` (Docker/buildx honours it), and `migrations/` is copied
> *beside* the binary because `resolveDir()` looks exe-relative before
> CWD-relative.

Replace it with:

> Two container notes worth knowing: **Podman ignores `HEALTHCHECK`** unless built
> with `--format docker` (Docker/buildx honours it), and the image no longer
> copies `migrations/` beside the binary — the SQL is embedded (root `migrations`
> package, `//go:embed *.sql`), so the container and the native binaries run the
> identical code path. That copy existed to satisfy an exe-relative lookup which
> made the deb, rpm and every archive fail on first use with `read migrations
> dir`; embedding removed the lookup and the whole class of bug. `//go:embed`
> fails the build when it matches nothing, so a missing migration set can no
> longer reach a user.

- [ ] **Step 3: Add the new job to the CI/CD section**

In the numbered "PR checks must pass" list, the header says **six jobs**. Change
it to **seven jobs** and add an entry after `Container smoke test`:

>    - `Package smoke test` — builds a goreleaser snapshot, then installs the
>      **rpm** (in a Fedora container), the **deb** (in a Debian container) and
>      extracts the **tar.gz**, running `owner bootstrap` + `serve` + `healthcheck`
>      from a working directory unrelated to the source tree. This is the guard
>      that keeps the packaged artifacts *runnable*; nothing had ever installed one,
>      which is exactly how they shipped unable to open their own database. Run it
>      locally with `make ci-package` — it is deliberately excluded from `make ci`
>      because a snapshot build takes minutes.

Then amend the `make ci` sentence in item 5, which claims it mirrors the gate
exactly, so it stays true:

> 5. **Run the same checks locally first** with `make ci` — it mirrors the gate
>    exactly, including the cross-compile matrix, with the single exception of
>    `Package smoke test` (`make ci-package`, kept separate for runtime).
>    `make ci-fmt` / `ci-vet` / `ci-test` / `ci-cross` / `ci-ui` run the pieces
>    individually.

- [ ] **Step 4: Verify no stale references remain**

Run:

```bash
grep -n 'resolveDir' CLAUDE.md
grep -n 'six jobs' CLAUDE.md
grep -n 'x86_64' packaging/README.md
```

Expected: all three print nothing.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md packaging/README.md
git commit -m "docs: record embedded migrations, the package gate and the rpm filename"
```

---

### Task 6: Verify against the real artifact on this host

The spec makes this the acceptance criterion: a unit test asserting the embed
holds the right files does not prove an installed package works. This task
reproduces the user's exact reported command.

**Files:** none — verification only.

**Interfaces:**
- Consumes: everything from Tasks 1-5.
- Produces: evidence, pasted into the PR description.

- [ ] **Step 1: Build a snapshot**

Run:

```bash
goreleaser release --clean --snapshot --skip=sign,sbom
ls -la dist/*.rpm dist/*.deb dist/*linux_amd64.tar.gz
```

Expected: one rpm, two debs (amd64 + arm64) and the archives.

- [ ] **Step 2: Run the exact reported command against the installed rpm**

Run, from the repo root:

```bash
podman run --rm -v "$PWD/dist":/dist:ro,Z fedora:latest bash -c '
  rpm -i /dist/*amd64*.rpm
  cd /
  rookery owner bootstrap -u owner -p "kompiri23"
'
```

Expected: **PASS** — `Owner account created: owner (id: …)`. Before this change
the identical command printed
`open db: read migrations dir: open migrations: no such file or directory`.

- [ ] **Step 3: Confirm the server actually serves from the installed rpm**

Run:

```bash
podman run --rm -p 18080:8080 -d --name rookery-rpm-smoke \
  -v "$PWD/dist":/dist:ro,Z fedora:latest bash -c '
    rpm -i /dist/*amd64*.rpm
    cd /
    rookery owner bootstrap -u owner -p "kompiri23"
    exec rookery serve
  '
sleep 10
curl -sS http://127.0.0.1:18080/healthz
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/
curl -sS http://127.0.0.1:18080/api/v1/auth/session
podman rm -f rookery-rpm-smoke
```

Expected: `/healthz` returns JSON containing `"status":"ok"`, the SPA root
returns `200`, and the session endpoint returns JSON.

- [ ] **Step 4: Confirm the archive runs from an unrelated CWD**

Run:

```bash
extract="$(mktemp -d)"; run="$(mktemp -d)"; data="$(mktemp -d)"
tar -xzf dist/rookery_*_linux_amd64.tar.gz -C "$extract"
cd "$run"
ROOKERY_DATA_DIR="$data" "$extract/rookery" owner bootstrap -u owner -p 'kompiri23'
cd -
rm -rf "$extract" "$run" "$data"
```

Expected: `Owner account created: owner (id: …)`. The working directory contains
no `migrations/` and neither does the extract directory's parent, so this is the
case the old exe-relative probe could never satisfy.

- [ ] **Step 5: Run the full local gate**

Run:

```bash
make ci
make ci-package
```

Expected: both green.

- [ ] **Step 6: Push and open the PR**

```bash
git push -u origin worktree-fix-packaged-migrations
gh pr create --draft \
  --title "fix(db): embed migrations so packaged installs can open their database" \
  --body "$(cat <<'EOF'
The deb, rpm and all six release archives ship the binary alone, so
`resolveDir("migrations")` found nothing and every packaged install failed on
first use:

    $ rookery owner bootstrap -u owner -p '***'
    open db: read migrations dir: open migrations: no such file or directory

Only the container worked, and only because its Dockerfile copied `migrations/`
next to the binary to satisfy that exact lookup.

The SQL is now embedded (`//go:embed *.sql` in a root `migrations` package),
`db.Open` takes only a path, and both `resolveDir` and the Dockerfile `COPY` are
deleted — one code path on every platform. A new `Package smoke test` job
installs the rpm, the deb and the tar.gz and actually runs each one; nothing in
CI had ever done that, which is how this shipped.

Verified on a Fedora host by installing the snapshot rpm and running the exact
reported command, then serving and probing `/healthz`.

Spec: `docs/superpowers/specs/2026-08-10-packaged-migrations-design.md`
Plan: `docs/superpowers/plans/2026-08-10-packaged-migrations.md`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: a draft PR whose title is a valid Conventional Commit, so the
`Conventional commit title` check passes.

---

## Self-Review

**Spec coverage.** Every section maps to a task: embed package → Task 1; `db.Open`
signature and the `""` sentinel inversion → Task 2; `resolveDir`, Dockerfile and
`binarySchemaVersion` → Task 3; test churn and the two deleted walk-up helpers →
Task 2 Step 5; CI package smoke test with rpm-in-Fedora and the foreign-CWD
archive case → Task 4; docs → Task 5; artifact verification → Task 6. The spec's
stated darwin/windows limitation is carried into Global Constraints.

**Placeholders.** None. Every code step carries the actual content; the 28 test
call sites are listed by file and line rather than described.

**Type consistency.** `migrations.FS` is `embed.FS` in Task 1 and read with
`fs.ReadDir(migrations.FS, ".")` / `fs.ReadFile(migrations.FS, name)` in Tasks 2
and 3. `db.Open(path string) (*DB, error)` is declared in Task 2 and called with
one argument everywhere afterwards. `binarySchemaVersion() (string, error)` keeps
its original signature. `CONTAINER_ENGINE` is spelled identically in the script,
the Makefile and the workflow.

**Sequencing note.** Task 2 is deliberately larger than the others: Go's compiler
forces the `db.Open` signature change and all 32 call sites into one commit. The
repo builds at the end of every task.
