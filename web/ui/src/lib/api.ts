export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export type RequestOptions = {
  /** Survive the request past page/tab teardown (`beforeunload` handlers).
   *  Scope this per call site, never set it as the default: keepalive
   *  requests share a combined ~64KB body budget across the whole page, so
   *  turning it on unconditionally here could make an unrelated large
   *  PUT/POST (e.g. a KB note save) start failing outside any unload
   *  scenario. */
  keepalive?: boolean;
};

async function request<T>(method: string, path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
    ...(options?.keepalive ? { keepalive: true } : {}),
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
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  del: <T>(path: string, body?: unknown, options?: RequestOptions) => request<T>("DELETE", path, body, options),
};
