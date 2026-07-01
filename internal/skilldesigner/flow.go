// Package skilldesigner implements the conversational skill-creator wizard. It
// mirrors agentdesigner.Flow's shape (FSM + SSE progress + drafts + approval
// triggers + coder wrapping) but is dedicated to authoring user skills: a chat
// asks what to build, the coder (with the skill-creator core skill loaded) writes
// SKILL.md (+ optional scripts/), the flow tests them, a second text-only coder
// call (with the skill-vetter core skill loaded) audits the result for malicious
// behaviour, and on approval the skill is saved to the user's vault.
package skilldesigner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/ilijad1/simple-agents/internal/skillstore"
)

// DesignState is the current step in the conversational skill-creator wizard.
type DesignState int

const (
	StateIdle           DesignState = iota
	StateAwaitingResume             // a draft exists; waiting for user to pick "resume" or "new"
	StateDescribing                 // Telegram: waiting for a description after /skill create <name>
	StateDesigning                  // free-form Q&A until the user says "approve"
	StateVerifying                  // generated skill + tests + vetting shown; awaiting approval or change request
	StateDone
)

func (s DesignState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateAwaitingResume:
		return "awaiting_resume"
	case StateDescribing:
		return "describing"
	case StateDesigning:
		return "designing"
	case StateVerifying:
		return "verifying"
	case StateDone:
		return "done"
	}
	return "unknown"
}

// DesignSession holds all state for one in-progress skill-creation session.
type DesignSession struct {
	UserID    string
	SkillName string
	State     DesignState
	History   []db.ChatMessage // full conversation fed to the coder on every turn

	// Available skills (core + the user's own) — shown to the designer so a new
	// skill complements rather than duplicates existing ones. Loaded once on start.
	Skills             []prompts.SkillRef
	ConnectedPlatforms []string
	UserProfile        string
	UserMemory         string
	ComposioEnabled    bool
	KBManifest         string
	CreatedAt          time.Time

	// Set after generation; cleared on finalize or when the user requests changes.
	PendingSkillMD string
	PendingScripts  map[string]string
	VettingReport   string

	// pendingName holds the skill name the user originally typed in the
	// StateAwaitingResume flow, so the "new" branch can start a fresh session
	// with that name once the draft is dismissed.
	pendingName string

	// Generation cancellation and progress (same pattern as agentdesigner.Flow).
	cancelGenerate context.CancelFunc
	progressFunc   func(string)
	progressCh     chan string
}

// dbStore is the subset of *db.DB the flow needs.
type dbStore interface {
	ListSkills(userID string) ([]*db.Skill, error)
	ListUserPlatformConnections(userID string) ([]*db.PlatformConnection, error)
	GetSetting(userID, key string) (string, error)
	SecretExists(userID, name string) (bool, error)

	UpsertSkillDraft(d *db.SkillDraft) error
	GetSkillDraft(userID string) (*db.SkillDraft, error)
	DeleteSkillDraft(userID string) error
}

type memoryStore interface {
	ContextString(userID string) (string, error)
}

type kbLister interface {
	NotePaths(userID string) []string
}

// Flow manages per-user skill-design sessions and drives the FSM. Safe for
// concurrent use.
type Flow struct {
	mu       sync.Mutex
	sessions map[string]*DesignSession // keyed by userID

	coderFor      func(userID string) *coder.Coder
	saver         *SkillSaver
	db            dbStore
	memStore      memoryStore
	kb            kbLister
	secretsLoader func(ctx context.Context, userID string) (map[string]string, error)
}

// NewSkillFlow creates a Flow. coderResolver maps a userID to the right coder.
func NewSkillFlow(coderResolver func(userID string) *coder.Coder, saver *SkillSaver) *Flow {
	return &Flow{
		sessions: make(map[string]*DesignSession),
		coderFor: coderResolver,
		saver:    saver,
	}
}

