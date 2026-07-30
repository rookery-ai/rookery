import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { AlertTriangle, Check } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { ContextPaneHeader } from "@/components/shell/ContextPaneParts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { CuratedSelect } from "@/components/profile/CuratedSelect";
import { timezoneOptions } from "@/components/profile/options";
import { cn } from "@/lib/utils";
import { entityIcon } from "@/lib/entityIcons";
import { ApiError } from "@/lib/api";
import { useTheme } from "@/theme";
import {
  useSettings,
  useSaveProfile,
  useSaveWorkspaceMeta,
  useChangeMasterPassword,
  type Profile,
  type WorkspaceMeta,
} from "@/lib/settings";
import { ProviderCards } from "./ProviderCards";
import { CoderSection } from "./CoderSection";
import { OwnerGate } from "./OwnerGate";
import {
  AuditLogSection, InstanceURLSection, SystemStatusSection, WorkspacesSection,
} from "./OwnerSections";
import { BackupSection } from "./BackupSection";

// Section navigation is driven by a `?section=` query param (not scroll
// anchors) — plain to unit-test (assert the param + the rendered section)
// and avoids IntersectionObserver plumbing for highlighting the active item.
// Two groups, eleven sections. The Owner area used to be ONE entry that
// stacked five sub-sections inside it, which is why it read as cluttered and
// gave no signal about which part you were looking at. Each owner sub-section
// is now a first-class entry with its own page, and the pane highlight is the
// "where am I" answer.
//
// Icons come from the shared entity map, replacing the emoji strings this list
// used to carry — the one place in the app that diverged from monochrome
// lucide, and the reason settings looked coloured while everything else
// looked grey.
const SECTION_GROUPS = [
  {
    label: "Workspace",
    sections: [
      { slug: "profile", label: "Profile" },
      { slug: "workspace", label: "Workspace" },
      { slug: "ai-providers", label: "AI Providers" },
      { slug: "coder", label: "Coder" },
      { slug: "master-password", label: "Master password" },
      { slug: "appearance", label: "Appearance" },
    ],
  },
  {
    label: "Owner",
    sections: [
      { slug: "owner-workspaces", label: "Workspaces" },
      { slug: "owner-instance-url", label: "Instance URL" },
      { slug: "owner-system", label: "System status" },
      { slug: "owner-backup", label: "Backup" },
      { slug: "owner-audit", label: "Audit log" },
    ],
  },
] as const;

type SectionSlug = (typeof SECTION_GROUPS)[number]["sections"][number]["slug"];

// Flattened for slug validation. Typed explicitly rather than inferred: a
// flatMap over a readonly tuple-of-tuples narrows each group to its own
// literal shape and then refuses to unify them.
const SECTIONS: readonly { slug: SectionSlug; label: string }[] = SECTION_GROUPS.flatMap(
  (g) => g.sections as readonly { slug: SectionSlug; label: string }[],
);
const DEFAULT_SECTION: SectionSlug = "profile";

// ?section=owner used to render all five owner sub-sections stacked on one
// page. Redirect it so existing links and bookmarks still land somewhere real
// rather than silently falling back to Profile.
const LEGACY_SECTION_ALIASES: Record<string, SectionSlug> = {
  owner: "owner-workspaces",
};

function isSectionSlug(v: string | null): v is SectionSlug {
  return SECTIONS.some((s) => s.slug === v);
}

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="mb-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {message}
    </div>
  );
}

function SavedChip({ show, label = "Saved" }: { show: boolean; label?: string }) {
  if (!show) return null;
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-ok-soft px-2 py-0.5 text-xs font-medium text-ok">
      <Check className="size-3" /> {label}
    </span>
  );
}

// ── Profile ──────────────────────────────────────────────────────────────

const EMPTY_PROFILE: Profile = {
  display_name: "",
  email: "",
  location: "",
  timezone: "",
  tone: "",
  language: "",
  notes: "",
};

