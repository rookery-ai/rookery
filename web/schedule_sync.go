package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/profile"
	"github.com/rookery-ai/rookery/internal/prompts"
)

// scheduleProseTimeout bounds one pass over an agent's sentences. The pass is detached
// from the request, so without this a stuck coder would hold it forever.
const scheduleProseTimeout = 10 * time.Minute

const agentEditOpenMsg = "This agent is being edited, and that edit would save its old schedule back. Finish or cancel the edit, then change the schedule."

// apiScheduleSync reports the pass that keeps an agent's sentences in step with its
// schedule, so an owner can see what a model changed in their agent's instructions.
type apiScheduleSync struct {
	State   string                       `json:"state"` // running | done | failed
	Changes []agentdesigner.ScheduleEdit `json:"changes"`
	Review  []string                     `json:"review"`
	Refused int                          `json:"refused"`
	Error   string                       `json:"error,omitempty"`
}

type scheduleSyncEntry struct {
	status apiScheduleSync
	cancel context.CancelFunc
	gen    uint64
}

// scheduleSyncTracker holds the latest pass per agent. It lives until the next save or
// a restart, so an owner who left the page can still read what happened.
type scheduleSyncTracker struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]*scheduleSyncEntry
	// fileMu serialises read-modify-write of AGENT.md across the save paths and the
	// pass, which finishes at an arbitrary later moment.
	fileMu sync.Mutex
}

func newScheduleSyncStatus(state string) apiScheduleSync {
	return apiScheduleSync{State: state, Changes: []agentdesigner.ScheduleEdit{}, Review: []string{}}
}

// EnableScheduleProseSync turns on the model pass over an agent's sentences. NewServer
// leaves it off and only serve calls this, so a test cannot reach a real coder, and a
// paid API call, by saving a schedule.
func (s *Server) EnableScheduleProseSync() {
	s.scheduleProse = func(ctx context.Context, workspaceID, prompt string) (string, error) {
		c := s.coderForWorkspace(workspaceID)
		result, err := c.WithNoTools().Generate(ctx, workspaceID, prompt)
		if err != nil {
			return "", errors.New(agentrunner.FriendlyRunError(err, c.Name()))
		}
		return result.Text, nil
	}
}

func (s *Server) scheduleSyncStatus(agentID string) *apiScheduleSync {
	s.scheduleSync.mu.Lock()
	defer s.scheduleSync.mu.Unlock()
	entry, ok := s.scheduleSync.entries[agentID]
	if !ok {
		return nil
	}
	status := entry.status
	return &status
}

// agentEditOpen reports a live session editing this agent. It holds its own AGENT.md
// and saving it runs reconcileScheduleOnSave, which would restore the old schedule.
// Another agent's session cannot do that, so unlike a rename this does not refuse on
// any session in the workspace.
func (s *Server) agentEditOpen(workspaceID, agentID string) bool {
	if s.designFlow == nil {
		return false
	}
	sess := s.designFlow.GetSession(workspaceID)
	return sess != nil && sess.IsEdit && sess.AgentID == agentID
}

// currentCron returns the schedule the row says is in force, "" for none. A disabled
// row counts as none, matching what an edit session reconciles to.
func (s *Server) currentCron(agentID string) string {
	sched, err := s.db.GetScheduleForAgent(agentID)
	if err != nil || sched == nil || !sched.Enabled {
		return ""
	}
	return sched.CronExpr
}

// applyScheduleChange is the one path by which an owner changes a schedule: the row
// first, because it decides what actually runs, then every place that states it. A
// failure after the row committed is reported, not returned, since the schedule has
// changed. cronExpr must already be valid; "" removes the schedule. extraOld names an
// old expression the file may quote beyond what the row held.
func (s *Server) applyScheduleChange(workspaceID string, agent *db.Agent, cronExpr string, extraOld ...string) (*db.AgentSchedule, error) {
	existing, _ := s.db.GetScheduleForAgent(agent.ID)
	olds := append([]string{}, extraOld...)
	if existing != nil {
		olds = append(olds, existing.CronExpr)
	}

	var row *db.AgentSchedule
	tz := ""
	if cronExpr == "" {
		if err := s.db.DeleteAgentSchedule(agent.ID); err != nil {
			return nil, err
		}
	} else {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(cronExpr)
		if err != nil {
			return nil, err
		}
		// Store the zone the expression is meant to be read in. An owner typing
		// "0 8 * * *" means 08:00 where THEY are, not where the server is; an empty
		// profile timezone yields "" + time.Local, which is the pre-column behaviour.
		var loc *time.Location
		tz, loc = profile.ScheduleZone(s.db, workspaceID)
		nextRun := sched.Next(time.Now().In(loc))
		// Reuse existing schedule ID so ON CONFLICT(id) updates rather than inserts.
		schedID := uuid.New().String()
		if existing != nil {
			schedID = existing.ID
		}
		row = &db.AgentSchedule{
			ID:          schedID,
			AgentID:     agent.ID,
			WorkspaceID: workspaceID,
			CronExpr:    cronExpr,
			NextRunAt:   &nextRun,
			Enabled:     true,
			Timezone:    tz,
		}
		if err := s.db.UpsertAgentSchedule(row); err != nil {
			return nil, err
		}
	}

	if err := s.projectScheduleIntoFile(workspaceID, agent, cronExpr, olds); err != nil {
		slog.Warn("schedule: could not update AGENT.md", "agent_id", agent.ID, "err", err)
		s.failScheduleSync(agent.ID, "The schedule is saved, but AGENT.md could not be updated: "+err.Error())
		return row, nil
	}
	s.projectScheduleIntoDraft(workspaceID, agent.ID, cronExpr, olds)
	s.startScheduleProse(workspaceID, agent, cronExpr, tz)
	return row, nil
}

