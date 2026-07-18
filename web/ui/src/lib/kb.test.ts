import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useKBTree, useKBNote, rawURL } from "./kb";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

test("useKBTree fetches nodes for a path", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ path: "notes", nodes: [{ name: "a.md", display_name: "a", path: "notes/a.md", is_dir: false, system: false }] }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  )));
  const { result } = renderHook(() => useKBTree("notes"), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.nodes[0].path).toBe("notes/a.md"));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/kb/tree?path=notes");
});

test("useKBNote is disabled for null path", () => {
  vi.stubGlobal("fetch", vi.fn());
  const { result } = renderHook(() => useKBNote(null), { wrapper: wrapper() });
  expect(result.current.fetchStatus).toBe("idle");
  expect(fetch).not.toHaveBeenCalled();
});

test("rawURL encodes the path", () => {
  expect(rawURL("notes/my note.md")).toBe("/api/v1/kb/raw?path=notes%2Fmy%20note.md");
});
