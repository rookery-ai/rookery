import { reconcilePending } from "./ChatWindow";
import type { ChatMessage } from "@/lib/chats";

// Count-based reconciliation: a pending entry is only dropped once its
// {role, content} match is "consumed" by a corresponding fetched message.
// An existence-based filter (fresh.messages.some(...)) would incorrectly
// drop BOTH pending copies of a legitimately-repeated message when only one
// of the two actually landed server-side.

test("two identical pending entries + one matching fetched message: exactly one pending survives", () => {
  const pending: ChatMessage[] = [
    { role: "user", content: "hi" },
    { role: "user", content: "hi" },
  ];
  const fresh: ChatMessage[] = [{ role: "user", content: "hi" }];
  expect(reconcilePending(pending, fresh)).toEqual([{ role: "user", content: "hi" }]);
});

test("both pending entries drop once both land in fetched history", () => {
  const pending: ChatMessage[] = [
    { role: "user", content: "hi" },
    { role: "user", content: "hi" },
  ];
  const fresh: ChatMessage[] = [
    { role: "user", content: "hi" },
    { role: "user", content: "hi" },
  ];
  expect(reconcilePending(pending, fresh)).toEqual([]);
});

test("a pending entry with no match at all is kept", () => {
  const pending: ChatMessage[] = [{ role: "user", content: "not landed yet" }];
  const fresh: ChatMessage[] = [{ role: "user", content: "hi" }];
  expect(reconcilePending(pending, fresh)).toEqual(pending);
});

test("role distinguishes matches — same content, different role, does not consume", () => {
  const pending: ChatMessage[] = [{ role: "assistant", content: "hi" }];
  const fresh: ChatMessage[] = [{ role: "user", content: "hi" }];
  expect(reconcilePending(pending, fresh)).toEqual(pending);
});

test("empty fresh history keeps all pending entries", () => {
  const pending: ChatMessage[] = [{ role: "user", content: "hi" }, { role: "assistant", content: "yo" }];
  expect(reconcilePending(pending, [])).toEqual(pending);
});
