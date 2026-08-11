import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import {
  BACKUP_WARNING_DISMISSED_KEY,
  BackupWarningBanner,
} from "./BackupWarningBanner";

const BASE = {
  enabled: false,
  destination: "local",
  schedule: "daily",
  hour: 3,
  weekday: 0,
  retention: 7,
  passphrase_set: false,
  local_dir: "/data/backups",
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
};

function mount(config: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ ...BASE, ...config }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/settings?section=owner-workspaces"]}>
        <BackupWarningBanner />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

it("warns when backups are not configured", async () => {
  mount({});
  expect(
    await screen.findByText(/backups are not enabled/i),
  ).toBeInTheDocument();
});

it("stays quiet when backups are enabled", async () => {
  mount({ enabled: true, passphrase_set: true });
  await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
  expect(screen.queryByText(/backups are not enabled/i)).not.toBeInTheDocument();
});

it("warns when a passphrase exists but the schedule is off", async () => {
  mount({ enabled: false, passphrase_set: true });
  expect(
    await screen.findByText(/backups are not enabled/i),
  ).toBeInTheDocument();
});

it("never returns once dismissed, even when backups are still off", async () => {
  const first = mount({});
  await screen.findByText(/backups are not enabled/i);
  await userEvent.click(screen.getByRole("button", { name: /dismiss/i }));
  expect(screen.queryByText(/backups are not enabled/i)).not.toBeInTheDocument();
  expect(localStorage.getItem(BACKUP_WARNING_DISMISSED_KEY)).toBe("1");
  first.unmount();

  // Dismissal is permanent by design: it is not cleared when backups are
  // enabled, so it must not come back when they are later turned off again.
  mount({});
  await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
  expect(screen.queryByText(/backups are not enabled/i)).not.toBeInTheDocument();
});
