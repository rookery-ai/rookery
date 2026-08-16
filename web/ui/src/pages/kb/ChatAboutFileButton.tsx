import { MessageSquareText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useSlideOver } from "@/components/shell/AppShell";
import { GlobalChatPanel } from "@/components/chat/GlobalChatButton";

// chatPrompt is the message the composer opens prefilled with. It NAMES the
// file rather than inlining its content.
//
// That is deliberate and matches the platform's own retrieval model: the chat
// coder already runs rooted at the vault with file tools (Read/Grep/Glob on a
// CLI coder, read_file/search_files/glob on the API engine) and its system
// prompt names the vault root, so a path is all it needs to open the file
// itself. Inlining would fight that design in both directions — it would blow
// the context on a large note, and it would hand the model a stale snapshot
// that silently diverges from the file the moment either of them edits it.
//
// Exported for direct unit testing: the exact wording is the contract between
// this button and the coder's ability to resolve the path.
export function chatPrompt(path: string): string {
  return `About my knowledge base file \`${path}\` — `;
}

// The selection-scoped sibling of chatPrompt. It NAMES the file and QUOTES the
// passage — the passage because that is the thing the user is asking about,
// the path because the chat coder runs rooted at the vault with file tools and
// can open the note itself for surrounding context.
//
// Exported for direct unit testing: the exact wording is the contract between
// this button and the coder's ability to act on the right text.
export function selectionChatPrompt(path: string, selection: string): string {
  return `In my knowledge base file \`${path}\`, I've selected this passage:

> ${selection.split("\n").join("\n> ")}

`;
}

// selectionEditPrompt is selectionChatPrompt's SENT counterpart, for "Edit with
// AI". They differ because auto-sending the other one would be worse than the
// prefill it replaced: it ends in a blank line — a citation waiting for an
// instruction — and sent alone it asks the model nothing.
//
// "apply the change to the file directly" is load-bearing. Without it the model
// proposes a rewrite in the chat and writes nothing, so there is no external
// change for the open editor to pick up and the feature reads as broken from
// the other end.
export function selectionEditPrompt(path: string, selection: string): string {
  return `In my knowledge base file \`${path}\`, I've selected this passage:

> ${selection.split("\n").join("\n> ")}

Help me edit it. Ask me what I want changed if it isn't obvious, then apply the change to the file directly.`;
}

// A "Chat about this file" affordance for the knowledge base. Opens the SAME
// slide-over the global chat button uses, but always on a FRESH chat
// (forceNew) so a question about a note never lands mid-thread in an unrelated
// conversation.
export default function ChatAboutFileButton({ path }: { path: string }) {
  const { open } = useSlideOver();
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => open(<GlobalChatPanel forceNew initialText={chatPrompt(path)} />, { title: "Chat" })}
    >
      <MessageSquareText className="size-4" />
      Chat about this
    </Button>
  );
}
