# UI Redesign Sub-plan 2: Frontend Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the React SPA foundation — Vite/`go:embed` pipeline, design tokens with dark mode, the Slack-style app shell, login and workspace flows — served by the Go binary at `/app`, against the `/api/v1` layer from sub-plan 1.

**Architecture:** New `web/ui/` Vite+React+TS app, built to `web/ui/dist/` and embedded via `go:embed` (`web/ui/embed.go` + `web/spa.go` SPA-fallback handler at `/app`). During the branch the template UI keeps all its routes; the SPA lives at `/app` and moves to `/` in sub-plan 6. Cookie-session auth reused as-is; dev loop is `vite dev` proxying `/api` to `:8080`. Spec: `docs/superpowers/specs/2026-07-16-ui-redesign-design.md` §2, §4, §5.

**Tech Stack:** Vite 7 + React 19 + TypeScript, Tailwind CSS v4 (`@tailwindcss/vite`), shadcn/ui (Radix), lucide-react, TanStack Query v5, React Router v7, Vitest + @testing-library/react (jsdom). Node is build-time only (v24 already on the box).

## Global Constraints

- Branch `ui-redesign` (already checked out). Full Go suite `go test ./... -count=1 -timeout 120s` stays green at every commit; frontend `npm test` (Vitest, run in `web/ui/`) green at every commit from Task 1 on.
- **`dist/` is not committed** (spec §2): `.gitignore` excludes `web/ui/dist/*` except a committed `web/ui/dist/.gitkeep` so `go:embed all:dist` always compiles. `ui.DistFS()` reports `ok=false` when `index.html` is absent; `/app` then returns 503 `"UI not built"` — `go build` never requires node.
- **SPA base path is `/app`**: Vite `base: '/app/'`, React Router `basename="/app"`. All fetches go to same-origin `/api/v1/*` (absolute path, NOT under /app).
- **Design tokens** (spec §5, exact values): light — content `#ffffff`, chrome `#f7f6f3`, text `#37352f`, border `#e9e7e3`, muted text `#787671`, faint text `#9b9891`; status green `#448361`/`#dbeddb`, amber `#c77d48`/`#fdecc8`, red `#d44c47`/`#ffe2dd`; one accent `#2d5a74`. Dark mode mirrors via the `.dark` class (shadcn convention), manual toggle + system default, persisted in `localStorage` key `sa-theme`.
- **API error envelope**: `{"error":{"code","message"}}` on new endpoints, legacy `{"error":"string"}` on the documented exceptions — the API client must parse BOTH into one `ApiError{code,message,status}` (legacy string → `code:"legacy"`).
- Auth redirect contract (from sub-plan 1 middleware): 401 `not_authenticated` → `/login`; 403 `no_workspace` → workspace picker; 403 `must_change_password` → `/change-password`; 403 `needs_setup` → setup placeholder (full onboarding is sub-plan 5).
- npm installs pin nothing exotic: latest stable majors (React 19, Vite 7, Tailwind 4, TanStack Query 5, React Router 7). `package-lock.json` IS committed.
- Commit messages: `feat(ui): ...` / `feat(web): ...` as given per task.

---

### Task 1: Vite scaffold, Tailwind, shadcn, Vitest

**Files:**
- Create: `web/ui/` (Vite react-ts scaffold: `package.json`, `vite.config.ts`, `tsconfig*.json`, `index.html`, `src/…`)
- Create: `web/ui/vitest.setup.ts`, `web/ui/src/App.test.tsx`
- Modify: `.gitignore`

**Interfaces:**
- Produces: a `web/ui` npm project where `npm run build` emits `dist/` with `base=/app/`, `npm test` runs Vitest, `npm run dev` proxies `/api` to `http://127.0.0.1:8080`. Path alias `@/` → `src/`. shadcn components live in `src/components/ui/`.

- [ ] **Step 1: Scaffold**

```bash
cd /home/rookie/simple-agents-v2/web && npm create vite@latest ui -- --template react-ts
cd ui && npm install
npm install -D vitest @testing-library/react @testing-library/jest-dom @testing-library/user-event jsdom @types/node
npm install tailwindcss @tailwindcss/vite @tanstack/react-query react-router lucide-react
```

(React Router v7 package is `react-router` — the `react-router-dom` name is a v6-era alias; if the shadcn CLI or docs reference `react-router-dom`, `react-router` is the correct v7 import root.)

- [ ] **Step 2: Replace `web/ui/vite.config.ts`**

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  base: "/app/",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
    proxy: {
      // Dev loop: Go server on :8080 answers the API (cookies work same-origin
      // because the proxy forwards them). SSE needs buffering off — proxy
      // handles that natively.
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: false },
    },
  },
  // Vitest config lives here so one file drives build+test.
  test: {
    environment: "jsdom",
    setupFiles: "./vitest.setup.ts",
    globals: true,
  },
});
```

Add to the top: `/// <reference types="vitest/config" />` so the `test` key typechecks.

- [ ] **Step 3: `web/ui/vitest.setup.ts`**

```ts
import "@testing-library/jest-dom/vitest";
```

Add `"test": "vitest run", "test:watch": "vitest"` to `package.json` scripts.

- [ ] **Step 4: Tailwind v4 + shadcn init**

Replace `src/index.css` content with just `@import "tailwindcss";` for now (Task 3 replaces this with the token sheet). Then:

```bash
cd /home/rookie/simple-agents-v2/web/ui
npx shadcn@latest init   # style: new-york, base color: neutral, CSS variables: yes
npx shadcn@latest add button input label dialog dropdown-menu sheet avatar tooltip separator
```

If the CLI asks about React Server Components, answer no; components dir `src/components/ui`, alias `@/`. Accept whatever `components.json` it writes.

- [ ] **Step 5: Minimal App + failing test first**

