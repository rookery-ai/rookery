import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import SkillsPage from "./SkillsPage";
import SkillNewPage from "./SkillNewPage";
import SkillDetailPage, { CoreSkillViewPage } from "./SkillDetailPage";
import { SkillView } from "./SkillView";
import type { SkillListItem, CoreSkillListItem, SkillDraft, SkillDetail } from "@/lib/skills";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let skills: SkillListItem[];
let coreSkills: CoreSkillListItem[];
let draft: SkillDraft;
let skillDetail: SkillDetail;
let dismissCalled: boolean;

function resetFixtures() {
  skills = [{ id: "s1", name: "Invoice Formatter", description: "Formats invoices.", created_at: "2026-07-01T00:00:00Z", category: "Productivity", version: "1.0.0", requires: [] }];
  coreSkills = [{ slug: "pdf", name: "pdf", description: "Read and write PDFs.", category: "File Processing", version: "1.0.0", requires: ["pdftotext or pandoc"] }];
  draft = null;
  skillDetail = { id: "s1", name: "Invoice Formatter", description: "Formats invoices.", content: "# Invoice Formatter\n\nDo the thing.", category: "Productivity", version: "1.0.0", requires: [] };
  dismissCalled = false;
}

function mockFetch(handlers: Record<string, (body: unknown) => Response | Promise<Response>> = {}) {
  const calls: Array<{ url: string; method: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/skills" && method === "GET") {
        return Promise.resolve(jsonResponse({ skills, core_skills: coreSkills, draft }));
      }
      if (url === "/api/v1/skills" && method === "POST") {
        const created = { id: "s2", name: "New Skill", description: "", content: (body as { content: string }).content };
        skills = [...skills, { id: created.id, name: created.name, description: created.description, created_at: "2026-07-17T00:00:00Z", category: "Other", version: "1.0.0", requires: [] }];
        return Promise.resolve(jsonResponse(created, 201));
      }
      if (url === "/api/v1/skills/s1" && method === "GET") return Promise.resolve(jsonResponse(skillDetail));
      if (url === "/api/v1/skills/s1" && method === "PUT") {
        skillDetail = { ...skillDetail, content: (body as { content: string }).content };
        return Promise.resolve(jsonResponse(skillDetail));
      }
      if (url === "/api/v1/skills/s1" && method === "DELETE") {
        skills = skills.filter((s) => s.id !== "s1");
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url === "/api/v1/skills/core/pdf" && method === "GET") {
        return Promise.resolve(jsonResponse({ slug: "pdf", content: "# pdf\n\nHandle PDFs." }));
      }
      if (url === "/api/v1/skills/design/dismiss" && method === "POST") {
        dismissCalled = true;
        draft = null;
        return Promise.resolve(jsonResponse({ status: "ok" }));
      }

      const key = `${method} ${url}`;
      if (handlers[key]) return Promise.resolve(handlers[key](body));

      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

function wrap(initialEntry = "/skills") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/skills" element={<SkillsPage />} />
            <Route path="/skills/new" element={<SkillNewPage />} />
            <Route path="/skills/core/:slug" element={<CoreSkillViewPage />} />
            <Route path="/skills/:id" element={<SkillDetailPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test("SkillsPage renders Your skills and Core skills sections", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Invoice Formatter")).toBeInTheDocument();
  expect(screen.getByText("Your skills")).toBeInTheDocument();
  expect(screen.getByText("Core skills")).toBeInTheDocument();
  expect(screen.getByText("pdf")).toBeInTheDocument();

  const userCard = screen.getByText("Invoice Formatter").closest("a")!;
  expect(userCard.getAttribute("href")).toBe("/skills/s1");
  const coreCard = screen.getByText("pdf").closest("a")!;
  expect(coreCard.getAttribute("href")).toBe("/skills/core/pdf");
});

test("New skill button links to /skills/new", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Invoice Formatter");
  const link = screen.getByRole("link", { name: /new skill/i });
  expect(link.getAttribute("href")).toBe("/skills/new");
});

test("empty state shows create-first-skill CTA when there are no skills or draft", async () => {
  skills = [];
  coreSkills = [];
  mockFetch();
  wrap();

  expect(await screen.findByText(/no skills of your own yet/i)).toBeInTheDocument();
  const cta = screen.getByRole("link", { name: /create.*first skill/i });
  expect(cta.getAttribute("href")).toBe("/skills/new");
});

// Regression: the empty state used to gate the ENTIRE page body, so a fresh
// workspace with no user skills saw only the CTA and the always-available
// built-in skills were invisible until a draft happened to exist.
test("core skills render even when the user has no skills of their own", async () => {
  skills = [];
  draft = null;
  mockFetch();
  wrap();

  expect(await screen.findByText(/no skills of your own yet/i)).toBeInTheDocument();
  expect(screen.getByText("Core skills")).toBeInTheDocument();
  const coreCard = screen.getByRole("link", { name: /pdf/i });
  expect(coreCard.getAttribute("href")).toBe("/skills/core/pdf");
});

