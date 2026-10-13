import CodeBlock from "@tiptap/extension-code-block";

// The fence for a code block, sized to its content.
//
// CommonMark closes a fenced block at the first line of AT LEAST as many
// backticks as the opener, so a block whose text contains ``` must be fenced
// with four or more. tiptap-markdown's serializer hard-codes three, which makes
// any such block — a markdown snippet, a shell one-liner an agent ran — a note
// that is not a fixed point: the editor rewrites "````" to "```", the content
// then closes the block early, and checkFidelity opens the note READ-ONLY. The
// Go side already writes the long fence (internal/vault fenceFor); this is the
// editor half of the same rule.
export function fenceFor(text: string): string {
  let longest = 0;
  for (const run of text.match(/`+/g) ?? []) longest = Math.max(longest, run.length);
  return "`".repeat(Math.max(3, longest + 1));
}

export const FencedCodeBlock = CodeBlock.extend({
  addStorage() {
    return {
      markdown: {
        serialize(state: any, node: any) {
          const fence = fenceFor(node.textContent);
          state.write(fence + (node.attrs.language || "") + "\n");
          state.text(node.textContent, false);
          state.ensureNewLine();
          state.write(fence);
          state.closeBlock(node);
        },
        // Parsing is unchanged from tiptap-markdown's own code block: markdown-it
        // already honours a longer fence on the way in.
        parse: {
          setup(markdownit: any) {
            markdownit.set({ langPrefix: (this as any)?.options?.languageClassPrefix ?? "language-" });
          },
          updateDOM(element: HTMLElement) {
            element.innerHTML = element.innerHTML.replace(/\n<\/code><\/pre>/g, "</code></pre>");
          },
        },
      },
    };
  },
});