`web/ui/src/App.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders the app root", () => {
  render(<App />);
  expect(screen.getByText(/simple agents/i)).toBeInTheDocument();
});
```

Run: `npm test` → FAIL (default Vite App has no such text). Then replace `web/ui/src/App.tsx`:

```tsx
export default function App() {
  return <div className="p-8 text-xl font-bold">Simple Agents</div>;
}
```

Delete `src/App.css` and its import; keep `src/main.tsx` as scaffolded (imports `./index.css`). Run: `npm test` → PASS. Run `npm run build` → emits `dist/` with assets referencing `/app/assets/...` (verify with `grep -o '/app/assets[^"]*' dist/index.html`).

- [ ] **Step 6: gitignore + commit**

Append to repo-root `.gitignore`:

```
web/ui/node_modules/
web/ui/dist/*
!web/ui/dist/.gitkeep
```

```bash
mkdir -p web/ui/dist && touch web/ui/dist/.gitkeep
cd /home/rookie/simple-agents-v2 && git add -A && git commit -m "feat(ui): Vite+React+TS scaffold — Tailwind v4, shadcn/ui, Vitest, base /app/"
```

---

### Task 2: go:embed + SPA serving at /app

**Files:**
- Create: `web/ui/embed.go`, `web/spa.go`, `web/spa_test.go`
- Modify: `web/server.go` (one call in `setupRoutes`), `Makefile`

**Interfaces:**
- Consumes: `web/ui/dist/` (built or placeholder-only).
- Produces: `ui.DistFS() (fs.FS, bool)`; `Server.setupSPARoutes()`; routes `GET /app`, `GET /app/*`; Makefile targets `ui` (npm build) and `build` now depending on it, plus `build-go` (Go only — used by tests/CI without node).

- [ ] **Step 1: Failing Go test** — `web/spa_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v4"
)

func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":       {Data: []byte("<html>SPA-INDEX</html>")},
		"assets/app-x.js":  {Data: []byte("console.log(1)")},
	}
}

func TestSPAHandlerServesAssetsAndFallsBack(t *testing.T) {
	e := echo.New()
	s := &Server{echo: e}
	h := s.spaHandler(spaTestFS(), true)

	cases := []struct {
		path     string
		wantBody string
	}{
		{"/app", "SPA-INDEX"},                 // root → index
		{"/app/assets/app-x.js", "console"},   // real asset → served
		{"/app/agents/123", "SPA-INDEX"},      // client route → index fallback
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := h(c); err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Fatalf("%s: got %d %q", tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestSPAHandlerNotBuilt(t *testing.T) {
	e := echo.New()
	s := &Server{echo: e}
	h := s.spaHandler(nil, false)
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	if err := h(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d want 503", rec.Code)
	}
}

func TestSPARoutesRegistered(t *testing.T) {
	s, _ := newAPITestServer(t)
	have := map[string]bool{}
	for _, r := range s.echo.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, w := range []string{"GET /app", "GET /app/*"} {
		if !have[w] {
			t.Fatalf("missing route %s", w)
		}
	}
}
```

Run: `go test ./web/... -run TestSPA -count=1` → FAIL (spaHandler undefined).

- [ ] **Step 2: `web/ui/embed.go`**

```go
// Package ui embeds the built single-page app (web/ui/dist). The dist tree is
// produced by `make ui` (npm run build) and is NOT committed — only a .gitkeep
// placeholder keeps the embed pattern valid, so `go build` works without node.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// DistFS returns the built SPA rooted at dist/. ok is false when the UI has
// not been built into this binary (no index.html present).
func DistFS() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
```

- [ ] **Step 3: `web/spa.go`**

```go
package web

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/ilijad1/simple-agents/web/ui"
	"github.com/labstack/echo/v4"
)

// setupSPARoutes mounts the embedded SPA at /app. Until sub-plan 6 removes the
// template UI, /app is the only SPA mount point; the cutover to / happens there.
func (s *Server) setupSPARoutes() {
	distFS, ok := ui.DistFS()
	h := s.spaHandler(distFS, ok)
	s.echo.GET("/app", h)
	s.echo.GET("/app/*", h)
}

// spaHandler serves real files from the embedded dist and falls back to
// index.html for client-side routes. With no built UI it answers 503 so a
// node-less build still runs the API + template UI cleanly.
func (s *Server) spaHandler(distFS fs.FS, ok bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !ok {
			return c.String(http.StatusServiceUnavailable, "UI not built — run `make ui`, then rebuild the binary")
		}
		p := strings.TrimPrefix(c.Request().URL.Path, "/app")
		p = strings.TrimPrefix(p, "/")
		if p != "" {
			if st, err := fs.Stat(distFS, p); err == nil && !st.IsDir() {
				return c.FileFS(p, distFS)
			}
		}
		return c.FileFS("index.html", distFS)
	}
}
```

In `web/server.go`, at the end of `setupRoutes()` (right after `s.setupAPIRoutes()`), add:

```go
	s.setupSPARoutes()
```

- [ ] **Step 4: Run tests** — `go test ./web/... -run TestSPA -count=1` → PASS. Full `go test ./... -count=1 -timeout 120s` → PASS.

- [ ] **Step 5: Makefile** — replace the `build` block and add `ui`/`build-go`:

```make
## ui: build the SPA (web/ui/dist) — requires node; run before `build`
ui:
	cd web/ui && npm ci && npm run build

## build-go: compile the binary only (embeds whatever dist/ currently holds)
build-go:
	go build -o $(BIN) $(PKG)

## build: full artifact — SPA + binary (spec §2)
build: ui build-go
```

Update `.PHONY` to include `ui build-go`. `start` already depends on `build` — after this change `make deploy` rebuilds the UI too, which is the spec's contract.

