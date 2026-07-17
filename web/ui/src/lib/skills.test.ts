import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useSkills, useSkillDetail, useCoreSkill, useSkillActions } from "./skills";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

test("useSkills fetches the list + core skills + draft", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse({
        skills: [{ id: "s1", name: "My Skill", description: "desc", created_at: "2026-07-01T00:00:00Z" }],
        core_skills: [{ slug: "pdf", name: "pdf", description: "PDF handling" }],
        draft: null,
      }),
    ),
  );
  const { result } = renderHook(() => useSkills(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.skills).toHaveLength(1));
  expect(result.current.data?.core_skills).toHaveLength(1);
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/skills");
});

test("useSkillDetail is disabled for null id and fetches when set", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ id: "s1", name: "x", description: "", content: "" })));
  const { result, rerender } = renderHook(({ id }) => useSkillDetail(id), {
    wrapper: wrapper(),
    initialProps: { id: null as string | null },
  });
  expect(result.current.fetchStatus).toBe("idle");
  expect(fetch).not.toHaveBeenCalled();

  rerender({ id: "s1" });
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/skills/s1");
});

test("useCoreSkill is disabled for null slug and fetches when set", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ slug: "pdf", content: "# pdf" })));
  const { result, rerender } = renderHook(({ slug }) => useCoreSkill(slug), {
    wrapper: wrapper(),
    initialProps: { slug: null as string | null },
  });
  expect(result.current.fetchStatus).toBe("idle");

  rerender({ slug: "pdf" });
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/skills/core/pdf");
});

test("useSkillActions.create POSTs content", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ id: "s1", name: "x", description: "", content: "# x" }, 201)));
  const { result } = renderHook(() => useSkillActions(), { wrapper: wrapper() });
  let response: { id: string } | undefined;
  await act(async () => {
    response = await result.current.create("# x");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/skills");
  expect((init as RequestInit).method).toBe("POST");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ content: "# x" });
  expect(response?.id).toBe("s1");
});

test("useSkillActions.save PUTs content", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ id: "s1", name: "x", description: "", content: "# y" })));
  const { result } = renderHook(() => useSkillActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.save("s1", "# y");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/skills/s1");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ content: "# y" });
});

test("useSkillActions.del DELETEs /api/v1/skills/:id", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useSkillActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.del("s1");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/skills/s1");
  expect((init as RequestInit).method).toBe("DELETE");
});

test("useSkillActions.del invalidates the skills list query", async () => {
  let skills = [{ id: "s1", name: "x", description: "", created_at: "2026-07-01T00:00:00Z" }];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/skills" && method === "GET") {
        return Promise.resolve(jsonResponse({ skills, core_skills: [], draft: null }));
      }
      if (url === "/api/v1/skills/s1" && method === "DELETE") {
        skills = [];
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const list = renderHook(() => useSkills(), { wrapper: Wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  const actions = renderHook(() => useSkillActions(), { wrapper: Wrapper });
  await act(async () => {
    await actions.result.current.del("s1");
  });

  await waitFor(() => expect(list.result.current.isFetching).toBe(false));
  const listCalls = vi.mocked(fetch).mock.calls.filter((c) => String(c[0]) === "/api/v1/skills");
  expect(listCalls.length).toBeGreaterThan(1);
});
