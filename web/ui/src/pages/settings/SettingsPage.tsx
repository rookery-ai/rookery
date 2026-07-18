import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { AlertTriangle, Check } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
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
import { OwnerSections } from "./OwnerSections";

// Section navigation is driven by a `?section=` query param (not scroll
// anchors) — plain to unit-test (assert the param + the rendered section)
// and avoids IntersectionObserver plumbing for highlighting the active item.
const SECTIONS = [
  { slug: "profile", icon: "👤", label: "Profile" },
  { slug: "workspace", icon: "🏠", label: "Workspace" },
  { slug: "ai-providers", icon: "🧠", label: "AI Providers" },
  { slug: "coder", icon: "⚙️", label: "Coder" },
  { slug: "master-password", icon: "🔐", label: "Master password" },
  { slug: "appearance", icon: "🌓", label: "Appearance" },
  { slug: "owner", icon: "🛡", label: "Owner" },
] as const;

type SectionSlug = (typeof SECTIONS)[number]["slug"];
const DEFAULT_SECTION: SectionSlug = "profile";

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
      await save.mutateAsync(form);
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
        Tells your assistant who you are so it can personalize replies.
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
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" value={form.email} onChange={(e) => set("email", e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="location">Location</Label>
          <Input id="location" value={form.location} onChange={(e) => set("location", e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="timezone">Timezone</Label>
          <Input
            id="timezone"
            placeholder="e.g. Europe/Skopje"
            value={form.timezone}
            onChange={(e) => set("timezone", e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="tone">Tone</Label>
          <Input id="tone" value={form.tone} onChange={(e) => set("tone", e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="language">Language</Label>
          <Input id="language" value={form.language} onChange={(e) => set("language", e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="notes">Notes</Label>
          <textarea
            id="notes"
            value={form.notes}
            onChange={(e) => set("notes", e.target.value)}
            className="min-h-24 w-full resize-y rounded-md border border-border bg-background p-3 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
          />
        </div>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save profile"}
        </Button>
      </form>
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
      await save.mutateAsync(form);
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
      <p className="mt-1 text-sm text-muted-2">The name and description shown across the app.</p>

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
          <Label htmlFor="ws_about">About</Label>
          <textarea
            id="ws_about"
            value={form.about}
            onChange={(e) => {
              setForm((f) => ({ ...f, about: e.target.value }));
              setSaved(false);
            }}
            className="min-h-24 w-full resize-y rounded-md border border-border bg-background p-3 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
          />
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
  const section: SectionSlug = isSectionSlug(rawSection) ? rawSection : DEFAULT_SECTION;

  const { data: settings, isLoading, isError, error } = useSettings();

  function goTo(slug: SectionSlug) {
    const next = new URLSearchParams(searchParams);
    next.set("section", slug);
    setSearchParams(next);
  }

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col gap-3 p-3">
          <h2 className="px-1 text-sm font-bold">Settings</h2>
          <nav className="flex flex-col gap-1">
            {SECTIONS.map((s) => (
              <button
                key={s.slug}
                type="button"
                onClick={() => goTo(s.slug)}
                aria-current={section === s.slug ? "page" : undefined}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm font-medium",
                  section === s.slug ? "bg-chrome text-foreground" : "text-muted-2 hover:bg-chrome",
                )}
              >
                <span>{s.icon}</span>
                <span>{s.label}</span>
              </button>
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
              />
            )}
            {section === "master-password" && <MasterPasswordSection />}
            {section === "appearance" && <AppearanceSection />}
            {section === "owner" && <OwnerSections />}
          </>
        )}
      </div>
    </>
  );
}