function ProfileSection({ profile }: { profile: Profile | undefined }) {
  const [form, setForm] = useState<Profile>(EMPTY_PROFILE);
  // Built once: timezoneOptions walks the platform tzdb (~400 entries), which
  // should not re-run on each keystroke elsewhere in the form.
  const timezones = useMemo(() => timezoneOptions(), []);
  const [saved, setSaved] = useState(false);
  const save = useSaveProfile();

  useEffect(() => {
    if (profile) setForm(profile);
  }, [profile]);

  function set<K extends keyof Profile>(key: K, value: Profile[K]) {
    setForm((f) => ({ ...f, [key]: value }));
    setSaved(false);
  }

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaved(false);
    try {
      await save.mutateAsync({ display_name: form.display_name, timezone: form.timezone });
      setSaved(true);
    } catch {
      // surfaced below via save.error
    }
  }

  return (
    <section>
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-bold">Profile</h2>
        <SavedChip show={saved} />
      </div>
      <p className="mt-1 text-sm text-muted-2">
Your name and timezone. Your timezone is what makes “remind me next Tuesday at
        3pm” land at the right moment.
      </p>

      {save.isError && <div className="mt-4"><ErrorBanner message={errMessage(save.error)} /></div>}

      <form onSubmit={(e) => void handleSave(e)} className="mt-4 max-w-lg space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="display_name">Display name</Label>
          <Input
            id="display_name"
            value={form.display_name}
            onChange={(e) => set("display_name", e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="timezone">Timezone</Label>
          <CuratedSelect
            id="timezone"
            value={form.timezone}
            onChange={(v) => set("timezone", v)}
            options={timezones}
          />
        </div>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save profile"}
        </Button>
      </form>

      <p className="mt-4 max-w-lg text-sm text-muted-2">
        Your background, tone and language live in your knowledge base, in{" "}
        <code className="rounded bg-chrome px-1 py-0.5 text-xs">memory/ABOUT.md</code> and{" "}
        <code className="rounded bg-chrome px-1 py-0.5 text-xs">memory/STYLE.md</code>. Editing
        them there is what changes how your assistant talks to you — there is no second copy
        here to fall out of step with them.
      </p>
    </section>
  );
}

// ── Workspace ────────────────────────────────────────────────────────────

function WorkspaceSection({ workspace }: { workspace: WorkspaceMeta | undefined }) {
  const [form, setForm] = useState<WorkspaceMeta>({ name: "", about: "" });
  const [saved, setSaved] = useState(false);
  const [nameError, setNameError] = useState("");
  const save = useSaveWorkspaceMeta();

  useEffect(() => {
    if (workspace) setForm(workspace);
  }, [workspace]);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaved(false);
    if (!form.name.trim()) {
      setNameError("Workspace name is required");
      return;
    }
    setNameError("");
    try {
      await save.mutateAsync({ name: form.name });
      setSaved(true);
    } catch {
      // surfaced below via save.error
    }
  }

  return (
    <section>
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-bold">Workspace</h2>
        <SavedChip show={saved} />
      </div>
      <p className="mt-1 text-sm text-muted-2">The workspace name shown across the app.</p>

      {save.isError && <div className="mt-4"><ErrorBanner message={errMessage(save.error)} /></div>}

      <form onSubmit={(e) => void handleSave(e)} className="mt-4 max-w-lg space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="ws_name">Name</Label>
          <Input
            id="ws_name"
            value={form.name}
            aria-invalid={!!nameError}
            onChange={(e) => {
              setForm((f) => ({ ...f, name: e.target.value }));
              setSaved(false);
              if (nameError) setNameError("");
            }}
          />
          {nameError && <p className="text-xs text-danger">{nameError}</p>}
        </div>
        <div className="space-y-1.5">
          <Label>About Workspace</Label>
          {workspace?.about ? (
            <p className="rounded-md border border-border bg-chrome/50 p-3 text-sm whitespace-pre-wrap">
              {workspace.about}
            </p>
          ) : (
            <p className="text-sm text-muted-2">Not set.</p>
          )}
          <p className="text-xs text-muted-2">
            Read-only here. This is what your agents and chat are told the workspace is for —
            edit it in{" "}
            <code className="rounded bg-chrome px-1 py-0.5">memory/ABOUT.md</code> in your
            knowledge base.
          </p>
        </div>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save workspace"}
        </Button>
      </form>
    </section>
  );
}

// ── Appearance ───────────────────────────────────────────────────────────

const APPEARANCE_OPTIONS = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
] as const;

function AppearanceSection() {
  const { theme, setTheme } = useTheme();

  return (
    <section>
      <h2 className="text-lg font-bold">Appearance</h2>
      <p className="mt-1 text-sm text-muted-2">Applies instantly — no save needed.</p>

      <div className="mt-4 grid max-w-lg grid-cols-3 gap-3" role="radiogroup" aria-label="Appearance">
        {APPEARANCE_OPTIONS.map((opt) => (
          <label
            key={opt.value}
            className={cn(
              "flex cursor-pointer flex-col items-center gap-2 rounded-lg border p-4 text-sm font-medium transition-colors",
              theme === opt.value
                ? "border-primary bg-primary/5 text-foreground"
                : "border-border text-muted-2 hover:border-primary/40",
            )}
          >
            <input
              type="radio"
              name="appearance"
              value={opt.value}
              checked={theme === opt.value}
              onChange={() => setTheme(opt.value)}
              className="sr-only"
            />
            {opt.label}
          </label>
        ))}
      </div>
    </section>
  );
}

// ── Master password ─────────────────────────────────────────────────────

