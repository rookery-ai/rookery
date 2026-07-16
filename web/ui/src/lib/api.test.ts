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
