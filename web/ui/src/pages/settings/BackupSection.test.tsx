import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BackupSection } from "./BackupSection";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function mockConfig(overrides: Record<string, unknown> = {}) {
  return {
    enabled: false,
    destination: "local",
    schedule: "daily",
    hour: 3,
    weekday: 0,
    retention: 7,
    passphrase_set: false,
    local_dir: "",
    s3: {
      endpoint: "",
      region: "",
      bucket: "",
      prefix: "",
      access_key: "",
      secret_key_set: false,
      path_style: false,
    },
    last_run_at: "0001-01-01T00:00:00Z",
    last_status: "",
    last_error: "",
    last_size: 0,
    next_run_at: "0001-01-01T00:00:00Z",
    pending_restore: false,
    ...overrides,
  };
}

// Only /api/v1/* is the JSON API; every other path falls through to the SPA
// catch-all, which answers 200 with index.html. So an unprefixed request does
// NOT fail loudly — it resolves with HTML that parses to null. Modelling that
// here is what makes a wrong path a test failure instead of a silent one.
function mountWith(config: Record<string, unknown>, snapshots: unknown[] = []) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (!url.startsWith("/api/v1/")) {
        return Promise.resolve(
          new Response("<!doctype html><title>SPA</title>", {
            status: 200,
            headers: { "Content-Type": "text/html" },
          }),
        );
      }
      if (url.startsWith("/api/v1/backup/snapshots")) return Promise.resolve(jsonResponse(snapshots));
      return Promise.resolve(jsonResponse(config));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <BackupSection />
    </QueryClientProvider>,
  );
}

describe("BackupSection", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("warns that the passphrase is the only way to recover", async () => {
    mountWith(mockConfig());
    expect(await screen.findByText(/only way to recover/i)).toBeInTheDocument();
  });

  // Regression guard. api.request() passes the path straight to fetch with no
  // prefix, so a bare "/backup/config" hit the SPA catch-all, came back as
  // 200 index.html, parsed to null, and rendered "Something went wrong" — with
  // a 200 in the server log, which is what made it hard to spot.
  it("calls the /api/v1-prefixed endpoints", async () => {
    mountWith(mockConfig({ enabled: true, passphrase_set: true }), []);
    await screen.findByText(/only way to recover/i);

    const urls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
    expect(urls.length).toBeGreaterThan(0);
    for (const u of urls) {
      expect(u).toMatch(/^\/api\/v1\//);
    }
    expect(urls).toContain("/api/v1/backup/config");
    expect(urls).toContain("/api/v1/backup/snapshots");
  });

  it("shows the last failure so a silently broken backup is visible", async () => {
    mountWith(
      mockConfig({
        enabled: true,
        passphrase_set: true,
        last_status: "error",
        last_error: "bucket unreachable",
        last_run_at: "2026-07-28T03:00:00Z",
      }),
    );
    expect(await screen.findByText(/bucket unreachable/i)).toBeInTheDocument();
  });

  it("surfaces a staged restore and how to cancel it", async () => {
    mountWith(mockConfig({ pending_restore: true }));
    expect(await screen.findByText(/applied the next time the server starts/i)).toBeInTheDocument();
    expect(screen.getByText(/cancel-restore/)).toBeInTheDocument();
  });

  it("requires typing RESTORE before the restore button enables", async () => {
    mountWith(mockConfig({ enabled: true, passphrase_set: true }), [
      { name: "rookery-20260729-030000.rkb", size: 12345, mod_time: "2026-07-29T03:00:00Z" },
    ]);

    const open = await screen.findByRole("button", { name: /restore from snapshot/i });
    await userEvent.click(open);

    const confirmBtn = await screen.findByRole("button", { name: /^restore$/i });
    expect(confirmBtn).toBeDisabled();

    await userEvent.type(screen.getByLabelText(/type restore to confirm/i), "RESTORE");
    await waitFor(() => expect(confirmBtn).toBeEnabled());
  });

  it("disables Back up now until a passphrase exists", async () => {
    mountWith(mockConfig({ passphrase_set: false }));
    const btn = await screen.findByRole("button", { name: /back up now/i });
    expect(btn).toBeDisabled();
  });

  it("does not resend the passphrase when one is already stored", async () => {
    mountWith(mockConfig({ enabled: true, passphrase_set: true, local_dir: "/mnt/b" }));
    await screen.findByText(/passphrase is set/i);

    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => {
      const calls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls;
      const put = calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
      expect(put).toBeTruthy();
      const body = JSON.parse((put![1] as RequestInit).body as string);
      // An empty passphrase would be indistinguishable from clearing it.
      expect(body.passphrase).toBeUndefined();
    });
  });
});
