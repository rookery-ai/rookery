package prompts

import "fmt"

// KBAssistActions is the closed set of actions the KB editor's selection panel
// offers. The handler validates against this exact slice, so the API and the
// prompt builder can never disagree about what is supported.
func KBAssistActions() []string {
	return []string{"improve", "proofread", "explain", "reformat"}
}

// kbAssistInstruction is the per-action body. Three of the four return
// replacement text; "explain" deliberately does not, because its result is
// shown read-only and must never be pasted over the user's prose.
func kbAssistInstruction(action string) string {
	switch action {
	case "proofread":
		return "Correct spelling, grammar and punctuation in the passage below. " +
			"Preserve the author's wording, voice and meaning — fix errors only, do not " +
			"rewrite for style. Return only the rewritten passage, with no preamble, " +
			"no explanation and no code fence."
	case "reformat":
		return "Reformat the passage below for readability using markdown — headings, " +
			"lists, emphasis or a table where the content genuinely calls for one. " +
			"Do not add, remove or reword any information. Return only the rewritten " +
			"passage, with no preamble, no explanation and no code fence."
	case "explain":
		return "Explain the passage below in plain language: what it means, and any " +
			"term, reference or assumption a reader might not know. Be concise — a " +
			"short paragraph, not an essay. Do NOT rewrite or correct the passage; " +
			"this explanation is shown alongside it and is never pasted into the note."
	default: // "improve"
		return "Improve the writing of the passage below: clearer, tighter and better " +
			"organised, in the author's own voice. Keep every fact and claim exactly as " +
			"stated — do not add information, and do not remove any. Return only the " +
			"rewritten passage, with no preamble, no explanation and no code fence."
	}
}

// BuildKBAssistPrompt builds the one-shot, text-only prompt behind
// POST /api/v1/kb/assist. The note path is context, not an instruction to open
// the file: this call runs with WithNoTools, so the model has no file access
// and the passage in the prompt is all it can see.
func BuildKBAssistPrompt(action, path, selection string) string {
	return fmt.Sprintf(`You are helping edit a note in a personal knowledge base.

The note is %q. You cannot open it — the passage below is the only content you have.

%s

Passage:
---
%s
---`, path, kbAssistInstruction(action), selection)
}
