package vault

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ilijad1/simple-agents/internal/memory"
)

// MigrateLegacyLayout moves any pre-vault on-disk data into per-user vaults. It
// handles the three legacy top-level directories:
//
//	<data>/agents/<workspaceID>/<agentID>   → <data>/vaults/<workspaceID>/agents/<agentID>
//	<data>/skills/<workspaceID>/<name>      → <data>/vaults/<workspaceID>/skills/<name>
//	<data>/memory/<workspaceID>/memory.jsonl→ <data>/vaults/<workspaceID>/memory/<id>.md
//
// It is idempotent and safe to run on every startup: it acts only on legacy
// directories that still exist, never overwrites an entry already present in the
// vault, and removes a legacy directory only once it has been fully drained.
// Returns nil quickly when there is nothing to migrate.
func (v *Vault) MigrateLegacyLayout() error {
	legacyAgents := filepath.Join(v.dataDir, "agents")
	legacySkills := filepath.Join(v.dataDir, "skills")
	legacyMemory := filepath.Join(v.dataDir, "memory")

	if !anyExists(legacyAgents, legacySkills, legacyMemory) {
		return nil
	}
	slog.Info("vault: migrating legacy on-disk layout into per-user vaults")

	// Agents and skills: move each leaf directory under the matching vault subdir.
	if err := v.migrateNestedDir(legacyAgents, "agents"); err != nil {
		return err
	}
	if err := v.migrateNestedDir(legacySkills, "skills"); err != nil {
		return err
	}

	// Memory: convert each user's JSONL file into markdown notes.
	mem := memory.New(v.VaultsDir())
	if users, err := os.ReadDir(legacyMemory); err == nil {
		for _, u := range users {
			if !u.IsDir() {
				continue
			}
			workspaceID := u.Name()
			_ = v.EnsureScaffold(workspaceID)
			jsonl := filepath.Join(legacyMemory, workspaceID, "memory.jsonl")
			n, err := mem.ImportJSONL(workspaceID, jsonl)
			if err != nil {
				slog.Warn("vault: memory import", "user", workspaceID, "err", err)
				continue
			}
			slog.Info("vault: migrated memory", "user", workspaceID, "entries", n)
			_ = os.Remove(jsonl)
			_ = os.Remove(filepath.Join(legacyMemory, workspaceID)) // removes if now empty
		}
		_ = os.Remove(legacyMemory) // removes if now empty
	}
	return nil
}

// migrateNestedDir moves every <legacyRoot>/<workspaceID>/<leaf> directory to
// <vault>/<workspaceID>/<subdir>/<leaf>. Existing destinations are left untouched.
func (v *Vault) migrateNestedDir(legacyRoot, subdir string) error {
	users, err := os.ReadDir(legacyRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, u := range users {
		if !u.IsDir() {
			continue
		}
		workspaceID := u.Name()
		if err := v.EnsureScaffold(workspaceID); err != nil {
			return err
		}
		destBase := filepath.Join(v.Root(workspaceID), subdir)
		if err := os.MkdirAll(destBase, 0o750); err != nil {
			return err
		}
		leaves, err := os.ReadDir(filepath.Join(legacyRoot, workspaceID))
		if err != nil {
			continue
		}
		for _, leaf := range leaves {
			src := filepath.Join(legacyRoot, workspaceID, leaf.Name())
			dst := filepath.Join(destBase, leaf.Name())
			if _, err := os.Stat(dst); err == nil {
				continue // already migrated; never clobber vault data
			}
			if err := os.Rename(src, dst); err != nil {
				slog.Warn("vault: migrate move", "src", src, "dst", dst, "err", err)
			}
		}
		_ = os.Remove(filepath.Join(legacyRoot, workspaceID)) // if now empty
	}
	_ = os.Remove(legacyRoot) // if now empty
	return nil
}

func anyExists(paths ...string) bool {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// MigrateSessionsToChats renames the legacy per-user vault `sessions/` directory
// to `chats/` and rewrites the home-note `[[sessions]]` wikilink to `[[chats]]`.
// Idempotent and safe on every startup: it only acts when a `sessions/` dir (or
// the old wikilink) still exists. Reflected notes are re-derived from the DB on
// the next chat auto-stop, so their `type: session` frontmatter is not rewritten
// here — new reflections write `type: chat`.
func (v *Vault) MigrateSessionsToChats() error {
	users, err := os.ReadDir(v.VaultsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	migrated := 0
	for _, u := range users {
		if !u.IsDir() {
			continue
		}
		workspaceID := u.Name()
		root := v.Root(workspaceID)

		// Rename sessions/ → chats/ if chats/ does not already exist.
		src := filepath.Join(root, "sessions")
		dst := filepath.Join(root, "chats")
		if _, err := os.Stat(src); err == nil {
			if _, err := os.Stat(dst); err != nil {
				if err := os.Rename(src, dst); err != nil {
					slog.Warn("vault: migrate sessions→chats", "user", workspaceID, "err", err)
				} else {
					migrated++
				}
			} else {
				// Both exist: drain sessions/ into chats/ one file at a time.
				_ = drainInto(src, dst)
			}
		}

		// Rewrite the home-note wikilink [[sessions]] → [[chats]].
		readme := filepath.Join(root, "README.md")
		if b, err := os.ReadFile(readme); err == nil {
			if updated := strings.ReplaceAll(string(b), "[[sessions]]", "[[chats]]"); updated != string(b) {
				_ = os.WriteFile(readme, []byte(updated), 0o640)
			}
		}
	}
	if migrated > 0 {
		slog.Info("vault: renamed sessions/ to chats/", "count", migrated)
	}
	return nil
}

// drainInto moves every file from srcDir into dstDir (best-effort), leaving
// srcDir empty so a later os.Remove can reclaim it. Used only when both the
// legacy sessions/ and the new chats/ already coexist.
func drainInto(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // never clobber
		}
		if err := os.Rename(src, dst); err != nil {
			slog.Warn("vault: drain sessions→chats", "src", src, "dst", dst, "err", err)
		}
	}
	_ = os.Remove(srcDir) // if now empty
	return nil
}