- [ ] **Step 6: End-to-end check + commit**

```bash
cd /home/rookie/simple-agents-v2 && make build && ./bin/simple-agents serve & sleep 2
curl -s http://127.0.0.1:8080/app | grep -q "Simple Agents" && echo SPA-OK
kill %1
git add -A && git commit -m "feat(web): embed SPA at /app — go:embed dist, SPA fallback, 503 when unbuilt, make ui"
```

(If port 8080 is in use by the production server, use `SA_PORT=8090` for the check and curl :8090.)

---

### Task 3: Design tokens + theme provider (dark mode)

**Files:**
- Create: `web/ui/src/theme.tsx`, `web/ui/src/theme.test.tsx`
- Modify: `web/ui/src/index.css`, `web/ui/src/main.tsx`

**Interfaces:**
- Produces: `<ThemeProvider>` + `useTheme(): { theme: "light"|"dark"|"system"; setTheme(t): void }`; CSS custom properties consumed via Tailwind utilities everywhere (`bg-background`, `bg-chrome`, `text-foreground`, `text-muted-2`, `border-border`, `text-accent`, status colors `ok/warn/danger`). localStorage key `sa-theme`.

- [ ] **Step 1: Token sheet** — replace `web/ui/src/index.css` (keep any `@import` lines shadcn init added, replace the `:root`/`.dark` variable blocks):

```css
@import "tailwindcss";

/* Design tokens — spec §5. Notion-ish: white content, warm gray chrome,
   near-black text, hairline borders, one restrained accent. */
:root {
  --background: #ffffff;
  --chrome: #f7f6f3;
  --foreground: #37352f;
  --border: #e9e7e3;
  --muted: #787671;
  --muted-2: #9b9891;
  --accent: #2d5a74;
  --accent-soft: #e7f0f5;
  --ok: #448361;
  --ok-soft: #dbeddb;
  --warn: #c77d48;
  --warn-soft: #fdecc8;
  --danger: #d44c47;
  --danger-soft: #ffe2dd;
}

.dark {
  --background: #191919;
  --chrome: #202020;
  --foreground: #e6e4e0;
  --border: #333330;
  --muted: #a3a09a;
  --muted-2: #7c7972;
  --accent: #6fa2bd;
  --accent-soft: #24313a;
  --ok: #6fbf8f;
  --ok-soft: #1e3328;
  --warn: #d99a66;
  --warn-soft: #3a2d1e;
  --danger: #e0716c;
  --danger-soft: #3a2322;
}

@theme inline {
  --color-background: var(--background);
  --color-chrome: var(--chrome);
  --color-foreground: var(--foreground);
  --color-border: var(--border);
  --color-muted: var(--muted);
  --color-muted-2: var(--muted-2);
  --color-accent: var(--accent);
  --color-accent-soft: var(--accent-soft);
  --color-ok: var(--ok);
  --color-ok-soft: var(--ok-soft);
  --color-warn: var(--warn);
  --color-warn-soft: var(--warn-soft);
  --color-danger: var(--danger);
  --color-danger-soft: var(--danger-soft);
}

@variant dark (.dark &);

body {
  @apply bg-background text-foreground antialiased;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  font-size: 14px;
}
```

Keep shadcn's own variable block if `init` generated one (its components reference `--radius` etc.); merge, don't delete. If shadcn's `:root` already defines `--background`/`--foreground` in oklch, replace those two entries with the hex values above so there is exactly one source of truth.

- [ ] **Step 2: Failing theme test** — `web/ui/src/theme.test.tsx`:

```tsx
import { render, act } from "@testing-library/react";
import { ThemeProvider, useTheme } from "./theme";

function Probe() {
  const { theme, setTheme } = useTheme();
  return (
    <button data-testid="btn" onClick={() => setTheme("dark")}>
      {theme}
    </button>
  );
}

test("theme defaults to system and persists explicit choice", () => {
  localStorage.removeItem("sa-theme");
  const { getByTestId } = render(
    <ThemeProvider>
      <Probe />
    </ThemeProvider>,
  );
  expect(getByTestId("btn").textContent).toBe("system");
  act(() => getByTestId("btn").click());
  expect(localStorage.getItem("sa-theme")).toBe("dark");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
});
```

Run `npm test` → FAIL. 

- [ ] **Step 3: `web/ui/src/theme.tsx`**

```tsx
import { createContext, useContext, useEffect, useState } from "react";

type Theme = "light" | "dark" | "system";
const KEY = "sa-theme";

const Ctx = createContext<{ theme: Theme; setTheme: (t: Theme) => void }>({
  theme: "system",
  setTheme: () => {},
});

function apply(theme: Theme) {
  const dark =
    theme === "dark" ||
    (theme === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(
    () => (localStorage.getItem(KEY) as Theme) ?? "system",
  );

  useEffect(() => {
    apply(theme);
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => theme === "system" && apply("system");
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [theme]);

  const setTheme = (t: Theme) => {
    localStorage.setItem(KEY, t);
    setThemeState(t);
  };

  return <Ctx.Provider value={{ theme, setTheme }}>{children}</Ctx.Provider>;
}

export const useTheme = () => useContext(Ctx);
```

Wrap the app in `main.tsx`: `<ThemeProvider><App /></ThemeProvider>`.

- [ ] **Step 4: Run** — `npm test` → PASS; `npm run build` → clean. Commit: `git add -A && git commit -m "feat(ui): design tokens (light/dark) + ThemeProvider with system default"`

---

### Task 4: API client, session, router skeleton with guards

**Files:**
- Create: `web/ui/src/lib/api.ts`, `web/ui/src/lib/api.test.ts`, `web/ui/src/lib/session.ts`, `web/ui/src/router.tsx`, `web/ui/src/pages/Placeholder.tsx`
- Modify: `web/ui/src/main.tsx`, `web/ui/src/App.tsx` (App becomes the router mount; keep the `/simple agents/i` test passing by rendering the title in the login/shell)

