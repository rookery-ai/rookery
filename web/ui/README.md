# Simple Agents — SPA

Vite + React + TypeScript source for the Simple Agents web UI (Tailwind v4, shadcn/ui). This
replaces the legacy server-rendered templates; it is embedded into the Go binary and served
at `/app` (see `web/spa.go`).

```bash
npm install       # first time only
npm run dev       # dev server on :5173, proxies /api to the Go server on :8080
npm test -- --run # vitest
npm run build     # production build → dist/ (embedded via go:embed)
```

From the repo root, `make ui` runs the build and drops the output where the Go binary embeds it;
`make deploy` builds the UI as part of a full rebuild+restart.
