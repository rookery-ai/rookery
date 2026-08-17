package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/rookery-ai/rookery/migrations"
	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB with migration support.
type DB struct {
	*sql.DB
}

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

	// Pragmas are declared in the DSN, not executed after opening, because
	// `database/sql` is a connection POOL and both of these are per-connection
	// settings. An `Exec("PRAGMA …")` runs on whichever single connection the pool
	// happened to hand out and every other connection in the pool never sees it —
	// so this was never "the database has foreign keys on", it was "one connection
	// does". Declared here, the driver applies them to each connection it opens.
	// (journal_mode is the exception that hid the bug: WAL is persisted in the file
	// itself, so it stuck regardless and the arrangement looked like it worked.)
	//
	// busy_timeout is not a tuning knob — without it a concurrent write does not
	// wait, it FAILS. WAL permits many readers but exactly one writer, and SQLite's
	// default for the second writer is to return SQLITE_BUSY immediately. Rookery
	// writes from several goroutines by design (the scheduler firing overdue agents,
	// a run recording its result, the connector refresh loop, the web API), and those
	// call sites generally log the error and carry on. The scheduler's was the
	// expensive one: a schedule whose times failed to save stayed due, so the next
	// poll ran the same agent all over again. Waiting up to 5s costs an idle install
	// nothing and makes those writes queue instead of collide.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	d := &DB{sqldb}

	if err := d.migrate(); err != nil {
		d.Close()
		return nil, err
	}

	return d, nil
}

// splitStatements splits a SQL file into individual statements on semicolons,
// skipping comments and blank lines.
func splitStatements(sql string) []string {
	var stmts []string
	var buf strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || trimmed == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimFunc(buf.String(), unicode.IsSpace)
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
			buf.Reset()
		}
	}
	return stmts
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			continue
		}

		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		for _, stmt := range splitStatements(string(data)) {
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("apply migration %s statement %q: %w", name, stmt[:min(len(stmt), 60)], err)
			}
		}

		if _, err := d.Exec(`INSERT INTO schema_migrations(name) VALUES(?)`, name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}

	return nil
}