func (f *Flow) WithDB(database dbStore) *Flow        { f.db = database; return f }
func (f *Flow) WithMemory(m memoryStore) *Flow       { f.memStore = m; return f }
func (f *Flow) WithKBLister(k kbLister) *Flow        { f.kb = k; return f }
func (f *Flow) WithSecretsLoader(fn func(ctx context.Context, userID string) (map[string]string, error)) *Flow {
	f.secretsLoader = fn
	return f
}

// StartDesign is the web path: creates a session already in StateDesigning with
// the user's first message and returns the coder's first response.
func (f *Flow) StartDesign(ctx context.Context, userID, skillName, firstMessage string) (string, error) {
	f.mu.Lock()
	if _, exists := f.sessions[userID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("a skill design session is already active; cancel it first")
	}
	if err := f.validateSkillName(userID, skillName); err != nil {
		f.mu.Unlock()
		return "", err
	}
	sess := f.newSession(userID, skillName, StateDesigning)
	f.sessions[userID] = sess
	f.mu.Unlock()

	return f.callCoder(ctx, userID, firstMessage)
}

// Step advances the session by one user message and returns the assistant reply.
// The (string, bool, string, error) return mirrors agentdesigner.Flow.Step for a
// consistent handler shape: reply text, done flag, created-resource-id, error.
func (f *Flow) Step(ctx context.Context, userID, input string) (string, bool, string, error) {
	f.mu.Lock()
	sess, ok := f.sessions[userID]
	if !ok {
		f.mu.Unlock()
		return "", false, "", fmt.Errorf("no active skill design session; start one first")
	}
	state := sess.State
	f.mu.Unlock()

	switch state {
	case StateAwaitingResume:
		return f.stepAwaitingResume(ctx, userID, input)
	case StateDescribing:
		return f.stepDescribing(ctx, userID, input)
	case StateDesigning:
		return f.stepDesigning(ctx, userID, input)
	case StateVerifying:
		return f.stepVerifying(ctx, userID, input)
	default:
		return "", false, "", fmt.Errorf("unexpected state: %s", state)
	}
}

// Cancel removes the user's active session without saving. If a coder subprocess
// is running, its context is cancelled. The progress channel is closed by
// runGeneration when it observes the cancellation (sole closer, like agentdesigner).
func (f *Flow) Cancel(userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[userID]
	if !ok {
		return
	}
	if sess.cancelGenerate != nil {
		sess.cancelGenerate()
	}
	delete(f.sessions, userID)
}

// SetProgressHandler stores a function called with milestone messages during the
// next generation (Telegram placeholder updates).
func (f *Flow) SetProgressHandler(userID string, fn func(string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sess, ok := f.sessions[userID]; ok {
		sess.progressFunc = fn
	}
}

// GetProgressChan returns the buffered progress channel for the user's active
// session (Web SSE). Returns (nil, false) if no session / no channel.
func (f *Flow) GetProgressChan(userID string) (<-chan string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[userID]
	if !ok || sess.progressCh == nil {
		return nil, false
	}
	return sess.progressCh, true
}

// GetSession returns the user's active session, or nil.
func (f *Flow) GetSession(userID string) *DesignSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[userID]
}

// ─── FSM step handlers ────────────────────────────────────────────────────────

func (f *Flow) stepAwaitingResume(ctx context.Context, userID, msg string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	pendingName := ""
	if sess != nil {
		pendingName = sess.pendingName
	}
	f.mu.Unlock()

	if strings.TrimSpace(strings.ToLower(msg)) == "resume" {
		return ret4(f.ResumeDraft(ctx, userID))
	}
	// "new" or anything else → dismiss the draft and start fresh.
	_ = f.DismissDraft(userID)
	f.mu.Lock()
	delete(f.sessions, userID) // drop the awaiting-resume shell so Start paths don't refuse
	f.mu.Unlock()
	if pendingName == "" {
		pendingName = "my-skill"
	}
	return ret4(f.StartDesign(ctx, userID, pendingName, "Let's build a skill."))
}

func (f *Flow) stepDescribing(ctx context.Context, userID, description string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	sess.State = StateDesigning
	f.mu.Unlock()
	return ret4(f.callCoder(ctx, userID, description))
}

