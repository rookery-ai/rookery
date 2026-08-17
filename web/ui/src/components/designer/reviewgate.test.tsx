import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { DesignerSurface, type DesignerEndpoints, type DesignerLabels } from "./DesignerSurface";

// The review step is where a finished build waits to be saved, and it is the one
// place in the designer where typing the wrong word costs a whole rebuild. These
// tests pin the two halves of the fix:
//
//   1. The dry run and its actions must render even if the last transcript entry
//      is not an assistant turn. They used to be gated on the LAST message being
//      assistant, so anything landing after the dry run hid the output AND every
//      button — leaving a finished build with no visible way to accept it.
//   2. While those actions are showing, the composer is locked, so approval goes
//      through a button rather than through a guess at which words the server
//      accepts. "Request changes" is the key that unlocks it.
//
// The lock is deliberately tied to the SAME condition that renders the buttons:
// if the actions are ever hidden, the composer must come back. A blanket lock on
// the verifying state would strand the user with neither buttons nor a text box.

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners: Record<string, Array<() => void>> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  close() {}
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type Handler = (body: any, method: string) => Response | Promise<Response>;

function mockFetch(handlers: Record<string, Handler>) {
  const calls: Array<{ url: string; method: string; body: any }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });
      const key = Object.keys(handlers).find((k) => url.startsWith(k));
      if (!key) return Promise.resolve(jsonResponse({}, 404));
      return Promise.resolve(handlers[key]!(body, method));
    }),
  );
  return calls;
}

const ENDPOINTS: DesignerEndpoints = {
  design: "/x/design",
  cancel: "/x/cancel",
  resume: "/x/resume",
  dismiss: "/x/dismiss",
  progress: "/x/progress",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Review"],
  buildButton: "Build it",
  saveButton: "Save agent",
  entityName: "agent",
};

function wrap(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

async function sendViaComposer(text: string) {
  const box = screen.getByRole("textbox");
  await userEvent.type(box, text);
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// Drives a session to the review step and returns the recorded fetch calls.
async function reachReview() {
  const calls = mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe")
        return jsonResponse({
          response: "DRY RUN OUTPUT",
          done: false,
          state: "verifying",
        });
      return jsonResponse({ response: "saved", done: true, agent_id: "abc123" });
    },
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);
  await sendViaComposer("describe");
  await screen.findByRole("button", { name: LABELS.saveButton });
  return calls;
}

test("the review step locks the composer so approval goes through a button", async () => {
  await reachReview();

  // The dry run is on screen with its actions...
  expect(screen.getByText("DRY RUN OUTPUT")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: LABELS.saveButton })).toBeInTheDocument();

  // ...and the text box is not a way to guess at approval words.
  expect(screen.getByRole("textbox")).toBeDisabled();
});

test("Request changes unlocks the composer and a typed change still posts", async () => {
  const calls = await reachReview();

  fireEvent.click(screen.getByRole("button", { name: /request changes/i }));

  const box = await screen.findByRole("textbox");
  await waitFor(() => expect(box).not.toBeDisabled());

  await sendViaComposer("make it 7:30");
  await waitFor(() => {
    const design = calls.filter((c) => c.url === "/x/design");
    expect(design[design.length - 1]!.body).toEqual({ message: "make it 7:30" });
  });
});

test("Save agent finishes the session without the user typing anything", async () => {
  const onDone = vi.fn();
  const calls = mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe")
        return jsonResponse({ response: "DRY RUN", done: false, state: "verifying" });
      return jsonResponse({ response: "saved", done: true, agent_id: "abc123" });
    },
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={onDone} />);
  await sendViaComposer("describe");

  fireEvent.click(await screen.findByRole("button", { name: LABELS.saveButton }));

  await waitFor(() => expect(onDone).toHaveBeenCalledWith("abc123"));
  const design = calls.filter((c) => c.url === "/x/design");
  expect(design[design.length - 1]!.body).toEqual({ message: "save" });
});

test("a failed turn after the dry run does not hide the finished build", async () => {
  // The regression, and the reason it is worth a test: the review card and every
  // button were gated on the LAST transcript entry being an assistant turn. A
  // turn that FAILS leaves the user's own message last and clears busy — so the
  // finished build vanished from the page. No output, no Save, no Request
  // changes; the only remaining move was to guess a word the server accepts,
  // and guessing wrong silently rebuilds the agent.
  //
  // The build still exists on the server the whole time. Nothing about a failed
  // turn should make it unreachable.
  mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe")
        return jsonResponse({ response: "DRY RUN OUTPUT", done: false, state: "verifying" });
      return jsonResponse({ error: "something went wrong" }, 500);
    },
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("describe");
  await screen.findByRole("button", { name: LABELS.saveButton });

  fireEvent.click(screen.getByRole("button", { name: /request changes/i }));
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  await sendViaComposer("one more thought");

  // The error is reported...
  expect(await screen.findByText(/something went wrong/i)).toBeInTheDocument();
  // ...and the finished build is still on screen and still savable.
  //
  // Awaited rather than asserted synchronously: the error banner and the cleared
  // `busy` flag are two separate state updates, so the actions can return a render
  // AFTER the error text appears. Reading them in the same tick passed locally and
  // failed under the parallel full-suite run — a flaky merge gate, which is worse
  // than no gate because it teaches people to re-run until green.
  expect(await screen.findByText("DRY RUN OUTPUT")).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: LABELS.saveButton })).toBeInTheDocument();
});
