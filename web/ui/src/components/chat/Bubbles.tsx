import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/lib/chats";
import { MessageMeta } from "./MessageMeta";

type ChatMessageBubbleProps = Pick<ChatMessage, "role" | "content"> & { createdAt?: string };

// Markdown renderer shared by every assistant/user bubble. Deliberately no
// rehype-raw — raw HTML in a message must render as inert text, not markup.
//
// `createdAt` is optional because DesignerSurface renders design-conversation
// turns through this same component and has no per-message timestamps; the
// footer then shows only the copy control.
export function ChatMessageBubble({ role, content, createdAt }: ChatMessageBubbleProps) {
  const isUser = role === "user";
  return (
    <div
      data-testid="bubble-row"
      className={cn("group flex w-full", isUser ? "justify-end" : "justify-start")}
    >
      <div className={cn("flex max-w-[75%] flex-col", isUser ? "items-end" : "items-start")}>
        <div
          className={cn(
            "rounded-2xl px-4 py-2 text-sm leading-relaxed break-words",
            isUser
              ? "bg-foreground text-background"
              : "bg-chrome text-foreground border border-border",
          )}
        >
          <div
            data-testid="message-body"
            className={cn(
              "max-w-none",
              "[&_p]:my-1 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0",
              "[&_pre]:my-2 [&_pre]:overflow-x-auto [&_code]:break-words",
              "[&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-5",
              "[&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-5",
              "[&_strong]:font-semibold [&_a]:underline",
              isUser ? "[&_a]:text-background" : "[&_a]:text-accent",
            )}
          >
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                a: ({ node: _node, ...props }) => (
                  <a {...props} target="_blank" rel="noreferrer noopener" />
                ),
              }}
            >
              {content}
            </ReactMarkdown>
          </div>
        </div>
        <MessageMeta content={content} createdAt={createdAt} />
      </div>
    </div>
  );
}

export function TypingIndicator({ label }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 px-4 py-2 text-sm text-muted">
      <span className="flex items-center gap-1">
        <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-2 [animation-delay:-0.3s]" />
        <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-2 [animation-delay:-0.15s]" />
        <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-2" />
      </span>
      {label && <span>{label}</span>}
    </div>
  );
}