test("the search box filters both user skills and core skills", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Invoice Formatter");

  const box = screen.getByLabelText(/search skills/i);
  await userEvent.type(box, "invoice");

  expect(screen.getByText("Invoice Formatter")).toBeInTheDocument();
  expect(screen.queryByText("Core skills")).not.toBeInTheDocument();

  await userEvent.clear(box);
  await userEvent.type(box, "zzznomatchzzz");
  expect(screen.getByText(/no skills match/i)).toBeInTheDocument();
});

test("draft banner shows Resume link and Discard posts dismiss + refreshes the list", async () => {
  draft = { skill_name: "Draft Skill", state: "designing", updated_at: "2026-07-16T00:00:00Z" };
  mockFetch();
  wrap();

  expect(await screen.findByText(/Draft Skill/)).toBeInTheDocument();
  const resume = screen.getByRole("link", { name: /resume/i });
  expect(resume.getAttribute("href")).toBe("/skills/new?resume=1");

  await userEvent.click(screen.getByRole("button", { name: /discard/i }));

  await waitFor(() => expect(dismissCalled).toBe(true));
  await waitFor(() => expect(screen.queryByText(/Draft Skill/)).not.toBeInTheDocument());
});

test("Import dialog POSTs the pasted SKILL.md content", async () => {
  const calls = mockFetch();
  wrap();

  await screen.findByText("Invoice Formatter");
  await userEvent.click(screen.getByRole("button", { name: /import/i }));

  const textarea = await screen.findByRole("textbox", { name: /skill\.md content/i });
  await userEvent.type(textarea, "---\nname: x\n---\nbody");

  await userEvent.click(screen.getByRole("button", { name: /^import$/i }));

  await waitFor(() => {
    const post = calls.find((c) => c.url === "/api/v1/skills" && c.method === "POST");
    expect(post).toBeTruthy();
    expect((post!.body as { content: string }).content).toBe("---\nname: x\n---\nbody");
  });
});

test("SkillNewPage with ?resume=1 mounts DesignerSurface without any GET to a design state URL", async () => {
  const calls = mockFetch();
  wrap("/skills/new?resume=1");

  // DesignerSurface renders the composer once mounted (no name gate, no
  // recovery banner since there's no draft in the fixture).
  expect(await screen.findByRole("textbox")).toBeInTheDocument();

  const stateCalls = calls.filter((c) => c.url.includes("/skills/design/state"));
  expect(stateCalls).toHaveLength(0);
});

test("SkillNewPage with ?resume=1 and an existing draft POSTs the skill resume endpoint and replays history", async () => {
  draft = { skill_name: "Draft Skill", state: "designing", updated_at: "2026-07-16T00:00:00Z" };
  const calls = mockFetch({
    "POST /api/v1/skills/design/resume": () =>
      jsonResponse({
        response: "Resuming your draft for **Draft Skill**. Continue, or approve.",
        state: "designing",
        history: [
          { role: "user", content: "make it check invoices" },
          { role: "assistant", content: "got it, anything else?" },
        ],
        skill_name: "Draft Skill",
      }),
  });
  wrap("/skills/new?resume=1");

  expect(await screen.findByText("make it check invoices")).toBeInTheDocument();
  expect(screen.getByText("got it, anything else?")).toBeInTheDocument();
  expect(screen.getByText(/Resuming your draft for/)).toBeInTheDocument();

  const resumeCalls = calls.filter((c) => c.url === "/api/v1/skills/design/resume");
  expect(resumeCalls).toHaveLength(1);
});

test("SkillDetailPage Save PUTs the edited content", async () => {
  const calls = mockFetch();
  wrap("/skills/s1");

  // The viewer opens on the rendered view now (same as a built-in skill), so
  // reach the editor through the Raw tab.
  await userEvent.click(await screen.findByRole("button", { name: /^raw$/i }));
  const textarea = await screen.findByRole("textbox", { name: "SKILL.md" });
  await userEvent.type(textarea, "\nmore");

  await userEvent.click(screen.getByRole("button", { name: /save skill/i }));

  await waitFor(() => {
    const put = calls.find((c) => c.url === "/api/v1/skills/s1" && c.method === "PUT");
    expect(put).toBeTruthy();
    expect((put!.body as { content: string }).content).toContain("more");
  });
});