func (f *Flow) stepDesigning(ctx context.Context, userID, input string) (string, bool, string, error) {
	if isApproval(input) {
		return f.runGeneration(ctx, userID)
	}
	return ret4(f.callCoder(ctx, userID, input))
}

func (f *Flow) stepVerifying(ctx context.Context, userID, input string) (string, bool, string, error) {
	if isVerifyApproval(input) {
		return f.finalizeSkill(ctx, userID)
	}
	// User wants changes — return to designing but KEEP the generated skill in
	// memory (PendingSkillMD/Scripts/VettingReport): the next approve re-generates
	// with the change context and overwrites it, but a misfire no longer silently
	// discards the whole build. Previously this cleared the pending content,
	// which lost the generated skill if the user's reply wasn't an exact
	// "approve" (e.g. "yes", "save", "ok", "approve!").
	f.mu.Lock()
	sess := f.sessions[userID]
	sess.State = StateDesigning
	f.mu.Unlock()
	return ret4(f.callCoder(ctx, userID, input))
}

// ret4 adapts a (string, error) into the step-handler 4-tuple (reply, done, id, err).
func ret4(s string, err error) (string, bool, string, error) { return s, false, "", err }

// ─── Coder conversation ───────────────────────────────────────────────────────

func (f *Flow) callCoder(ctx context.Context, userID, userMessage string) (string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	coderSvc := f.coderFor(userID)
	f.mu.Unlock()

	if coderSvc == nil {
		return "", fmt.Errorf("no coder configured for this user")
	}

	systemPrompt := prompts.BuildSkillDesignSystemPrompt(prompts.SkillDesignParams{
		SkillName:          sess.SkillName,
		AvailableSkills:    sess.Skills,
		UserProfile:        sess.UserProfile,
		UserMemory:         sess.UserMemory,
		ComposioEnabled:    sess.ComposioEnabled,
		KBManifest:         sess.KBManifest,
		ConnectedPlatforms: sess.ConnectedPlatforms,
		ChatApps:           prompts.ChatAppsForPlatforms(sess.ConnectedPlatforms),
	})

	result, err := coderSvc.WithNoTools().Chat(ctx, userID, sess.History, systemPrompt, userMessage)

	// Persist the draft on every terminal path so the conversation survives a
	// page navigation or server restart — even when the coder is rate-limited
	// (the usage-limit soft path previously returned without saving, so a
	// first turn that hit the limit left no draft at all).
	var reply string
	switch {
	case err == nil:
		reply = result.Text
	case errors.Is(err, coder.ErrUsageLimit):
		reply = fmt.Sprintf("⚠️ %s hit its usage limit. The skill design session is still active — try again in a while.", coderSvc.Name())
	default:
		f.mu.Lock()
		sess.History = append(sess.History, db.ChatMessage{Role: "user", Content: userMessage})
		f.saveDraft(sess)
		f.mu.Unlock()
		return "", fmt.Errorf("coder: %w", err)
	}

	f.mu.Lock()
	sess.History = append(sess.History,
		db.ChatMessage{Role: "user", Content: userMessage},
		db.ChatMessage{Role: "assistant", Content: reply},
	)
	f.saveDraft(sess)
	f.mu.Unlock()

	return reply, nil
}

// ─── Generation (triggered by approval) ──────────────────────────────────────

