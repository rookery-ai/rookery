package agentdesigner

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
)

// legacyManifest is a private, minimal decode of agent.json used only by the
// migration. It deliberately does not depend on AgentManifest/LoadManifest —
// those are deleted in a later task, and the migration must survive that.
type legacyManifest struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Skills []string `json:"skills"`
}

// MigrateAgentFilesToMarkdown is the idempotent startup migration that:
//
//  1. Converts each agent's state.json to state.md (the new markdown state
//     format from Task 1), verifying the round-trip before ever deleting the
//     original — losing an agent's state is the one unacceptable outcome here.
//  2. Absorbs the one-time skills-attachment reconciliation that used to be
//     ReconcileSkillAttachmentsToDB: it must run BEFORE agent.json is deleted,
//     since that's the only place manifest.Skills lives.
//  3. Deletes agent.json once its skills (if any) have been safely reconciled
//     into the DB (AGENT.md/agent.json are for the LLM; the DB is the record).
//
// It walks <vaultsBase>/<workspaceID>/agents/*/, including draft_<slug> dirs —
// drafts migrate on the same terms as canonical agents/<uuid> dirs. Safe to
// call on every boot: once state.md exists and agent.json is gone, an agent
// dir is a no-op on subsequent runs.
func MigrateAgentFilesToMarkdown(database skillDB, vaultsBase string, coreSkillNames []string) (int, error) {
	coreSet := make(map[string]bool, len(coreSkillNames))
	for _, n := range coreSkillNames {
		coreSet[n] = true
	}

	workspaceDirs, err := os.ReadDir(vaultsBase)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	touched := 0
	for _, wd := range workspaceDirs {
		if !wd.IsDir() {
			continue
		}
		workspaceID := wd.Name()
		agentEntries, err := os.ReadDir(filepath.Join(vaultsBase, workspaceID, "agents"))
		if err != nil {
			continue // no agents dir for this workspace
		}
		for _, ae := range agentEntries {
			if !ae.IsDir() {
				continue
			}
			agentDir := filepath.Join(vaultsBase, workspaceID, "agents", ae.Name())
			if migrateOneAgentDir(database, agentDir, ae.Name(), coreSet) {
				touched++
			}
		}
	}

	return touched, nil
}

// migrateOneAgentDir performs both the state and manifest migration steps for
// a single agent directory. It returns true if anything was actually changed
// on disk (used only for the returned count / logging — every step is
// independently safe to retry, so a false return is never a signal of error).
func migrateOneAgentDir(database skillDB, agentDir, agentID string, coreSet map[string]bool) bool {
	changed := false
	if migrateAgentState(agentDir, agentID) {
		changed = true
	}
	if migrateAgentManifest(database, agentDir, agentID, coreSet) {
		changed = true
	}
	return changed
}

// migrateAgentState converts state.json -> state.md for one agent dir.
//
// Only acts when state.json exists AND state.md does not: a pre-existing
// state.md means either an already-migrated agent, or a state.md created
// directly by the new code path — either way state.json (if still present)
// is left alone rather than guessed at.
//
// The write is verified before the source is ever removed: after WriteState,
// ReadState is called on the same file and deep-compared against the original
// parsed object. Any mismatch, or any error at any step, aborts leaving BOTH
// files in place and logs loudly — never silently drop state.
func migrateAgentState(agentDir, agentID string) bool {
	statePath := filepath.Join(agentDir, "state.json")
	mdPath := filepath.Join(agentDir, "state.md")

	if _, err := os.Stat(mdPath); err == nil {
		return false // already has state.md; do not touch state.json
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			// Unreadable is not the same as absent: a permissions problem would
			// otherwise block this agent's migration silently and forever.
			slog.Error("agent state migration: state.json unreadable; leaving it in place",
				"agent", agentID, "path", statePath, "err", err)
		}
		return false
	}

	// UseNumber here is what the verify-then-delete gate below depends on: the
	// legacy state.json is the ONE source of truth for the original values, so
	// if this first decode already rounds a large integer to float64, the
	// later reflect.DeepEqual(original, readBack) compares two equally-rounded
	// values and passes — the migration would report success and delete
	// state.json having already lost precision before the "verify" step ever
	// ran. Decoding both sides with UseNumber keeps the comparison honest.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var original map[string]any
	if err := dec.Decode(&original); err != nil {
		slog.Error("agent state migration: state.json is not valid JSON; leaving both files",
			"agent", agentID, "path", statePath, "err", err)
		return false
	}
	if original == nil {
		original = map[string]any{}
	}

	agentName := agentID
	if m, ok := loadLegacyManifestQuiet(agentDir); ok && m.Name != "" {
		agentName = m.Name
	}

	if err := WriteState(mdPath, agentName, original); err != nil {
		slog.Error("agent state migration: WriteState failed; leaving both files",
			"agent", agentID, "path", mdPath, "err", err)
		return false
	}

	readBack, err := ReadState(mdPath)
	if err != nil {
		slog.Error("agent state migration: verify-read of state.md failed; leaving both files",
			"agent", agentID, "path", mdPath, "err", err)
		return false
	}

	if !reflect.DeepEqual(original, readBack) {
		slog.Error("agent state migration: state.md verify mismatch after write; leaving both files",
			"agent", agentID, "path", mdPath)
		return false
	}

	if err := os.Remove(statePath); err != nil {
		slog.Error("agent state migration: verified state.md but failed to remove state.json; leaving both files",
			"agent", agentID, "path", statePath, "err", err)
		return false
	}

	return true
}

// loadLegacyManifestQuiet is a best-effort read of agent.json's name field,
// used only to pick a nicer heading for a freshly-created state.md. Any
// failure is silently ignored — the agent ID is a fine fallback.
func loadLegacyManifestQuiet(agentDir string) (legacyManifest, bool) {
	data, err := os.ReadFile(filepath.Join(agentDir, "agent.json"))
	if err != nil {
		return legacyManifest{}, false
	}
	var m legacyManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return legacyManifest{}, false
	}
	return m, true
}

// migrateAgentManifest reconciles manifest.Skills into the agent_skills DB
// table (exactly as ReconcileSkillAttachmentsToDB used to) and then deletes
// agent.json. Skipped entirely when agent.json does not exist.
func migrateAgentManifest(database skillDB, agentDir, agentID string, coreSet map[string]bool) bool {
	manifestPath := filepath.Join(agentDir, "agent.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("agent manifest migration: agent.json unreadable; leaving it in place",
				"agent", agentID, "path", manifestPath, "err", err)
		}
		return false // absent — nothing to reconcile/delete
	}

	var m legacyManifest
	if err := json.Unmarshal(data, &m); err != nil {
		slog.Error("agent manifest migration: agent.json is not valid JSON; leaving it in place",
			"agent", agentID, "path", manifestPath, "err", err)
		return false
	}

	if len(m.Skills) > 0 {
		existing, _ := database.ListAgentSkillNames(agentID)
		if len(existing) == 0 && !manifestIsFallbackBloat(m.Skills, coreSet) {
			if err := database.SetAgentSkills(agentID, m.Skills); err != nil {
				slog.Error("agent manifest migration: SetAgentSkills failed; leaving agent.json in place",
					"agent", agentID, "err", err)
				return false
			}
		}
	}

	if err := os.Remove(manifestPath); err != nil {
		slog.Error("agent manifest migration: failed to remove agent.json after reconciling skills",
			"agent", agentID, "path", manifestPath, "err", err)
		return false
	}

	return true
}