**Interfaces:**
- Produces (used by every later task):
  - `api.get<T>(path)`, `api.post<T>(path, body?)`, `api.put<T>(path, body?)`, `api.del<T>(path, body?)` — all take `/api/v1/...` absolute paths, return parsed JSON `T`, throw `ApiError`.
  - `class ApiError extends Error { status: number; code: string }` (legacy `{error:"string"}` → `code === "legacy"`).
  - `type Session = { authenticated: boolean; owner?: { id: string; username: string; must_change_password: boolean }; workspace?: Workspace | null; workspaces?: Workspace[] }`, `type Workspace = { id: string; name: string; about: string; needs_setup: boolean; created_at: string }`.
  - `useSession()` — TanStack Query wrapper on `GET /api/v1/auth/session`, `queryKey: ["session"]`; callers invalidate `["session"]` after login/logout/enter/leave.
  - Router paths (basename `/app`): `/login`, `/change-password`, `/workspaces`, and a guarded layout with children `/`, `/kb`, `/agents`, `/skills`, `/connections`, `/chats`, `/secrets`, `/settings` (placeholders until later sub-plans).
  - `<RequireAuth>` guard: no session → `/login`; `must_change_password` → `/change-password`; no active workspace → `/workspaces`; `workspace.needs_setup` → renders the `/workspaces` picker with a "finish setup in the classic UI" note (full onboarding is sub-plan 5).

- [ ] **Step 1: Failing api-client test** — `web/ui/src/lib/api.test.ts`:

```ts
import { api, ApiError } from "./api";

function mockFetchOnce(status: number, body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

test("parses success JSON", async () => {
  mockFetchOnce(200, { ok: true });
  await expect(api.get("/api/v1/x")).resolves.toEqual({ ok: true });
});

test("parses envelope errors", async () => {
  mockFetchOnce(401, { error: { code: "not_authenticated", message: "log in first" } });
  const err = await api.get("/api/v1/x").catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err.code).toBe("not_authenticated");
  expect(err.status).toBe(401);
  expect(err.message).toBe("log in first");
});

test("parses legacy string errors", async () => {
  mockFetchOnce(400, { error: "name is required" });
  const err = await api.post("/api/v1/x", {}).catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err.code).toBe("legacy");
  expect(err.message).toBe("name is required");
});
```

Run `npm test` → FAIL.

- [ ] **Step 2: `web/ui/src/lib/api.ts`**

```ts
export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* non-JSON (e.g. 503 UI-not-built) — fall through */
  }
  if (!res.ok) {
    const e = (data as { error?: { code?: string; message?: string } | string } | null)?.error;
    if (typeof e === "string") throw new ApiError(res.status, "legacy", e);
    if (e && typeof e === "object")
      throw new ApiError(res.status, e.code ?? "unknown", e.message ?? res.statusText);
    throw new ApiError(res.status, "unknown", text || res.statusText);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string, body?: unknown) => request<T>("DELETE", path, body),
};
```

Run `npm test` → PASS.

- [ ] **Step 3: `web/ui/src/lib/session.ts`**

```ts
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type Workspace = {
  id: string;
  name: string;
  about: string;
  needs_setup: boolean;
  created_at: string;
};

export type Session = {
  authenticated: boolean;
  owner?: { id: string; username: string; must_change_password: boolean };
  workspace?: Workspace | null;
  workspaces?: Workspace[];
};

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api.get<Session>("/api/v1/auth/session"),
    staleTime: 30_000,
  });
}
```

- [ ] **Step 4: Router + guards** — `web/ui/src/pages/Placeholder.tsx`:

```tsx
export default function Placeholder({ title }: { title: string }) {
  return (
    <div className="p-8">
      <h1 className="text-xl font-bold">{title}</h1>
      <p className="text-muted-2 mt-2">Coming in a later sub-plan.</p>
    </div>
  );
}
```

`web/ui/src/router.tsx`:

```tsx
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { useSession } from "@/lib/session";
import Placeholder from "@/pages/Placeholder";

function RequireAuth() {
  const { data: session, isLoading } = useSession();
  if (isLoading) return <div className="p-8 text-muted-2">Loading…</div>;
  if (!session?.authenticated) return <Navigate to="/login" replace />;
  if (session.owner?.must_change_password) return <Navigate to="/change-password" replace />;
  if (!session.workspace) return <Navigate to="/workspaces" replace />;
  return <Outlet />;
}

// Tasks 5-7 replace these placeholder elements with real screens.
export const router = createBrowserRouter(
  [
    { path: "/login", element: <Placeholder title="Login" /> },
    { path: "/change-password", element: <Placeholder title="Change password" /> },
    { path: "/workspaces", element: <Placeholder title="Workspaces" /> },
    {
      element: <RequireAuth />,
      children: [
        {
          // AppShell mounts here in Task 6.
          element: <Outlet />,
          children: [
            { path: "/", element: <Placeholder title="Home" /> },
            { path: "/kb", element: <Placeholder title="Knowledge Base" /> },
            { path: "/agents", element: <Placeholder title="Agents" /> },
            { path: "/skills", element: <Placeholder title="Skills" /> },
            { path: "/connections", element: <Placeholder title="Connections" /> },
            { path: "/chats", element: <Placeholder title="Chats" /> },
            { path: "/secrets", element: <Placeholder title="Secrets" /> },
            { path: "/settings", element: <Placeholder title="Settings" /> },
          ],
        },
      ],
    },
  ],
  { basename: "/app" },
);
```

`web/ui/src/App.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router";
import { router } from "./router";

const qc = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
});

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  );
}
```