// scheduleProjection applies the two rewrites that need no judgement. oldExprs are the
// expressions the text may still quote: the row and the file header can disagree, and
// either may be what a sentence repeats.
func scheduleProjection(agentMD, cronExpr string, oldExprs []string) string {
	olds := oldExprs
	if header, ok := agentdesigner.ReadScheduleHeader(agentMD); ok {
		olds = append(append([]string{}, oldExprs...), header)
	}
	next := agentdesigner.SetScheduleHeader(agentMD, cronExpr)
	if cronExpr != "" {
		for _, old := range olds {
			next = agentdesigner.ReplaceCronLiteral(next, old, cronExpr)
		}
	}
	return next
}

func (s *Server) projectScheduleIntoFile(workspaceID string, agent *db.Agent, cronExpr string, oldExprs []string) error {
	s.scheduleSync.fileMu.Lock()
	defer s.scheduleSync.fileMu.Unlock()

	path := agentdesigner.AgentDescPath(s.agentsDir(), workspaceID, agent.ID)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // nothing states the schedule yet
	}
	if err != nil {
		return err
	}
	next := scheduleProjection(string(raw), cronExpr, oldExprs)
	if next == string(raw) {
		return nil
	}
	if err := writeAgentMDAtomic(path, next); err != nil {
		return err
	}
	s.refreshAgentDescription(agent.ID, string(raw), next)
	return nil
}

// projectScheduleIntoDraft keeps an unfinished edit from restoring the old schedule:
// approving it runs reconcileScheduleOnSave against the draft's own header.
func (s *Server) projectScheduleIntoDraft(workspaceID, agentID, cronExpr string, oldExprs []string) {
	draft, err := s.db.GetAgentDraft(workspaceID)
	if err != nil || draft == nil || !draft.IsEdit || draft.AgentID != agentID || draft.PendingAgentMD == "" {
		return
	}
	next := scheduleProjection(draft.PendingAgentMD, cronExpr, oldExprs)
	if next == draft.PendingAgentMD {
		return
	}
	if err := s.db.UpdateEditDraftAgentMD(workspaceID, agentID, next); err != nil {
		slog.Warn("schedule: could not update the edit draft", "agent_id", agentID, "err", err)
	}
}

// refreshAgentDescription keeps the agent card in step, since its description is the
// file's first paragraph and that paragraph often states when the agent runs. It reads
// the current row rather than taking the agent passed in, which may be minutes old by
// the time the model pass finishes.
func (s *Server) refreshAgentDescription(agentID, oldMD, newMD string) {
	agent, err := s.db.GetAgent(agentID)
	if err != nil {
		return
	}
	next, changed := agentdesigner.RefreshDescription(agent.Description, oldMD, newMD, agent.Name)
	if !changed {
		return
	}
	if err := s.db.UpdateAgentDescription(agentID, next); err != nil {
		slog.Warn("schedule: could not update the description", "agent_id", agentID, "err", err)
	}
}

// beginScheduleSync cancels any earlier pass and records a new one. Cancelling is safe
// because a pass is told what the file should state rather than what changed, so the
// new one covers whatever the old one had not reached.
func (s *Server) beginScheduleSync(agentID string, status apiScheduleSync, cancel context.CancelFunc) uint64 {
	t := &s.scheduleSync
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = map[string]*scheduleSyncEntry{}
	}
	if prev, ok := t.entries[agentID]; ok && prev.cancel != nil {
		prev.cancel()
	}
	t.gen++
	t.entries[agentID] = &scheduleSyncEntry{status: status, cancel: cancel, gen: t.gen}
	return t.gen
}

// finishScheduleSync records a pass's outcome, unless a newer one has replaced it.
func (s *Server) finishScheduleSync(agentID string, gen uint64, status apiScheduleSync) {
	t := &s.scheduleSync
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry, ok := t.entries[agentID]; ok && entry.gen == gen {
		entry.status = status
		entry.cancel = nil
	}
}

