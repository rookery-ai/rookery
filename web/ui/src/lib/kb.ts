import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./api";

export type KBNode = {
  name: string;
  display_name: string;
  path: string;
  is_dir: boolean;
  system: boolean;
  // Custom emoji icon ("" = default lucide icon). Stored out-of-band server-side
  // in the kb_icons setting; see web/api_kb.go.
  icon?: string;
};

// `order` is the user's drag-chosen sibling order for this directory, by node
// NAME — empty when they've never reordered it. Served with the nodes (see
// web/api_kb.go's apiKBTreeResponse) so opening a folder stays one request.
export type KBTree = { path: string; nodes: KBNode[]; order: string[] };

// "markdown" -> NoteEditor (WYSIWYG/raw); "code" -> FileViewer's read-only
// <pre>; "binary" -> FileViewer's Download-only panel (content is "").
// Decided server-side by content sniffing, not by extension — see
// web/api_kb.go's apiGetKBNote.
export type KBNoteKind = "markdown" | "code" | "binary" | "image";

export type KBNote = {
  path: string;
  content: string;
  html: string;
  backlinks: string[];
  kind: KBNoteKind;
  icon?: string;
};

// `title` is the server-resolved display name for `path` — for a reflected note
// (a chat transcript, an inbox notification, an agent run log) the filename is a
// UUID, so the path alone is not a usable label. Optional so a response from an
// older server still type-checks; callers fall back to the path.
export type KBSearchHit = { path: string; title?: string; line: number; snippet: string };

export const rawURL = (path: string) => `/api/v1/kb/raw?path=${encodeURIComponent(path)}`;

export function useKBTree(path: string) {
  return useQuery({
    queryKey: ["kb-tree", path],
    queryFn: () =>
      api
        .get<KBTree>(`/api/v1/kb/tree?path=${encodeURIComponent(path)}`)
        // Same defence as backlinks below: a nil Go slice marshals to null,
        // and every consumer here indexes into `order` unconditionally.
        .then((tree) => ({ ...tree, order: tree.order ?? [] })),
  });
}

// Persists a single directory's sibling order. Sending an empty list clears
// the stored order for that directory (back to the derived one).
export function useSaveKBOrder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ dir, names }: { dir: string; names: string[] }) =>
      api.put<{ ok: boolean }>("/api/v1/kb/order", { dir, names }),
    onSuccess: (_data, { dir }) => {
      qc.invalidateQueries({ queryKey: ["kb-tree", dir] });
    },
  });
}

export function useKBNote(path: string | null) {
  return useQuery({
    queryKey: ["kb-note", path],
    queryFn: () =>
      api
        .get<KBNote>(`/api/v1/kb/note?path=${encodeURIComponent(path!)}`)
        // Belt-and-braces alongside the backend fix (web/api.go's orEmpty):
        // a nil Go []string marshals to JSON null, and NoteEditor
        // unconditionally does `data.backlinks.length`/`.map`.
        .then((note) => ({ ...note, backlinks: note.backlinks ?? [] })),
    enabled: !!path,
  });
}

export function useSaveNote() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) =>
      api.put<{ ok: boolean }>("/api/v1/kb/note", { path, content }),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: ["kb-note", path] });
    },
  });
}

export function useNewNote() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, is_dir }: { path: string; is_dir: boolean }) =>
      api.post<{ ok: boolean }>("/api/v1/kb/new", { path, is_dir }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["kb-tree"] });
    },
  });
}

export function useDeleteNote() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path }: { path: string }) =>
      api.del<{ ok: boolean }>(`/api/v1/kb/note?path=${encodeURIComponent(path)}`),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: ["kb-tree"] });
      qc.removeQueries({ queryKey: ["kb-note", path] });
    },
  });
}

export function useRenameNote() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      api.post<{ ok: boolean }>("/api/v1/kb/rename", { from, to }),
    onSuccess: (_data, { from, to }) => {
      qc.invalidateQueries({ queryKey: ["kb-tree"] });
      qc.invalidateQueries({ queryKey: ["kb-note", from] });
      qc.invalidateQueries({ queryKey: ["kb-note", to] });
    },
  });
}

// Sets (or clears, with icon="") a node's custom emoji. Invalidates the tree
// and the open note so the new icon shows everywhere it's rendered.
export function useSetKBIcon() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, icon }: { path: string; icon: string }) =>
      api.put<{ ok: boolean }>("/api/v1/kb/icon", { path, icon }),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: ["kb-tree"] });
      qc.invalidateQueries({ queryKey: ["kb-note", path] });
    },
  });
}