func (f *Flow) runGeneration(ctx context.Context, userID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	coderSvc := f.coderFor(userID)
	skillNameSnap := sess.SkillName
	historySnap := make([]db.ChatMessage, len(sess.History))
	copy(historySnap, sess.History)
	paramsSnap := prompts.SkillDesignParams{
		AvailableSkills:    sess.Skills,
		ConnectedPlatforms:  sess.ConnectedPlatforms,
		ChatApps:            prompts.ChatAppsForPlatforms(sess.ConnectedPlatforms),
		ComposioEnabled:    sess.ComposioEnabled,
	}

	if sess.progressCh == nil {
		sess.progressCh = make(chan string, 8)
	}
	progressCh := sess.progressCh
	progressFunc := sess.progressFunc

	genCtx, cancelGenerate := context.WithCancel(ctx)
	sess.cancelGenerate = cancelGenerate
	f.mu.Unlock()

	notify := func(msg string) {
		select {
		case progressCh <- msg:
		default:
		}
		if progressFunc != nil {
			progressFunc(msg)
		}
	}

	var progressOnce sync.Once
	closeProgress := func() {
		progressOnce.Do(func() {
			f.mu.Lock()
			if s, ok := f.sessions[userID]; ok {
				s.progressCh = nil
			}
			f.mu.Unlock()
			close(progressCh)
		})
	}

	if coderSvc == nil {
		closeProgress()
		return "", false, "", fmt.Errorf("no coder configured for this user")
	}

	notify("⚙️ Preparing skill workspace…")

	// Staging dir under the user's vault: <vault>/<userID>/skills/.staging-<name>/.
	// The live skill folder is only written in finalizeSkill after approval.
	stagingDir := skillstore.SkillDir(f.saver.SkillsDir(), userID, ".staging-"+skillNameSnap)
	_ = os.RemoveAll(stagingDir)
	if err := os.MkdirAll(filepath.Join(stagingDir, "scripts"), 0o750); err != nil {
		closeProgress()
		return "", false, "", fmt.Errorf("create staging dir: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(stagingDir) }

	skillCreatorBody, _ := skilllibrary.CoreSkillContent("skill-creator")
	prompt := prompts.BuildSkillImplementationPrompt(skillNameSnap, dbMessagesToPrompt(historySnap), skillCreatorBody, paramsSnap)

	notify("🤖 Coder is building your skill — this can take a few minutes…")

	generationCoder := coderSvc.WithDir(stagingDir).WithAllowedTools("Bash,Write,Edit,Read")
	if f.secretsLoader != nil {
		if env, err := f.secretsLoader(genCtx, userID); err == nil && len(env) > 0 {
			generationCoder = generationCoder.WithExtraEnv(env)
		}
	}
	result, err := generationCoder.Generate(genCtx, userID, prompt)
	if err != nil {
		cleanupStaging()
		closeProgress()
		if errors.Is(err, context.Canceled) {
			return "Skill creation was cancelled.", false, "", nil
		}
		if errors.Is(err, coder.ErrUsageLimit) {
			return fmt.Sprintf("⚠️ %s hit its usage limit during generation. Your skill design session is still active — try again later, or simplify what you asked for.", coderSvc.Name()), false, "", nil
		}
		if strings.Contains(err.Error(), "timed out") {
			return "⚠️ The coder timed out — the task may be too complex to build in one go. Try breaking it into simpler steps, then type approve.", false, "", nil
		}
		return "", false, "", fmt.Errorf("coder: %w", err)
	}

	if blocked := parseBlockedOutput(result.Text); blocked != "" {
		cleanupStaging()
		closeProgress()
		return "The coder ran into a blocker:\n\n" + blocked + "\n\nTell me how you'd like to proceed, or describe a different approach.", false, "", nil
	}

	notify("🔍 Validating skill safety checks…")

	// Ground truth: read what the coder actually wrote.
	skillMDBytes, err := os.ReadFile(filepath.Join(stagingDir, "SKILL.md"))
	if err != nil {
		cleanupStaging()
		closeProgress()
		return "The coder didn't create SKILL.md. Tell me what to change and I'll try again.", false, "", nil
	}
	skillMD := strings.TrimSpace(string(skillMDBytes))

	scripts, err := readScriptsFromDisk(filepath.Join(stagingDir, "scripts"))
	if err != nil {
		cleanupStaging()
		closeProgress()
		return "", false, "", fmt.Errorf("read scripts: %w", err)
	}

	// Guardrails on the actual content the coder wrote.
	if err := agentdesigner.CheckEthics(skillMD, ""); err != nil {
		cleanupStaging()
		closeProgress()
		return fmt.Sprintf("The skill failed safety checks: %s\n\nPlease rephrase or simplify.", err.Error()), false, "", nil
	}
	for filename, code := range scripts {
		if err := agentdesigner.RunToolGuardrails(filename, code); err != nil {
			cleanupStaging()
			closeProgress()
			return fmt.Sprintf("The generated script %s didn't pass validation: %s\n\nType **approve** to rebuild, or tell me what to change.", filename, err.Error()), false, "", nil
		}
	}

	notify("🧪 Testing scripts…")
	testOut := f.runTests(stagingDir, scripts, parseTestOutput(result.Text))

	notify("🔒 Security vetting the skill…")
	report := f.vetSkill(ctx, userID, coderSvc, skillNameSnap, skillMD, scripts)

	closeProgress()

	// A blocked vetting verdict (🔴/⛔ or "do not save") keeps the user in the
	// design state to revise — the skill is NOT saved.
	if vettingBlocksSave(report) {
		f.mu.Lock()
		sess = f.sessions[userID]
		sess.State = StateDesigning
		sess.PendingSkillMD = skillMD
		sess.PendingScripts = scripts
		sess.VettingReport = report
		f.saveDraft(sess)
		f.mu.Unlock()
		cleanupStaging()
		return fmt.Sprintf(
			"🔒 The security vetting **blocked** this skill from being saved.\n\n---\n%s\n---\n\nPlease revise the flagged issues and type **approve** to rebuild, or describe what you'll change.",
			report,
		), false, "", nil
	}

	// Verified + vetted — move to StateVerifying for the user to approve or revise.
	f.mu.Lock()
	sess = f.sessions[userID]
	sess.State = StateVerifying
	sess.PendingSkillMD = skillMD
	sess.PendingScripts = scripts
	sess.VettingReport = report
	f.saveDraft(sess)
	f.mu.Unlock()

	return fmt.Sprintf(
		"Here's the generated skill and how it tested:\n\n---\n%s\n---\n\n🔒 Vetting report:\n%s\n\nDoes this look right? Type **approve** to save the skill, or tell me what to change.",
		testOut, report,
	), false, "", nil
}

// runTests smoke-runs each script (py_compile to confirm no syntax errors) and
// combines the result with the coder's own [TEST_OUTPUT]. For prompt-only skills
// (no scripts) it synthesizes a frontmatter-validation note so a verifying
// preview is always present.
func (f *Flow) runTests(stagingDir string, scripts map[string]string, coderTestOut string) string {
	var sb strings.Builder
	if coderTestOut != "" {
		sb.WriteString("Coder test output:\n")
		sb.WriteString(coderTestOut)
		sb.WriteString("\n\n")
	}
	if len(scripts) == 0 {
		if sb.Len() == 0 {
			sb.WriteString("Prompt-only skill — no scripts to run. Validated frontmatter parses and the description reads as a trigger.\n")
		}
		return strings.TrimSpace(sb.String())
	}
	sb.WriteString("Script smoke check (py_compile):\n")
	for _, name := range sortedScriptNames(scripts) {
		path := filepath.Join(stagingDir, "scripts", name)
		cmd := exec.Command("python3", "-m", "py_compile", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			sb.WriteString(fmt.Sprintf("- ❌ %s: %s\n", name, strings.TrimSpace(string(out))))
		} else {
			sb.WriteString(fmt.Sprintf("- ✅ %s: compiles cleanly\n", name))
		}
	}
	return strings.TrimSpace(sb.String())
}

// vetSkill runs a second, text-only coder call with the skill-vetter core skill
// as the system prompt over the generated SKILL.md + every script. Returns the
// vetting report text ("" if the audit itself failed — treated as non-blocking).
func (f *Flow) vetSkill(ctx context.Context, userID string, coderSvc *coder.Coder, skillName, skillMD string, scripts map[string]string) string {
	if coderSvc == nil {
		return ""
	}
	vetterBody, _ := skilllibrary.CoreSkillContent("skill-vetter")
	// The vetter protocol is the system prompt; the skill-under-review + task is
	// the user message (vetterBody passed empty to avoid double-injecting it).
	userMsg := prompts.BuildSkillVettingPrompt(skillName, skillMD, scripts, "")
	result, err := coderSvc.WithNoTools().Chat(ctx, userID, nil, vetterBody, userMsg)
	if err != nil {
		slog.Warn("skilldesigner: vetting coder call failed", "user_id", userID, "skill", skillName, "err", err)
		return "⚠️ Security vetting could not run automatically. Review the skill manually before approving."
	}
	return strings.TrimSpace(result.Text)
}

// vettingBlocksSave returns true if the vetting report carries a blocking
// verdict. It parses the authoritative "Verdict:" line rather than scanning the
// whole report for emoji: the skill-vetter format offers verdict options as
// "✅ safe to save | ⚠️ save with caution | ❌ do not save", and a literal model
// could echo the whole alternation — scanning for ❌ would then falsely block a
// safe skill. A pure block verdict ("Verdict: ❌ do not save") contains "do not
// save" but NOT "safe to save"; an echoed alternation contains both. A missing or
// ambiguous report does NOT block (the report is still shown to the user, who
// can decline to approve).
func vettingBlocksSave(report string) bool {
	if strings.TrimSpace(report) == "" {
		return false
	}
	for _, line := range strings.Split(report, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(l, "verdict:") {
			if strings.Contains(l, "do not save") && !strings.Contains(l, "safe to save") {
				return true
			}
			return false
		}
	}
	return false
}

// finalizeSkill saves the pending skill to the user's vault + DB and cleans up.
func (f *Flow) finalizeSkill(ctx context.Context, userID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	skillMD := sess.PendingSkillMD
	scripts := sess.PendingScripts
	skillName := sess.SkillName
	f.mu.Unlock()

	if skillMD == "" {
		return "", false, "", fmt.Errorf("no pending skill to save")
	}

	meta, _ := skilllibrary.ParseMeta(skillMD)
	name := meta.Name
	if name == "" {
		name = skillName
	}
	description := meta.Description
	if description == "" {
		description = "User-created skill: " + name
	}

	skill, err := f.saver.SaveSkill(userID, name, description, skillMD, scripts)
	if err != nil {
		return "", false, "", fmt.Errorf("save skill: %w", err)
	}

	// Saved — drop the draft and the session.
	f.deleteDraft(userID)
	f.mu.Lock()
	delete(f.sessions, userID)
	f.mu.Unlock()

	return fmt.Sprintf("✅ Saved skill **%s**. You can now assign it to agents (it'll appear in their available skills).", name), true, skill.ID, nil
}

// ─── Draft save / resume ──────────────────────────────────────────────────────

const draftTTL = 7 * 24 * time.Hour

func (f *Flow) saveDraft(sess *DesignSession) {
	if f.db == nil {
		return
	}
	histJSON, _ := json.Marshal(sess.History)
	scriptsJSON, _ := json.Marshal(sess.PendingScripts)
	state := "designing"
	if sess.State == StateVerifying {
		state = "verifying"
	}
	_ = f.db.UpsertSkillDraft(&db.SkillDraft{
		UserID:             sess.UserID,
		SkillName:          sess.SkillName,
		State:              state,
		HistoryJSON:        string(histJSON),
		PendingSkillMD:     sess.PendingSkillMD,
		PendingScriptsJSON: string(scriptsJSON),
		VettingReport:      sess.VettingReport,
		ExpiresAt:          time.Now().Add(draftTTL),
	})
}

func (f *Flow) deleteDraft(userID string) {
	if f.db == nil {
		return
	}
	_ = f.db.DeleteSkillDraft(userID)
}

// HasDraft returns the user's skill draft if one exists and is not expired.
func (f *Flow) HasDraft(userID string) *db.SkillDraft {
	if f.db == nil {
		return nil
	}
	draft, err := f.db.GetSkillDraft(userID)
	if err != nil {
		return nil
	}
	return draft
}

// DismissDraft deletes the user's draft and any orphaned staging directory.
func (f *Flow) DismissDraft(userID string) error {
	if f.db == nil {
		return nil
	}
	draft := f.HasDraft(userID)
	if draft == nil {
		return nil
	}
	_ = f.db.DeleteSkillDraft(userID)
	if draft.SkillName != "" {
		_ = os.RemoveAll(skillstore.SkillDir(f.saver.SkillsDir(), userID, ".staging-"+draft.SkillName))
	}
	return nil
}

// OfferDraftResume creates a minimal session in StateAwaitingResume and returns
// the prompt to send the user.
func (f *Flow) OfferDraftResume(userID, pendingSkillName string, draft *db.SkillDraft) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[userID] = &DesignSession{
		UserID:      userID,
		SkillName:   draft.SkillName,
		State:       StateAwaitingResume,
		pendingName: pendingSkillName,
		CreatedAt:   time.Now(),
	}
	return fmt.Sprintf(
		"Found an unfinished skill draft for \"%s\". Reply 'resume' to continue it, or 'new' to start fresh.",
		draft.SkillName,
	), nil
}

// ResumeDraft reconstructs a session from the saved draft and returns the message
// to show the user. Derived context (Skills, platforms, profile, memory) is
// reloaded. The coder is never re-run on resume.
func (f *Flow) ResumeDraft(ctx context.Context, userID string) (string, error) {
	if f.db == nil {
		return "", fmt.Errorf("no database configured")
	}
	draft, err := f.db.GetSkillDraft(userID)
	if err != nil {
		return "", fmt.Errorf("no skill draft to resume")
	}

	sess := f.newSession(userID, draft.SkillName, StateDesigning)
	_ = json.Unmarshal([]byte(draft.HistoryJSON), &sess.History)
	if draft.PendingScriptsJSON != "" {
		_ = json.Unmarshal([]byte(draft.PendingScriptsJSON), &sess.PendingScripts)
	}
	sess.PendingSkillMD = draft.PendingSkillMD
	sess.VettingReport = draft.VettingReport

	if draft.State == "verifying" {
		sess.State = StateVerifying
	} else {
		sess.State = StateDesigning
	}

	f.mu.Lock()
	f.sessions[userID] = sess
	f.mu.Unlock()

	if sess.State == StateVerifying {
		preview := sess.PendingSkillMD
		if len(preview) > 600 {
			preview = preview[:600] + "…"
		}
		return fmt.Sprintf(
			"Resuming your draft for **%s**. The coder already built this version:\n\n```\n%s\n```\n\nType `approve` to save it, or describe any changes you'd like.",
			sess.SkillName, preview,
		), nil
	}
	return fmt.Sprintf(
		"Resuming your draft for **%s**. Here's the conversation so far — continue, or type 'approve' when ready to generate.",
		sess.SkillName,
	), nil
}

// ─── session + context loading ────────────────────────────────────────────────

func (f *Flow) newSession(userID, skillName string, state DesignState) *DesignSession {
	return &DesignSession{
		UserID:             userID,
		SkillName:          skillName,
		State:              state,
		Skills:             f.loadSkillNames(userID),
		ConnectedPlatforms: f.loadConnectedPlatforms(userID),
		UserProfile:        f.loadUserProfile(userID),
		UserMemory:         f.loadUserMemory(userID),
		ComposioEnabled:    f.loadComposioEnabled(userID),
		KBManifest:        f.loadKBManifest(userID),
		CreatedAt:          time.Now(),
	}
}

// validateSkillName rejects reserved core-skill names and empty/invalid names.
func (f *Flow) validateSkillName(userID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("give the skill a name first")
	}
	if skilllibrary.IsCoreSkill(name) {
		return fmt.Errorf("%q is a reserved core-skill name; choose a different name", name)
	}
	return nil
}