Update `App.test.tsx` — the router needs a URL under /app; simplest robust form:

```tsx
import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders the app root", async () => {
  window.history.pushState({}, "", "/app/login");
  render(<App />);
  expect(await screen.findByText(/login/i)).toBeInTheDocument();
});
```

- [ ] **Step 5: Run + commit** — `npm test` → PASS; `npm run build` → clean. `git add -A && git commit -m "feat(ui): api client (dual error shapes), session query, router skeleton with auth guards"`

---

### Task 5: Login + change-password screens

**Files:**
- Create: `web/ui/src/pages/Login.tsx`, `web/ui/src/pages/ChangePassword.tsx`, `web/ui/src/pages/Login.test.tsx`
- Modify: `web/ui/src/router.tsx` (swap the two placeholders)

**Interfaces:**
- Consumes: `api`, `useSession` (invalidates `["session"]` on success), shadcn `Button`/`Input`/`Label`.
- Produces: `/login` posts `{username,password}` to `/api/v1/auth/login`; on `must_change_password` → navigate `/change-password`, else `/`. `/change-password` posts `{password,confirm}` to `/api/v1/auth/change-password` then navigates `/`. Both show `ApiError.message` inline on failure.

- [ ] **Step 1: Failing test** — `web/ui/src/pages/Login.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import Login from "./Login";

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
}

test("shows API error message on bad credentials", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ error: { code: "invalid_credentials", message: "invalid username or password" } }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  wrap(<Login />);
  await userEvent.type(screen.getByLabelText(/username/i), "admin");
  await userEvent.type(screen.getByLabelText(/password/i), "wrong");
  await userEvent.click(screen.getByRole("button", { name: /log in/i }));
  await waitFor(() =>
    expect(screen.getByText(/invalid username or password/i)).toBeInTheDocument(),
  );
});
```

Run `npm test` → FAIL.

- [ ] **Step 2: `web/ui/src/pages/Login.tsx`**

```tsx
import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export default function Login() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const nav = useNavigate();
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await api.post<{ ok: boolean; must_change_password: boolean }>(
        "/api/v1/auth/login",
        { username, password },
      );
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav(res.must_change_password ? "/change-password" : "/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-screen bg-chrome flex items-center justify-center">
      <form
        onSubmit={submit}
        className="bg-background border border-border rounded-xl p-8 w-full max-w-sm shadow-sm"
      >
        <h1 className="text-xl font-bold mb-1">Simple Agents</h1>
        <p className="text-muted-2 text-sm mb-6">Sign in to your server</p>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="username">Username</Label>
            <Input id="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Signing in…" : "Log in"}
          </Button>
        </div>
      </form>
    </div>
  );
}
```

- [ ] **Step 3: `web/ui/src/pages/ChangePassword.tsx`** (same skeleton, fields `password`/`confirm`, POST `/api/v1/auth/change-password`, client-side check password===confirm → else inline error, on success invalidate session + `nav("/")`; heading "Change password", button "Save password"):

```tsx
import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export default function ChangePassword() {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const nav = useNavigate();
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setError("Passwords do not match");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.post("/api/v1/auth/change-password", { password, confirm });
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-screen bg-chrome flex items-center justify-center">
      <form onSubmit={submit} className="bg-background border border-border rounded-xl p-8 w-full max-w-sm shadow-sm">
        <h1 className="text-xl font-bold mb-6">Change password</h1>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="password">New password</Label>
            <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="confirm">Confirm password</Label>
            <Input id="confirm" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Saving…" : "Save password"}
          </Button>
        </div>
      </form>
    </div>
  );
}
```

- [ ] **Step 4: Wire into router** — in `router.tsx` replace the two placeholders with `<Login />` / `<ChangePassword />` (import them). Update `App.test.tsx` expectation if needed (`/log in/i` button exists on /app/login).

- [ ] **Step 5: Run + commit** — `npm test` → PASS; `npm run build` clean. `git add -A && git commit -m "feat(ui): login + change-password screens against /api/v1/auth"`

---

### Task 6: App shell — rail, context pane, slide-over, responsive

**Files:**
- Create: `web/ui/src/components/shell/AppShell.tsx`, `web/ui/src/components/shell/IconRail.tsx`, `web/ui/src/components/shell/SlideOver.tsx`, `web/ui/src/components/shell/shell.test.tsx`
- Modify: `web/ui/src/router.tsx` (mount AppShell as the guarded layout)

**Interfaces:**
- Produces (later sub-plans build on these exact APIs):
  - `<AppShell>` — guarded layout route element; renders `<IconRail />`, an optional context pane, `<Outlet />` content, and the slide-over host. Pages declare a context pane by rendering `<AppShell.ContextPane>…</AppShell.ContextPane>` — implemented via a small context (`ShellCtx`) with `setContextPane(node)`; for THIS sub-plan the pane renders only when a page provides one (Home/KB/Chats/Connections do so in later sub-plans).
  - `useSlideOver(): { open(content: React.ReactNode, opts?: { title?: string }): void; close(): void }` — the universal right-panel API (Slack-thread style), backed by shadcn `Sheet` with `side="right"`.
  - `railItems` config: Home `/`, Knowledge Base `/kb`, Agents `/agents`, Skills `/skills`, Connections `/connections`, Chats `/chats`, Secrets `/secrets` (lucide icons: House, Library, Bot, Sparkles, Plug, MessageSquare, KeyRound), profile (Avatar → `/settings`) pinned at bottom, workspace button pinned at top (Task 7 fills its menu).
  - Responsive (spec §4): below `md` the rail becomes a fixed bottom bar (icons only, workspace+profile included), context pane hidden, slide-over full-screen (`Sheet` `className="w-full sm:max-w-md"`).