function MasterPasswordSection() {
  const [current, setCurrent] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [mismatchError, setMismatchError] = useState("");
  const [saved, setSaved] = useState(false);
  const change = useChangeMasterPassword();

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaved(false);
    if (newPassword !== confirm) {
      setMismatchError("New passwords do not match");
      return;
    }
    setMismatchError("");
    try {
      await change.mutateAsync({ current, new_password: newPassword, confirm });
      setCurrent("");
      setNewPassword("");
      setConfirm("");
      setSaved(true);
    } catch {
      // surfaced below via change.error
    }
  }

  return (
    <section>
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-bold">Master password</h2>
        <SavedChip show={saved} label="Changed" />
      </div>
      <p className="mt-1 text-sm text-muted-2">
        Protects this workspace's secrets. You'll re-enter it whenever you switch into this
        workspace.
      </p>

      {mismatchError && <div className="mt-4"><ErrorBanner message={mismatchError} /></div>}
      {change.isError && !mismatchError && (
        <div className="mt-4">
          <ErrorBanner message={errMessage(change.error)} />
        </div>
      )}

      <form onSubmit={(e) => void handleSave(e)} className="mt-4 max-w-lg space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="current_pw">Current master password</Label>
          <Input
            id="current_pw"
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="new_pw">New master password</Label>
          <Input
            id="new_pw"
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="confirm_pw">Confirm new master password</Label>
          <Input
            id="confirm_pw"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </div>
        <Button type="submit" disabled={change.isPending}>
          {change.isPending ? "Changing…" : "Change master password"}
        </Button>
      </form>
    </section>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────

export default function SettingsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawSection = searchParams.get("section");
  // A legacy ?section=owner link resolves to the first owner section rather
  // than silently falling back to Profile, which is what an unrecognised slug
  // would otherwise do.
  const aliased = rawSection !== null ? LEGACY_SECTION_ALIASES[rawSection] : undefined;
  const section: SectionSlug = aliased ?? (isSectionSlug(rawSection) ? rawSection : DEFAULT_SECTION);

  // Rewrite the URL for an aliased slug so the address bar, a copied link and a
  // reload all agree on where you are.
  useEffect(() => {
    if (!aliased) return;
    const next = new URLSearchParams(searchParams);
    next.set("section", aliased);
    setSearchParams(next, { replace: true });
  }, [aliased, searchParams, setSearchParams]);

  const { data: settings, isLoading, isError, error } = useSettings();

  function goTo(slug: SectionSlug) {
    const next = new URLSearchParams(searchParams);
    next.set("section", slug);
    setSearchParams(next);
  }

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <ContextPaneHeader title="Settings" />
          <nav className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-3">
            {SECTION_GROUPS.map((group) => (
              <div key={group.label} className="flex flex-col gap-1">
                <h3 className="px-1 pb-1 text-xs font-bold uppercase tracking-wide text-muted-2">
                  {group.label}
                </h3>
                {group.sections.map((s) => {
                  const Icon = entityIcon(s.slug);
                  return (
                    <button
                      key={s.slug}
                      type="button"
                      onClick={() => goTo(s.slug)}
                      aria-current={section === s.slug ? "page" : undefined}
                      className={cn(
                        "flex items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-sm font-medium",
                        section === s.slug
                          ? "bg-chrome text-foreground"
                          : "text-muted-2 hover:bg-chrome hover:text-foreground",
                      )}
                    >
                      <Icon className="size-4 shrink-0" />
                      <span>{s.label}</span>
                    </button>
                  );
                })}
              </div>
            ))}
          </nav>
        </div>
      </ContextPane>

      <div className="mx-auto max-w-3xl p-6">
        {isError && <ErrorBanner message={errMessage(error)} />}
        {isLoading ? (
          <div className="text-sm text-muted-2">Loading…</div>
        ) : (
          <>
            {section === "profile" && <ProfileSection profile={settings?.profile} />}
            {section === "workspace" && <WorkspaceSection workspace={settings?.workspace} />}
            {section === "ai-providers" && (
              <section>
                <h2 className="text-lg font-bold">AI Providers</h2>
                <p className="mt-1 text-sm text-muted-2">
                  Connect the LLM providers your coder can use — add a key once, then pick a
                  provider under Coder.
                </p>
                <div className="mt-4">
                  <ProviderCards
                    catalog={settings?.coder_catalog ?? []}
                    providers={settings?.api_providers ?? []}
                  />
                </div>
              </section>
            )}
            {section === "coder" && (
              <CoderSection
                coder={settings?.coder}
                detectedCoders={settings?.detected_coders ?? []}
                catalog={settings?.coder_catalog ?? []}
                coderMode={settings?.coder_mode}
              />
            )}
            {section === "master-password" && <MasterPasswordSection />}
            {section === "appearance" && <AppearanceSection />}
            {/* Each owner section mounts OwnerGate independently. That costs
                no extra requests: the gate's probe is a react-query on the
                shared key ["admin","overview"], so all five share one cached
                result — and one unlock covers all five because the SERVER owns
                the verification stamp, not this component. */}
            {section === "owner-workspaces" && (
              <OwnerGate title="Workspaces">
                <WorkspacesSection />
              </OwnerGate>
            )}
            {section === "owner-instance-url" && (
              <OwnerGate title="Instance URL">
                <InstanceURLSection />
              </OwnerGate>
            )}
            {section === "owner-system" && (
              <OwnerGate title="System status">
                <SystemStatusSection />
              </OwnerGate>
            )}
            {section === "owner-backup" && (
              <OwnerGate title="Backup">
                <BackupSection />
              </OwnerGate>
            )}
            {section === "owner-audit" && (
              <OwnerGate title="Audit log">
                <AuditLogSection />
              </OwnerGate>
            )}
          </>
        )}
      </div>
    </>
  );
}
