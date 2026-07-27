import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TimeZoneContext } from "@/lib/timezone";
import { ChatMessageBubble } from "./Bubbles";

// No QueryClientProvider here, deliberately: the bubble must render standalone
// (DesignerSurface and several component tests mount it with no query client
// above them), which is why the timezone arrives as context rather than a
// useSession() call inside MessageMeta.
function renderBubble(ui: React.ReactElement, timezone: string | undefined = "Asia/Tokyo") {
  return render(<TimeZoneContext.Provider value={timezone}>{ui}</TimeZoneContext.Provider>);
}

// 2026-07-26T12:00:00Z is Sunday noon UTC → 21:00 in Tokyo.
const ISO = "2026-07-26T12:00:00Z";

test("the footer renders the time in the workspace timezone", async () => {
  renderBubble(<ChatMessageBubble role="assistant" content="hi" createdAt={ISO} />);
  expect(await screen.findByText(/Sun.*21:00/)).toBeInTheDocument();
});

test("with no timezone context the bubble still renders (browser-local)", () => {
  renderBubble(<ChatMessageBubble role="assistant" content="hi" createdAt={ISO} />, undefined);
  expect(screen.getByTestId("message-time").textContent).not.toBe("");
});

// The footer must exist in the DOM at all times and be revealed with opacity.
// Mounting it on hover reflows the bubble under the cursor and kills an
// in-progress drag-select, which is the behaviour the user explicitly asked to
// preserve.
test("the footer is always mounted and hover-revealed by opacity only", () => {
  const { container } = renderBubble(<ChatMessageBubble role="user" content="hi" createdAt={ISO} />);
  const footer = container.querySelector('[data-testid="message-meta"]')!;
  expect(footer).toBeInTheDocument();
  expect(footer.className).toContain("opacity-0");
  expect(footer.className).toContain("group-hover:opacity-100");
});

test("the message body is selectable (select-none lives only on the footer)", () => {
  const { container } = renderBubble(<ChatMessageBubble role="user" content="selectable" createdAt={ISO} />);
  const body = container.querySelector('[data-testid="message-body"]')!;
  expect(body.className).not.toContain("select-none");
  expect(container.querySelector('[data-testid="message-meta"]')!.className).toContain("select-none");
});

test("copy writes the raw message text to the clipboard", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
  renderBubble(<ChatMessageBubble role="assistant" content="**bold** text" createdAt={ISO} />);
  await userEvent.click(screen.getByRole("button", { name: /copy message/i }));
  expect(writeText).toHaveBeenCalledWith("**bold** text");
  // Feedback: the control flips to a "Copied" state.
  await waitFor(() => expect(screen.getByRole("button", { name: /copied/i })).toBeInTheDocument());
});

// DesignerSurface renders bubbles with no timestamps — the footer must still
// give them a copy button rather than disappearing or rendering "Invalid Date".
test("with no createdAt the copy button remains and no time is shown", () => {
  renderBubble(<ChatMessageBubble role="assistant" content="hi" />);
  expect(screen.getByRole("button", { name: /copy message/i })).toBeInTheDocument();
  expect(screen.queryByTestId("message-time")).not.toBeInTheDocument();
});

// The server is reached over plain HTTP on the LAN (http://<host>:8080), which
// is NOT a secure context — so `navigator.clipboard` is undefined and the
// Clipboard API path throws before it can copy anything. This is the real
// reported failure: the button did nothing at all, silently.
test("copy falls back to execCommand when the Clipboard API is unavailable", async () => {
  vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
  const execCommand = vi.fn().mockReturnValue(true);
  document.execCommand = execCommand;

  renderBubble(<ChatMessageBubble role="assistant" content="fallback me" createdAt={ISO} />);
  await userEvent.click(screen.getByRole("button", { name: /copy message/i }));

  expect(execCommand).toHaveBeenCalledWith("copy");
  await waitFor(() => expect(screen.getByRole("button", { name: /copied/i })).toBeInTheDocument());
});

// A silent no-op is what let the original bug hide. When BOTH paths fail the
// control has to say so.
test("copy reports a failure when both the Clipboard API and execCommand fail", async () => {
  vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
  document.execCommand = vi.fn().mockReturnValue(false);

  renderBubble(<ChatMessageBubble role="assistant" content="nope" createdAt={ISO} />);
  await userEvent.click(screen.getByRole("button", { name: /copy message/i }));

  await waitFor(() => expect(screen.getByRole("button", { name: /copy failed/i })).toBeInTheDocument());
});