- [ ] **Step 1: Failing shell test** — `web/ui/src/components/shell/shell.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell, useSlideOver } from "./AppShell";

function Page() {
  const { open } = useSlideOver();
  return (
    <button onClick={() => open(<div>PANEL-CONTENT</div>, { title: "Details" })}>
      open panel
    </button>
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          authenticated: true,
          owner: { id: "o1", username: "admin", must_change_password: false },
          workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
          workspaces: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Page />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("renders rail items and opens the slide-over", async () => {
  wrap();
  expect(await screen.findByLabelText(/agents/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/knowledge base/i)).toBeInTheDocument();
  await userEvent.click(screen.getByText("open panel"));
  expect(await screen.findByText("PANEL-CONTENT")).toBeInTheDocument();
  expect(screen.getByText("Details")).toBeInTheDocument();
});
```

Run `npm test` → FAIL.

- [ ] **Step 2: `web/ui/src/components/shell/AppShell.tsx`**

```tsx
import { createContext, useContext, useState } from "react";
import { Outlet } from "react-router";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import IconRail from "./IconRail";

type SlideOverState = { node: React.ReactNode; title?: string } | null;

const ShellCtx = createContext<{
  openPanel: (node: React.ReactNode, opts?: { title?: string }) => void;
  closePanel: () => void;
  setContextPane: (node: React.ReactNode | null) => void;
}>({ openPanel: () => {}, closePanel: () => {}, setContextPane: () => {} });

export function useSlideOver() {
  const { openPanel, closePanel } = useContext(ShellCtx);
  return { open: openPanel, close: closePanel };
}

export function useContextPane() {
  return useContext(ShellCtx).setContextPane;
}

export function AppShell() {
  const [panel, setPanel] = useState<SlideOverState>(null);
  const [contextPane, setContextPane] = useState<React.ReactNode | null>(null);

  return (
    <ShellCtx.Provider
      value={{
        openPanel: (node, opts) => setPanel({ node, title: opts?.title }),
        closePanel: () => setPanel(null),
        setContextPane,
      }}
    >
      <div className="h-screen flex flex-col md:flex-row bg-background">
        <IconRail />
        {contextPane && (
          <aside className="hidden md:flex w-64 shrink-0 flex-col border-r border-border bg-chrome/60 overflow-y-auto">
            {contextPane}
          </aside>
        )}
        <main className="flex-1 min-w-0 overflow-y-auto pb-16 md:pb-0">
          <Outlet />
        </main>
        <Sheet open={panel !== null} onOpenChange={(o) => !o && setPanel(null)}>
          <SheetContent side="right" className="w-full sm:max-w-md p-0 flex flex-col">
            <SheetHeader className="border-b border-border px-4 py-3">
              <SheetTitle className="text-sm font-bold">{panel?.title ?? ""}</SheetTitle>
            </SheetHeader>
            <div className="flex-1 overflow-y-auto p-4">{panel?.node}</div>
          </SheetContent>
        </Sheet>
      </div>
    </ShellCtx.Provider>
  );
}
```

- [ ] **Step 3: `web/ui/src/components/shell/IconRail.tsx`**

```tsx
import { NavLink } from "react-router";
import {
  House, Library, Bot, Sparkles, Plug, MessageSquare, KeyRound,
} from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session";

export const railItems = [
  { to: "/", label: "Home", icon: House },
  { to: "/kb", label: "Knowledge Base", icon: Library },
  { to: "/agents", label: "Agents", icon: Bot },
  { to: "/skills", label: "Skills", icon: Sparkles },
  { to: "/connections", label: "Connections", icon: Plug },
  { to: "/chats", label: "Chats", icon: MessageSquare },
  { to: "/secrets", label: "Secrets", icon: KeyRound },
];

// WorkspaceButton is a plain initial-badge here; Task 7 replaces it with the
// workspace menu (switch/create/leave).
function WorkspaceButton() {
  const { data } = useSession();
  const initial = data?.workspace?.name?.[0]?.toUpperCase() ?? "?";
  return (
    <div
      aria-label="Workspace"
      className="size-9 rounded-lg bg-foreground text-background flex items-center justify-center font-bold"
    >
      {initial}
    </div>
  );
}

export default function IconRail() {
  return (
    <nav
      aria-label="Primary"
      className="fixed bottom-0 inset-x-0 z-20 flex flex-row items-center justify-around border-t border-border bg-chrome px-2 py-1
                 md:static md:h-full md:w-14 md:flex-col md:justify-start md:border-t-0 md:border-r md:py-3 md:gap-1.5"
    >
      <div className="hidden md:block mb-2">
        <WorkspaceButton />
      </div>
      {railItems.map(({ to, label, icon: Icon }) => (
        <Tooltip key={to}>
          <TooltipTrigger asChild>
            <NavLink
              to={to}
              aria-label={label}
              className={({ isActive }) =>
                `flex size-9 items-center justify-center rounded-lg transition-colors ${
                  isActive ? "bg-border text-foreground" : "text-muted hover:bg-border/60"
                }`
              }
            >
              <Icon className="size-[18px]" />
            </NavLink>
          </TooltipTrigger>
          <TooltipContent side="right">{label}</TooltipContent>
        </Tooltip>
      ))}
      <div className="md:mt-auto">
        <Tooltip>
          <TooltipTrigger asChild>
            <NavLink to="/settings" aria-label="Profile & Settings">
              <Avatar className="size-8">
                <AvatarFallback className="bg-accent-soft text-accent text-xs font-semibold">
                  <ProfileInitial />
                </AvatarFallback>
              </Avatar>
            </NavLink>
          </TooltipTrigger>
          <TooltipContent side="right">Profile &amp; Settings</TooltipContent>
        </Tooltip>
      </div>
    </nav>
  );
}

function ProfileInitial() {
  const { data } = useSession();
  return <>{data?.owner?.username?.[0]?.toUpperCase() ?? "?"}</>;
}
```

