import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChatMessageBubble } from "./Bubbles";
import { Composer } from "./Composer";

test("ChatMessageBubble renders markdown (bold + link target=_blank)", () => {
  render(
    <ChatMessageBubble
      role="assistant"
      content={"**bold** and a [link](https://example.com)"}
    />,
  );
  const strong = screen.getByText("bold");
  expect(strong.tagName).toBe("STRONG");
  const link = screen.getByRole("link", { name: "link" });
  expect(link).toHaveAttribute("href", "https://example.com");
  expect(link).toHaveAttribute("target", "_blank");
  expect(link).toHaveAttribute("rel", expect.stringContaining("noreferrer"));
});

test("ChatMessageBubble never renders raw HTML (no rehype-raw)", () => {
  render(<ChatMessageBubble role="assistant" content={"<img src=x onerror=alert(1)>"} />);
  expect(document.querySelector("img")).not.toBeInTheDocument();
  expect(screen.getByText(/onerror=alert\(1\)/)).toBeInTheDocument();
});

test("ChatMessageBubble alignment differs for user vs assistant", () => {
  const { rerender } = render(<ChatMessageBubble role="user" content="hi" />);
  const userRow = screen.getByTestId("bubble-row");
  expect(userRow.className).toContain("justify-end");

  rerender(<ChatMessageBubble role="assistant" content="hi" />);
  const assistantRow = screen.getByTestId("bubble-row");
  expect(assistantRow.className).toContain("justify-start");
});

test("Composer: Enter sends trimmed value and clears the box", async () => {
  const onSend = vi.fn();
  render(<Composer onSend={onSend} />);
  const box = screen.getByRole("textbox");
  await userEvent.type(box, "  hello world  ");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
  expect(onSend).toHaveBeenCalledWith("hello world");
  expect(box).toHaveValue("");
});

test("Composer: Shift+Enter does not send (newline instead)", async () => {
  const onSend = vi.fn();
  render(<Composer onSend={onSend} />);
  const box = screen.getByRole("textbox");
  await userEvent.type(box, "line one");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter", shiftKey: true });
  expect(onSend).not.toHaveBeenCalled();
});

test("Composer: busy disables the textarea and suppresses send", () => {
  const onSend = vi.fn();
  render(<Composer onSend={onSend} busy />);
  const box = screen.getByRole("textbox");
  expect(box).toBeDisabled();
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
  expect(onSend).not.toHaveBeenCalled();
});

test("Composer: whitespace-only send is a no-op", async () => {
  const onSend = vi.fn();
  render(<Composer onSend={onSend} />);
  const box = screen.getByRole("textbox");
  await userEvent.type(box, "   ");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
  expect(onSend).not.toHaveBeenCalled();
});

test("Composer: Enter during IME composition does not send", async () => {
  const onSend = vi.fn();
  render(<Composer onSend={onSend} />);
  const box = screen.getByRole("textbox");
  await userEvent.type(box, "hello");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter", isComposing: true });
  expect(onSend).not.toHaveBeenCalled();
});

test("Composer: initialText seeds an empty composer", () => {
  const onSend = vi.fn();
  render(<Composer onSend={onSend} initialText="hello from search" />);
  expect(screen.getByRole("textbox")).toHaveValue("hello from search");
});

// Reproduces the CommandPalette "Ask assistant" draft-clobber bug: the
// composer is NOT remounted when its parent (GlobalChatPanel) re-renders
// with a new `initialText` prop (React reconciles the same component type
// in the same position) — rerender() here is the faithful way to exercise
// that same-instance prop update, as opposed to render() which would mount
// a fresh instance every time.
test("Composer: a new initialText does NOT clobber an in-progress draft", async () => {
  const onSend = vi.fn();
  const { rerender } = render(<Composer onSend={onSend} initialText="first query" />);
  const box = screen.getByRole("textbox");
  expect(box).toHaveValue("first query");

  await userEvent.clear(box);
  await userEvent.type(box, "my unsent draft");
  expect(box).toHaveValue("my unsent draft");

  rerender(<Composer onSend={onSend} initialText="second query" />);
  expect(box).toHaveValue("my unsent draft");
});
