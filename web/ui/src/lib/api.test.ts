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
  const err = (await api.get("/api/v1/x").catch((e) => e)) as ApiError;
  expect(err).toBeInstanceOf(ApiError);
  expect(err.code).toBe("not_authenticated");
  expect(err.status).toBe(401);
  expect(err.message).toBe("log in first");
});

test("parses legacy string errors", async () => {
  mockFetchOnce(400, { error: "name is required" });
  const err = (await api.post("/api/v1/x", {}).catch((e) => e)) as ApiError;
  expect(err).toBeInstanceOf(ApiError);
  expect(err.code).toBe("legacy");
  expect(err.message).toBe("name is required");
});

// keepalive must stay opt-in, not the request-wrapper default: it shares a
// combined ~64KB body budget across the page, so an unconditional default
// could make an unrelated large PUT/POST start failing. Confirm a plain
// api.del() call doesn't set it, and that passing it through does.
test("del omits keepalive by default", async () => {
  mockFetchOnce(200, { ok: true });
  await api.del("/api/v1/x");
  const init = vi.mocked(fetch).mock.calls[0][1] as RequestInit;
  expect(init.keepalive).toBeUndefined();
});

test("del sets keepalive when explicitly requested", async () => {
  mockFetchOnce(200, { ok: true });
  await api.del("/api/v1/x", undefined, { keepalive: true });
  const init = vi.mocked(fetch).mock.calls[0][1] as RequestInit;
  expect(init.keepalive).toBe(true);
});