If shadcn's Tooltip requires a `TooltipProvider`, wrap it once inside `AppShell` (around the whole layout div) — the test will tell you.

- [ ] **Step 4: Mount in router** — in `router.tsx`, replace the guarded `element: <Outlet />` layer with `element: <AppShell />` (import from `@/components/shell/AppShell`). `SlideOver.tsx` is not a separate file after all — the Sheet host lives in AppShell; do NOT create a file that just re-exports (YAGNI). Remove it from this task's file list when committing.

- [ ] **Step 5: Run + commit** — `npm test` → PASS (shell test + all prior); `npm run build` clean. `git add -A && git commit -m "feat(ui): Slack-style app shell — icon rail, context-pane slot, right slide-over, responsive"`

---

### Task 7: Workspace flows — picker, enter (master password), create, switch, leave

**Files:**
- Create: `web/ui/src/pages/Workspaces.tsx`, `web/ui/src/components/shell/WorkspaceMenu.tsx`, `web/ui/src/pages/Workspaces.test.tsx`
- Modify: `web/ui/src/router.tsx` (swap placeholder), `web/ui/src/components/shell/IconRail.tsx` (WorkspaceButton → WorkspaceMenu)

**Interfaces:**
- Consumes: `api`, `useSession`, session invalidation; endpoints `POST /api/v1/workspaces` `{name,about}`, `POST /api/v1/workspaces/:id/enter` `{master_password}` (401 `wrong_master_password`; response `{ok,needs_setup}`), `POST /api/v1/workspaces/leave`.
- Produces: `/workspaces` full-screen picker (used when logged in but no active workspace); `<WorkspaceMenu />` in the rail (top icon → dropdown: current workspace, switch list, "Create workspace", "Leave workspace"). Entering any workspace ALWAYS prompts for its master password (spec §4 — re-prompt on every switch); a `needs_setup` workspace enters without password and routes to `/workspaces?setup=<id>` which shows the "finish setup in the classic UI at /dashboard/setup" note (full onboarding replaces this in sub-plan 5).

- [ ] **Step 1: Failing test** — `web/ui/src/pages/Workspaces.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import Workspaces from "./Workspaces";

const session = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: null,
  workspaces: [
    { id: "w1", name: "personal", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  ],
};

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Workspaces />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("enter flow prompts for master password and surfaces wrong-password error", async () => {
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url);
    if (u.endsWith("/auth/session"))
      return Promise.resolve(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }));
    if (u.endsWith("/workspaces/w1/enter"))
      return Promise.resolve(
        new Response(JSON.stringify({ error: { code: "wrong_master_password", message: "Incorrect master password" } }), {
          status: 401, headers: { "Content-Type": "application/json" },
        }),
      );
    return Promise.resolve(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  });
  vi.stubGlobal("fetch", fetchMock);

  wrap();
  await userEvent.click(await screen.findByText("personal"));
  // password dialog appears
  const pw = await screen.findByLabelText(/master password/i);
  await userEvent.type(pw, "nope");
  await userEvent.click(screen.getByRole("button", { name: /enter/i }));
  await waitFor(() => expect(screen.getByText(/incorrect master password/i)).toBeInTheDocument());
});
```

Run `npm test` → FAIL.

- [ ] **Step 2: `web/ui/src/pages/Workspaces.tsx`**

```tsx
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { useSession, type Workspace } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";

export function EnterWorkspaceDialog({
  ws, onClose,
}: { ws: Workspace | null; onClose: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const nav = useNavigate();
  const qc = useQueryClient();

  async function enter(e: React.FormEvent) {
    e.preventDefault();
    if (!ws) return;
    setBusy(true);
    setError("");
    try {
      await api.post<{ ok: boolean; needs_setup: boolean }>(
        `/api/v1/workspaces/${ws.id}/enter`,
        { master_password: password },
      );
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={ws !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Enter “{ws?.name}”</DialogTitle>
        </DialogHeader>
        <form onSubmit={enter} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="master_password">Master password</Label>
            <Input
              id="master_password" type="password" value={password}
              onChange={(e) => setPassword(e.target.value)} autoFocus
            />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Entering…" : "Enter workspace"}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function CreateWorkspaceDialog({
  open, onClose,
}: { open: boolean; onClose: () => void }) {
  const [name, setName] = useState("");
  const [about, setAbout] = useState("");
  const [error, setError] = useState("");
  const nav = useNavigate();
  const qc = useQueryClient();

  async function create(e: React.FormEvent) {
    e.preventDefault();
    try {
      const ws = await api.post<Workspace>("/api/v1/workspaces", { name, about });
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav(`/workspaces?setup=${ws.id}`, { replace: true });
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Create workspace</DialogTitle>
        </DialogHeader>
        <form onSubmit={create} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="ws-name">Name</Label>
            <Input id="ws-name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ws-about">About (optional)</Label>
            <Input id="ws-about" value={about} onChange={(e) => setAbout(e.target.value)} />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full">Create</Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export default function Workspaces() {
  const { data: session } = useSession();
  const [entering, setEntering] = useState<Workspace | null>(null);
  const [creating, setCreating] = useState(false);
  const [params] = useSearchParams();
  const setupID = params.get("setup");

  const list = session?.workspaces ?? [];

  return (
    <div className="min-h-screen bg-chrome flex items-center justify-center p-4">
      <div className="bg-background border border-border rounded-xl p-8 w-full max-w-md shadow-sm">
        <h1 className="text-xl font-bold mb-1">Workspaces</h1>
        <p className="text-muted-2 text-sm mb-6">
          Pick a workspace to enter — its master password is required every time.
        </p>
        {setupID && (
          <p className="text-sm bg-warn-soft text-foreground rounded-lg p-3 mb-4">
            This workspace still needs onboarding. Finish setup in the classic UI
            at <a className="underline" href="/dashboard/setup">/dashboard/setup</a>{" "}
            (the guided setup moves here in a later phase).
          </p>
        )}
        <ul className="space-y-2">
          {list.map((ws) => (
            <li key={ws.id}>
              <button
                onClick={() =>
                  ws.needs_setup
                    ? api
                        .post(`/api/v1/workspaces/${ws.id}/enter`, {})
                        .then(() => (window.location.href = "/app/workspaces?setup=" + ws.id))
                    : setEntering(ws)
                }
                className="w-full text-left border border-border rounded-lg px-4 py-3 hover:bg-chrome transition-colors"
              >
                <span className="font-semibold">{ws.name}</span>
                {ws.needs_setup && <span className="text-warn text-xs ml-2">needs setup</span>}
                {ws.about && <span className="block text-muted-2 text-xs mt-0.5">{ws.about}</span>}
              </button>
            </li>
          ))}
        </ul>
        <Button variant="outline" className="w-full mt-4" onClick={() => setCreating(true)}>
          + Create workspace
        </Button>
        <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
        <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
      </div>
    </div>
  );
}
```