test("SkillDetailPage Delete confirms then DELETEs and navigates to /skills", async () => {
  mockFetch();
  wrap("/skills/s1");

  await screen.findByRole("button", { name: /^raw$/i });
  await userEvent.click(screen.getByRole("button", { name: /^delete$/i }));

  const heading = await screen.findByRole("heading", { name: /^Delete\s/ });
  expect(heading.textContent).toContain("Invoice Formatter");

  await userEvent.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() => expect(screen.queryByRole("button", { name: /^raw$/i })).not.toBeInTheDocument());
});

test("CoreSkillViewPage renders a readonly markdown render of the core skill content", async () => {
  mockFetch();
  wrap("/skills/core/pdf");

  expect(await screen.findByText("Handle PDFs.")).toBeInTheDocument();
  // Read-only: no textarea/editor present.
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

// ── SkillView: one viewer for both kinds ─────────────────────────────────

const VIEW_CONTENT = "---\nname: demo\ndescription: d\n---\n\n# Demo\n\nSome **bold** body.\n";

function renderView(kind: "core" | "user", extra: Record<string, unknown> = {}) {
  return render(
    <SkillView
      kind={kind}
      name="demo"
      description="Does a demo thing."
      category="File Processing"
      version="1.0.0"
      requires={["pandoc", "pdftotext or mutool"]}
      content={VIEW_CONTENT}
      {...extra}
    />,
  );
}

test("SkillView renders the same metadata header for both kinds", () => {
  for (const kind of ["core", "user"] as const) {
    const { unmount } = renderView(kind);
    expect(screen.getByText("demo")).toBeInTheDocument();
    expect(screen.getByText("File Processing")).toBeInTheDocument();
    expect(screen.getByText("v1.0.0")).toBeInTheDocument();
    expect(screen.getByText(/pandoc, pdftotext or mutool/)).toBeInTheDocument();
    expect(screen.getByText(kind === "core" ? "Built-in" : "Yours")).toBeInTheDocument();
    unmount();
  }
});

test("SkillView defaults to the rendered view for both kinds", () => {
  renderView("user", { onSave: vi.fn() });
  // Rendered markdown produces a heading element; the raw source does not.
  expect(screen.getByRole("heading", { name: "Demo" })).toBeInTheDocument();
  expect(screen.queryByLabelText("SKILL.md")).not.toBeInTheDocument();
});

test("SkillView: core skills get a read-only raw view and no write controls", async () => {
  renderView("core");
  await userEvent.click(screen.getByRole("button", { name: /^raw$/i }));
  const ta = screen.getByLabelText("SKILL.md") as HTMLTextAreaElement;
  expect(ta.readOnly).toBe(true);
  expect(screen.queryByRole("button", { name: /save skill/i })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /delete/i })).not.toBeInTheDocument();
});

test("SkillView: user skills have an editable raw view, Save disabled until dirty", async () => {
  renderView("user", { onSave: vi.fn(), onDelete: vi.fn() });
  await userEvent.click(screen.getByRole("button", { name: /^raw$/i }));
  const ta = screen.getByLabelText("SKILL.md") as HTMLTextAreaElement;
  expect(ta.readOnly).toBe(false);
  expect(screen.getByRole("button", { name: /save skill/i })).toBeDisabled();
  await userEvent.type(ta, "x");
  expect(screen.getByRole("button", { name: /save skill/i })).toBeEnabled();
});

// The toggle must not be a way to silently lose an edit.
test("SkillView keeps an unsaved edit across a Raw → Rendered → Raw round trip", async () => {
  renderView("user", { onSave: vi.fn() });
  await userEvent.click(screen.getByRole("button", { name: /^raw$/i }));
  await userEvent.type(screen.getByLabelText("SKILL.md"), "EDITED");
  await userEvent.click(screen.getByRole("button", { name: /^rendered$/i }));
  await userEvent.click(screen.getByRole("button", { name: /^raw$/i }));
  expect((screen.getByLabelText("SKILL.md") as HTMLTextAreaElement).value).toContain("EDITED");
});

test("SkillView omits the version chip when the version is unset", () => {
  renderView("user", { category: "Other", version: "" });
  expect(screen.getByText("Other")).toBeInTheDocument();
  expect(screen.queryByText(/^v$/)).not.toBeInTheDocument();
});

// Regression: a Go nil slice marshals to null, and a TS default parameter
// substitutes only for undefined — so `requires = []` never fired and
// `requires.length` threw, blanking the whole route with "Unexpected
// Application Error". Every core skill with no declared tooling hit this.
test("SkillView tolerates a null requires from the API", () => {
  expect(() =>
    render(
      <SkillView
        kind="core"
        name="agent-collaboration"
        category="Agent Behaviour"
        content="# Agent collaboration"
        requires={null}
      />,
    ),
  ).not.toThrow();
  expect(screen.queryByText(/^Needs:/)).toBeNull();
});
