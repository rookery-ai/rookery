import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import ChatsPage from "./ChatsPage";
import type { Chat, ChatMessage } from "@/lib/chats";

// Mirrors web/api_kb.go's apiUploadKBFile response shape.
type KBUploadResult = {
  note_path: string;
  original_path: string;
  kind: string;
  extractor: string;
  warnings: string[];
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

// The toast host renders a toast's message in two places (a visually-hidden
// aria-live announcer and the visible toast body) — scope to the visible
// <span> to disambiguate, matching the established helper in
// components/shell/toast.test.tsx and pages/kb/tree.test.tsx.
function toastBody(text: string | RegExp) {
  return screen.getByText(text, { selector: "span" });
}

let chats: Chat[];
let messages: Record<string, ChatMessage[]>;
// When true, the NEXT /messages POST fails with a 500 and the flag resets —
// lets a test force exactly one confirmation-turn failure while everything
// else (including the upload itself) succeeds normally.
let failNextSend = false;

function resetFixtures() {
  chats = [
    { id: "c1", name: "Chat One", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-17T07:00:00Z" },
  ];
  messages = { c1: [{ role: "user", content: "hi" }, { role: "assistant", content: "hello there" }] };
  failNextSend = false;
}

// uploadFor maps a file's NAME to the canned /api/v1/kb/upload outcome each
// test needs — chosen so a single mockFetch implementation covers every
// scenario without per-test fetch overrides.
function uploadFor(name: string): Response {
  if (name === "big.bin") {
    return jsonResponse({ error: { code: "too_large", message: "file exceeds the 26214400 byte limit" } }, 413);
  }
  if (name === "bad.xyz") {
    return jsonResponse({ error: { code: "unsupported_format", message: "convert: unsupported format: .xyz" } }, 422);
  }
  if (name === "scan.pdf") {
    const result: KBUploadResult = {
      note_path: "notes/scan.pdf.md",
      original_path: "notes/scan.pdf",
      kind: "pdf",
      extractor: "pdftotext",
      warnings: ["scanned PDF yielded little extractable text"],
    };
    return jsonResponse(result);
  }
  const result: KBUploadResult = {
    note_path: `notes/${name}.md`,
    original_path: `notes/${name}`,
    kind: "text",
    extractor: "plain",
    warnings: [],
  };
  return jsonResponse(result);
}

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/chats" && method === "GET") return Promise.resolve(jsonResponse({ chats }));

      const detail = url.match(/^\/api\/v1\/chats\/([^/]+)$/);
      if (detail && method === "GET") {
        const chat = chats.find((c) => c.id === detail[1]);
        return Promise.resolve(jsonResponse({ chat, messages: messages[detail[1]] ?? [] }));
      }

      if (url === "/api/v1/kb/upload" && method === "POST") {
        const body = init?.body as FormData;
        const file = body.get("file") as File;
        return Promise.resolve(uploadFor(file.name));
      }

      const send = url.match(/^\/api\/v1\/chats\/([^/]+)\/messages$/);
      if (send && method === "POST") {
        if (failNextSend) {
          failNextSend = false;
          return Promise.resolve(
            jsonResponse({ error: { code: "internal", message: "coder crashed" } }, 500),
          );
        }
        const id = send[1];
        const body = JSON.parse(String(init?.body)) as { message: string };
        const response = `noted (${body.message.length} chars)`;
        messages[id] = [
          ...(messages[id] ?? []),
          { role: "user", content: body.message },
          { role: "assistant", content: response },
        ];
        return Promise.resolve(jsonResponse({ response }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/?chat=c1"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<ChatsPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
});

test("the attach control is a real, keyboard-reachable button distinct from the drop-only surface", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const button = screen.getByRole("button", { name: "Attach file" });
  expect(button.tagName).toBe("BUTTON");
  expect(button).not.toBeDisabled();

  // A real <button> with no explicit tabIndex is a normal tab stop —
  // proving it's keyboard-reachable, not a pointer/drop-only affordance.
  // (Walking the FULL app-chrome tab order — nav rail, workspace switcher,
  // chat list — to reach it would make this test assert on unrelated
  // shell layout instead of on the attach control itself.)
  expect(button.tabIndex).not.toBe(-1);
  button.focus();
  expect(document.activeElement).toBe(button);
  await userEvent.keyboard("{Enter}");

  // The underlying <input type="file"> also exists and is a real form
  // control (not a div faking one).
  const input = screen.getByLabelText("Attach file", { selector: "input" });
  expect(input.tagName).toBe("INPUT");
  expect((input as HTMLInputElement).type).toBe("file");
});

test("choosing a file uploads it and posts a confirmation message naming the note path", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const input = screen.getByLabelText("Attach file", { selector: "input" }) as HTMLInputElement;
  const file = new File(["hello world"], "doc.txt", { type: "text/plain" });
  await userEvent.upload(input, file);

  expect(await screen.findByText(/Attached/)).toBeInTheDocument();
  expect(screen.getByText("doc.txt")).toBeInTheDocument();
  expect(screen.getByText("notes/doc.txt.md")).toBeInTheDocument();
  // The confirmation was a real turn — the assistant's reply landed too.
  expect(await screen.findByText(/noted \(/)).toBeInTheDocument();
});

test("a 413 response toasts a clear size error and posts no confirmation message", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const input = screen.getByLabelText("Attach file", { selector: "input" }) as HTMLInputElement;
  const file = new File(["x".repeat(10)], "big.bin");
  await userEvent.upload(input, file);

  await waitFor(() => expect(toastBody(/too large/i)).toBeInTheDocument());
  expect(screen.queryByText(/Attached/)).not.toBeInTheDocument();
  expect(screen.queryByText(/noted \(/)).not.toBeInTheDocument();
});

test("a 422 response toasts a clear format error and posts no confirmation message", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const input = screen.getByLabelText("Attach file", { selector: "input" }) as HTMLInputElement;
  const file = new File(["x"], "bad.xyz");
  await userEvent.upload(input, file);

  await waitFor(() => expect(toastBody(/can't read this kind of file/i)).toBeInTheDocument());
  expect(screen.queryByText(/Attached/)).not.toBeInTheDocument();
  expect(screen.queryByText(/noted \(/)).not.toBeInTheDocument();
});

test("a multi-file batch reports every outcome: an earlier rejection isn't erased by a later success", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const input = screen.getByLabelText("Attach file", { selector: "input" }) as HTMLInputElement;
  const big = new File(["x".repeat(10)], "big.bin");
  const doc = new File(["hello"], "doc.txt", { type: "text/plain" });
  await userEvent.upload(input, [big, doc]);

  // doc.txt's success is still visible as an in-chat confirmation…
  expect(await screen.findByText("doc.txt")).toBeInTheDocument();
  expect(await screen.findByText(/noted \(/)).toBeInTheDocument();
  // …AND big.bin's rejection survived doc.txt's later success clearing
  // anything — it's its own toast, not the shared error banner.
  expect(toastBody(/too large/i)).toBeInTheDocument();
});

test("upload succeeds but the confirmation turn fails: the message says the file WAS imported, not that the import failed", async () => {
  mockFetch();
  failNextSend = true;
  wrap();
  await screen.findByText("hi");

  const input = screen.getByLabelText("Attach file", { selector: "input" }) as HTMLInputElement;
  const file = new File(["hello world"], "doc.txt", { type: "text/plain" });
  await userEvent.upload(input, file);

  const toastEl = await screen.findByText(/was imported/i, { selector: "span" });
  expect(toastEl.textContent).toContain("doc.txt");
  expect(toastEl.textContent).toContain("notes/doc.txt.md");
  // Must not read as a total/generic failure — the file really is in the KB.
  expect(toastEl.textContent).not.toMatch(/something went wrong/i);

  // The optimistic "📎 Attached…" bubble is still visible for this live
  // session even though nothing was persisted server-side (the toast above
  // is the durable record that survives a reload, where the bubble won't).
  expect(screen.getByText(/Attached/)).toBeInTheDocument();
});

test("a batch larger than the per-drop cap is rejected outright, with no partial uploads", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const dropZone = screen.getByTestId("chat-window");
  const files = Array.from({ length: 11 }, (_, i) => new File(["x"], `f${i}.txt`));
  const dataTransfer = { files, types: ["Files"] };

  fireEvent.dragOver(dropZone, { dataTransfer });
  fireEvent.drop(dropZone, { dataTransfer });

  await waitFor(() => expect(toastBody(/up to 10 files/i)).toBeInTheDocument());
  expect(screen.queryByText(/Attached/)).not.toBeInTheDocument();
});

test("a returned warnings array is surfaced in the confirmation message", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const input = screen.getByLabelText("Attach file", { selector: "input" }) as HTMLInputElement;
  const file = new File(["%PDF-1.4 ..."], "scan.pdf", { type: "application/pdf" });
  await userEvent.upload(input, file);

  expect(await screen.findByText(/Attached/)).toBeInTheDocument();
  expect(screen.getByText(/scanned PDF yielded little extractable text/)).toBeInTheDocument();
});

test("dragging a file onto the chat window uploads it and posts the same confirmation", async () => {
  mockFetch();
  wrap();
  await screen.findByText("hi");

  const dropZone = screen.getByTestId("chat-window");
  const file = new File(["hello"], "dragged.txt", { type: "text/plain" });
  const dataTransfer = { files: [file], types: ["Files"] };

  fireEvent.dragOver(dropZone, { dataTransfer });
  fireEvent.drop(dropZone, { dataTransfer });

  expect(await screen.findByText(/Attached/)).toBeInTheDocument();
  expect(screen.getByText("dragged.txt")).toBeInTheDocument();
  await waitFor(() => expect(screen.getByText(/noted \(/)).toBeInTheDocument());
});
