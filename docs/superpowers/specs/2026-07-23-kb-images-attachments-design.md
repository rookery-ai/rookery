# KB images & attachments via the `/` menu (Theme D) — design

**Date:** 2026-07-23
**Status:** approved (design, self-authorized under user delegation)
**Scope:** insert images and file attachments into notes from the editor's `/`
menu; store them in the vault; serve them with correct MIME so they render;
image picker supports both upload-from-computer and pick-from-KB. Reported #4.

---

## Storage & serving (backend)

- **Assets live in the vault** under a per-note-agnostic **`assets/`** top-level
  folder (`<vault>/assets/<uuid-or-slug>.<ext>`). Kept as raw bytes — NOT run
  through `internal/convert` (that's for turning docs into markdown; an embedded
  image must stay an image). `assets/` is added to `kbSystemDirs` so it's grouped
  as system/managed in the tree (visible, not muted-critical).
- **`POST /api/v1/kb/asset`** (multipart `{file}`) — caps at the shared
  `iolimit` 25 MiB, sniffs content type, stores under `assets/`, returns
  `{path, url, kind, content_type}`. Reuses `vault.Resolve` for safety. Distinct
  from `/kb/upload` (which converts to markdown); this preserves bytes.
- **Serving:** extend the existing `GET /api/v1/kb/raw` to set a **sniffed
  `Content-Type`** (via `http.DetectContentType` / extension) instead of the
  current hard-coded `text/plain`, so `<img src="/api/v1/kb/raw?path=assets/…">`
  renders. Add `Cache-Control` for assets. (A dedicated `/kb/asset/:path` GET is
  an alternative; extending `/kb/raw` is less surface. Chosen: extend `/kb/raw`.)
- **KB supports images generally:** because `/kb/raw` now serves correct MIME,
  an image file opened from the tree renders in a viewer too (see FileViewer
  note below).

## Markdown representation

- Images serialize as standard markdown `![alt](<src>)`. **`src` is stored as a
  vault-relative path** (`assets/foo.png`) — portable, not tied to a URL. On
  load into the editor, a transform maps `assets/…` → the served URL
  (`/api/v1/kb/raw?path=assets/…`) for display; on save, it maps back to the
  bare vault-relative path so the on-disk markdown stays portable and agent-
  readable. Implemented in the TipTap Image extension's parse/serialize
  (mirroring the wikilink atom-node pattern) so round-trip fidelity holds and
  `checkFidelity` is unaffected.
- Non-image attachments serialize as a link `[filename](assets/…)` with the same
  path↔URL mapping.

## Editor `/` menu (frontend)

Add to `slashItems.ts` (the pattern the user cited — "like headers and tables"):
- **Image** → opens the **image picker dialog** (below), inserts a TipTap Image
  node on choice.
- **Attachment / File** → opens a file input (or the KB picker), uploads via
  `/kb/asset`, inserts a link.

**Image picker dialog** (`ImagePicker.tsx`) — two tabs:
1. **Upload from computer** — a file input (accept `image/*`) → `POST /kb/asset`
   → insert. Drag-drop supported.
2. **From knowledge base** — browse existing `assets/` images (and any image
   file in the vault) as a thumbnail grid → pick → insert. Backed by
   `GET /api/v1/kb/folders` + tree listing filtered to image extensions, or a
   small `GET /api/v1/kb/assets` convenience endpoint returning image paths.
   **Chosen:** `GET /api/v1/kb/assets` → `{assets: [{path, url}]}` (walk vault
   for image extensions) — one call, no client-side tree crawling.

TipTap `@tiptap/extension-image` is already installed and in `buildExtensions`;
this wires insertion + the path↔URL transform + the picker UI around it.

## FileViewer

`FileViewer.tsx` currently handles code/binary. Add an **image branch**: when the
opened file's `kind` is binary AND its content-type (from the note response or a
HEAD on `/kb/raw`) is an image, render an `<img>` from `/kb/raw` instead of the
download-only panel. (Backend: `apiGetKBNote` can set `kind:"image"` for image
files under the size cap, or the client infers from extension — **chosen:**
backend adds `kind:"image"` so the client stays dumb.)

## Testing

- **Backend (Go):** `/kb/asset` stores bytes + returns url/kind; `/kb/raw`
  serves image MIME; `/kb/assets` lists image files; size cap enforced;
  `api_parity_test.go` updated.
- **Frontend (vitest):** slash items include Image/Attachment; image picker
  upload + pick-from-KB insert an Image node; the path↔URL transform round-trips
  (`assets/x.png` in markdown ↔ served URL in the DOM) and `checkFidelity`
  passes an image body; FileViewer renders an image file.

## Non-goals
- Image editing/resizing/cropping (insert as-is).
- Pasting images from clipboard (nice-to-have; can follow — the `/`-menu upload
  is the committed path).
- External/hotlinked image URLs (allowed to render if typed, but the feature is
  vault-stored assets).