// Flat list of every folder path in the vault (root "" included), for the
// new-note "Location" picker and the bulk-Move picker.
export function useKBFolders() {
  return useQuery({
    queryKey: ["kb-folders"],
    queryFn: () =>
      api
        .get<{ folders: string[] }>("/api/v1/kb/folders")
        .then((r) => r.folders ?? []),
  });
}

// Which export formats the host can currently produce (PDF depends on a
// headless renderer being installed server-side).
export type ExportFormats = { html: boolean; docx: boolean; pdf: boolean };

export function useExportFormats() {
  return useQuery({
    queryKey: ["kb-export-formats"],
    queryFn: () => api.get<ExportFormats>("/api/v1/kb/export/formats"),
    staleTime: 5 * 60 * 1000,
  });
}

export const exportURL = (path: string, format: "html" | "docx" | "pdf") =>
  `/api/v1/kb/export?path=${encodeURIComponent(path)}&format=${format}`;

// An embeddable asset (image/file) stored in the vault under assets/.
export type KBAsset = { path: string; url: string };
export type KBAssetUploadResult = { path: string; url: string; kind: string; content_type: string };

// Uploads a raw file (image/attachment) to the vault's assets/ folder and
// returns its portable path + served URL. Distinct from useUploadKBFile, which
// converts a document into markdown; this preserves the bytes.
export function useUploadKBAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (file: File): Promise<KBAssetUploadResult> => {
      const body = new FormData();
      body.append("file", file);
      const res = await fetch("/api/v1/kb/asset", { method: "POST", body, credentials: "same-origin" });
      const text = await res.text();
      let data: unknown = null;
      try {
        data = text ? JSON.parse(text) : null;
      } catch {
        /* non-JSON error body */
      }
      if (!res.ok) {
        const e = (data as { error?: { code?: string; message?: string } } | null)?.error;
        throw new ApiError(res.status, e?.code ?? "unknown", e?.message ?? res.statusText);
      }
      return data as KBAssetUploadResult;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["kb-tree"] });
      qc.invalidateQueries({ queryKey: ["kb-assets"] });
    },
  });
}

// Lists every image already stored in the vault, for the editor's
// "insert from knowledge base" picker.
export function useKBAssets(enabled = true) {
  return useQuery({
    queryKey: ["kb-assets"],
    queryFn: () => api.get<{ assets: KBAsset[] }>("/api/v1/kb/assets").then((r) => r.assets ?? []),
    enabled,
  });
}

export function useKBSearch(q: string) {
  return useQuery({
    queryKey: ["kb-search", q],
    queryFn: () => api.get<{ hits: KBSearchHit[] }>(`/api/v1/kb/search?q=${encodeURIComponent(q)}`),
    enabled: q.length >= 2,
  });
}

// KBUploadResult mirrors web/api_kb.go's apiUploadKBFile response. Warnings is
// how a lossy conversion (a scanned PDF that yielded no text, a spreadsheet
// with dropped formulas) declares itself — callers must surface it, not treat
// a 200 alone as "converted faithfully".
export type KBUploadResult = {
  note_path: string;
  original_path: string;
  kind: string;
  extractor: string;
  warnings: string[];
};

// useUploadKBFile posts a document to /api/v1/kb/upload as multipart/form-data
// (the generic `api` helper only ever sends JSON, so this builds the request
// by hand rather than stretching that helper to cover a second body shape).
// `dir` is optional — omitted, the backend files the note under notes/.
export function useUploadKBFile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ file, dir }: { file: File; dir?: string }): Promise<KBUploadResult> => {
      const body = new FormData();
      body.append("file", file);
      if (dir) body.append("dir", dir);
      const res = await fetch("/api/v1/kb/upload", { method: "POST", body, credentials: "same-origin" });
      const text = await res.text();
      let data: unknown = null;
      try {
        data = text ? JSON.parse(text) : null;
      } catch {
        /* non-JSON error body — fall through to the generic message below */
      }
      if (!res.ok) {
        const e = (data as { error?: { code?: string; message?: string } } | null)?.error;
        throw new ApiError(res.status, e?.code ?? "unknown", e?.message ?? res.statusText);
      }
      return data as KBUploadResult;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["kb-tree"] });
    },
  });
}