func (s *Server) failScheduleSync(agentID, message string) {
	status := newScheduleSyncStatus("failed")
	status.Error = message
	s.beginScheduleSync(agentID, status, nil)
}

func (s *Server) startScheduleProse(workspaceID string, agent *db.Agent, cronExpr, timezone string) {
	if s.scheduleProse == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), scheduleProseTimeout)
	gen := s.beginScheduleSync(agent.ID, newScheduleSyncStatus("running"), cancel)
	path := agentdesigner.AgentDescPath(s.agentsDir(), workspaceID, agent.ID)
	go func() {
		defer cancel()
		status := s.runScheduleProse(ctx, workspaceID, agent.ID, path, cronExpr, timezone, gen)
		if ctx.Err() == context.Canceled {
			return // replaced by a newer save, which reports for itself
		}
		s.audit.Log(workspaceID, "schedule_text_sync", "agent:"+agent.ID,
			fmt.Sprintf("%s: %d edited, %d to review, %d refused", status.State, len(status.Changes), len(status.Review), status.Refused), "")
		// Last, so a reader seeing the outcome knows the pass has finished all its work.
		s.finishScheduleSync(agent.ID, gen, status)
	}()
}

func (s *Server) scheduleSyncIsCurrent(agentID string, gen uint64) bool {
	t := &s.scheduleSync
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[agentID]
	return ok && entry.gen == gen
}

// runScheduleProse asks the model for edits and applies the ones that survive
// ApplyScheduleEdits. Any failure leaves the file exactly as it was: the header and the
// quoted cron are already right, and a stale sentence is better than a damaged one.
func (s *Server) runScheduleProse(ctx context.Context, workspaceID, agentID, path, cronExpr, timezone string, gen uint64) apiScheduleSync {
	status := newScheduleSyncStatus("done")
	fail := func(message string) apiScheduleSync {
		status.State = "failed"
		status.Error = message
		return status
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		return fail("AGENT.md could not be read: " + err.Error())
	}

	answer, err := s.scheduleProse(ctx, workspaceID, prompts.BuildScheduleProsePrompt(string(raw), cronExpr, timezone))
	if err != nil {
		slog.Warn("schedule: model pass failed", "agent_id", agentID, "err", err)
		return fail(err.Error())
	}
	edits, review, err := agentdesigner.ParseScheduleProse(answer)
	if err != nil {
		slog.Warn("schedule: model answer unreadable", "agent_id", agentID, "err", err)
		return fail("The model's answer could not be read.")
	}
	status.Review = append(status.Review, review...)

	s.scheduleSync.fileMu.Lock()
	defer s.scheduleSync.fileMu.Unlock()
	// A newer save rewrote the header and started its own pass; applying this answer
	// now would state the schedule this pass was told about, which is no longer true.
	if !s.scheduleSyncIsCurrent(agentID, gen) {
		return status
	}
	// Apply to the file as it is NOW. The owner may have edited it during the call, and
	// each edit must still match exactly once in what is actually there.
	current, err := os.ReadFile(path)
	if err != nil {
		return fail("AGENT.md could not be read: " + err.Error())
	}
	next, applied, refused := agentdesigner.ApplyScheduleEdits(string(current), edits)
	status.Refused = refused
	if len(applied) == 0 {
		return status
	}
	if err := agentdesigner.CheckEthics(next, ""); err != nil {
		slog.Warn("schedule: edits failed the safety check", "agent_id", agentID, "err", err)
		status.Refused += len(applied)
		return status
	}
	if err := writeAgentMDAtomic(path, next); err != nil {
		return fail("AGENT.md could not be updated: " + err.Error())
	}
	status.Changes = append(status.Changes, applied...)
	s.refreshAgentDescription(agentID, string(current), next)
	return status
}

func writeAgentMDAtomic(path, content string) error {
	mode := os.FileMode(0o640)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-agent-md-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// resolveAgentMDSave decides what a save from the AGENT.md card means for the schedule.
// It compares with the file, not the row: a header the owner changed is a schedule
// change, while one they left alone must not re-time an agent whose file had drifted
// just because they fixed a typo further down.
func resolveAgentMDSave(content, diskMD string) (cronExpr string, change bool, err error) {
	header, present := agentdesigner.ReadScheduleHeader(content)
	if !present {
		return "", false, nil
	}
	diskHeader, _ := agentdesigner.ReadScheduleHeader(diskMD)
	if header == diskHeader {
		return "", false, nil
	}
	if strings.EqualFold(header, "none") {
		return "", !strings.EqualFold(diskHeader, "none"), nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(header); err != nil {
		return "", false, err
	}
	return header, true, nil
}