func (f *Flow) loadSkillNames(userID string) []prompts.SkillRef {
	refs := make([]prompts.SkillRef, 0, 16)
	for _, s := range skilllibrary.LoadBundled() {
		refs = append(refs, prompts.SkillRef{Name: s.Name, Description: s.Description})
	}
	if f.db == nil {
		return refs
	}
	skills, _ := f.db.ListSkills(userID)
	for _, s := range skills {
		refs = append(refs, prompts.SkillRef{Name: s.Name, Description: s.Description})
	}
	return refs
}

func (f *Flow) loadConnectedPlatforms(userID string) []string {
	if f.db == nil {
		return nil
	}
	conns, err := f.db.ListUserPlatformConnections(userID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.Platform)
	}
	return out
}

func (f *Flow) loadUserProfile(userID string) string {
	if f.db == nil {
		return ""
	}
	p := profile.Load(f.db, userID)
	return p.ContextString()
}

func (f *Flow) loadUserMemory(userID string) string {
	if f.memStore == nil {
		return ""
	}
	mem, err := f.memStore.ContextString(userID)
	if err != nil {
		return ""
	}
	return mem
}

func (f *Flow) loadComposioEnabled(userID string) bool {
	if f.db == nil {
		return false
	}
	ok, err := f.db.SecretExists(userID, "COMPOSIO_API_KEY")
	return err == nil && ok
}

