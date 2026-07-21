import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type KBNode = {
  name: string;
  display_name: string;
  path: string;
  is_dir: boolean;
  system: boolean;
};

// `order` is the user's drag-chosen sibling order for this directory, by node
// NAME — empty when they've never reordered it. Served with the nodes (see
// web/api_kb.go's apiKBTreeResponse) so opening a folder stays one request.
export type KBTree = { path: string; nodes: KBNode[]; order: string[] };

// "markdown" -> NoteEditor (WYSIWYG/raw); "code" -> FileViewer's read-only
// <pre>; "binary" -> FileViewer's Download-only panel (content is "").
// Decided server-side by content sniffing, not by extension — see
// web/api_kb.go's apiGetKBNote.
export type KBNoteKind = "markdown" | "code" | "binary";

export type KBNote = { path: string; content: string; html: string; backlinks: string[]; kind: KBNoteKind };

export type KBSearchHit = { path: string; line: number; snippet: string };

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

export function useKBSearch(q: string) {
  return useQuery({
    queryKey: ["kb-search", q],
    queryFn: () => api.get<{ hits: KBSearchHit[] }>(`/api/v1/kb/search?q=${encodeURIComponent(q)}`),
    enabled: q.length >= 2,
  });
}