- [ ] **Step 3: `web/ui/src/components/shell/WorkspaceMenu.tsx`** — rail-top dropdown reusing the same dialogs:

```tsx
import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { useSession, type Workspace } from "@/lib/session";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { EnterWorkspaceDialog, CreateWorkspaceDialog } from "@/pages/Workspaces";

export default function WorkspaceMenu() {
  const { data: session } = useSession();
  const [entering, setEntering] = useState<Workspace | null>(null);
  const [creating, setCreating] = useState(false);
  const nav = useNavigate();
  const qc = useQueryClient();
  const current = session?.workspace;

  async function leave() {
    await api.post("/api/v1/workspaces/leave");
    await qc.invalidateQueries({ queryKey: ["session"] });
    nav("/workspaces", { replace: true });
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label="Workspace"
          className="size-9 rounded-lg bg-foreground text-background flex items-center justify-center font-bold"
        >
          {current?.name?.[0]?.toUpperCase() ?? "?"}
        </DropdownMenuTrigger>
        <DropdownMenuContent side="right" align="start" className="w-56">
          <DropdownMenuLabel className="truncate">{current?.name}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          {(session?.workspaces ?? [])
            .filter((w) => w.id !== current?.id)
            .map((w) => (
              <DropdownMenuItem key={w.id} onSelect={() => setEntering(w)}>
                Switch to {w.name}
              </DropdownMenuItem>
            ))}
          <DropdownMenuItem onSelect={() => setCreating(true)}>+ Create workspace</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={leave}>Leave workspace</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
      <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
    </>
  );
}
```

In `IconRail.tsx`: replace `WorkspaceButton` with `<WorkspaceMenu />` (delete the local WorkspaceButton; also render WorkspaceMenu in the mobile bottom bar — move it out of the `hidden md:block` wrapper into a `mb-0 md:mb-2` one that shows on both).

- [ ] **Step 4: Wire router** — swap the `/workspaces` placeholder for `<Workspaces />`.

- [ ] **Step 5: Run + commit** — `npm test` → PASS (all suites); `npm run build` clean; `go test ./... -count=1 -timeout 120s` still green (no Go changes, sanity only). Then the end-to-end check against the real server:

```bash
cd /home/rookie/simple-agents-v2 && make build && SA_PORT=8090 ./bin/simple-agents serve & sleep 2
curl -s http://127.0.0.1:8090/app/login | grep -qi 'html' && echo LOGIN-SHELL-OK
kill %1
git add -A && git commit -m "feat(ui): workspace flows — picker, master-password enter, create, switch, leave"
```

---

### Task 8: Docs + ledger close-out

**Files:**
- Modify: `/home/rookie/simple-agents-v2/CLAUDE.md` (Commands + routes sections)

- [ ] **Step 1:** In CLAUDE.md's Commands block add after the Go build lines:

```bash
# Frontend (web/ui): build the SPA into the binary
make ui        # npm ci + vite build → web/ui/dist (embedded on next go build)
make build     # ui + go build (full artifact); make build-go for Go-only
# Dev loop: cd web/ui && npm run dev  (Vite on :5173, proxies /api to :8080)
```

In the routes section, under the `/api/v1` subsection added in sub-plan 1, add one line: `GET /app, /app/* — embedded SPA (React; 503 when built without make ui); moves to / in sub-plan 6.`

- [ ] **Step 2:** `go test ./... -count=1 -timeout 120s` + `cd web/ui && npm test && npm run build` → all green.
- [ ] **Step 3:** `git add -A && git commit -m "docs: SPA build pipeline + /app route"`

---

## Self-review notes (already applied)

- **Spec coverage:** §2 stack/embedding/dev-loop → Tasks 1-2; §5 tokens/dark → Task 3; §3 error envelope both shapes → Task 4; §4 shell/rail/pane/slide-over/responsive + workspace gate absorption → Tasks 6-7; login → Task 5. Onboarding wizard, per-page context panes, global chat button, command palette are LATER sub-plans by design (the slide-over + pane APIs they need are produced here).
- **Placeholder scan:** the `/workspaces?setup=` note deliberately points at the still-live classic setup wizard — that's the sub-plan-5 boundary, not a TODO.
- **Type consistency:** `useSlideOver().open(node, {title})`, `useContextPane()(node)`, `api.get/post/put/del`, `Session`/`Workspace` types, `railItems` — defined once (Tasks 4/6) and consumed by name in Task 7; later sub-plans import these exact names.
- Frontend tests mock `fetch` globally per test (`vi.stubGlobal`) — no msw dependency (YAGNI at this scale).