func (f *Flow) loadKBManifest(userID string) string {
	if f.kb == nil {
		return ""
	}
	paths := f.kb.NotePaths(userID)
	if len(paths) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range paths {
		if i >= 30 {
			fmt.Fprintf(&sb, "- …and %d more\n", len(paths)-30)
			break
		}
		sb.WriteString("- ")
		sb.WriteString(p)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func isApproval(input string) bool {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimRight(s, ".!?,;:)")
	switch s {
	case "approve", "go ahead", "build it", "create it", "/approve":
		return true
	}
	return false
}

// isVerifyApproval is the forgiving approval test used in StateVerifying, where
// the skill is already built and the user is only confirming whether to save it.
// Common confirmations ("yes", "save", "ok", "looks good") count so a natural
// reply saves the build instead of being treated as a change request that
// discards it. A negative cue ("don't", "not yet", "change", "wait", "instead")
// means the user wants changes, not approval. isApproval (used in StateDesigning)
// stays strict so a casual "ok"/"yes" while answering design questions doesn't
// launch a full generation run.
func isVerifyApproval(input string) bool {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimRight(s, ".!?,;:)")
	if strings.Contains(s, "don't") || strings.Contains(s, "do not") ||
		strings.Contains(s, "not yet") || strings.Contains(s, "change") ||
		strings.Contains(s, "wait") || strings.Contains(s, "instead") {
		return false
	}
	switch s {
	case "approve", "go ahead", "build it", "create it", "/approve",
		"yes", "save", "save it", "ok", "okay", "looks good", "looks good to me",
		"confirm", "confirmed", "go", "do it", "ship it", "lgtm", "perfect", "great":
		return true
	}
	return false
}

func dbMessagesToPrompt(msgs []db.ChatMessage) []prompts.ChatMessage {
	out := make([]prompts.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = prompts.ChatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

func parseTestOutput(text string) string {
	start := strings.Index(text, "[TEST_OUTPUT]")
	if start < 0 {
		return ""
	}
	start += len("[TEST_OUTPUT]")
	end := strings.Index(text[start:], "[/TEST_OUTPUT]")
	if end < 0 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}

func parseBlockedOutput(text string) string {
	start := strings.Index(text, "[BLOCKED]")
	if start < 0 {
		return ""
	}
	start += len("[BLOCKED]")
	end := strings.Index(text[start:], "[/BLOCKED]")
	if end < 0 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}

// readScriptsFromDisk reads every .py file under scriptsDir (one level) as a
// relpath→content map. Non-.py files are ignored.
func readScriptsFromDisk(scriptsDir string) (map[string]string, error) {
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	scripts := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(scriptsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		scripts[e.Name()] = string(data)
	}
	return scripts, nil
}

func sortedScriptNames(scripts map[string]string) []string {
	names := make([]string, 0, len(scripts))
	for n := range scripts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}