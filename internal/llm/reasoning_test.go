package llm

import "testing"

// A reasoning model bills its thinking against the same completion budget as its
// answer, so on a hard turn it can spend the whole cap before emitting a single
// content token. The provider then returns finish_reason "length", empty
// content, and the thinking in a separate field.
//
// Reading only `content` made that indistinguishable from a model that returned
// nothing at all — which is how one agent's failure was diagnosed as a
// large-file problem four times before anyone asked the provider what it sent.
// This shape was captured from a live provider before it was written down.
func TestParseOpenAIResponse_CapturesReasoningAndTruncation(t *testing.T) {
	body := []byte(`{
	  "choices": [{
	    "finish_reason": "length",
	    "message": {"role": "assistant", "content": "", "reasoning": "We need answer user."}
	  }],
	  "usage": {"prompt_tokens": 10, "completion_tokens": 300, "total_tokens": 310}
	}`)
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.FinishReason != "length" {
		t.Errorf("finish reason = %q, want length", resp.FinishReason)
	}
	if resp.Content != "" {
		t.Errorf("content = %q, want empty", resp.Content)
	}
	if resp.Reasoning != "We need answer user." {
		t.Errorf("reasoning was dropped: %q", resp.Reasoning)
	}
}

// Both spellings occur against this one schema: OpenRouter normalizes to
// `reasoning`, DeepSeek's own API emits `reasoning_content`, and a `generic`
// OpenAI-compatible endpoint can be either. Reading one leaves the other
// invisible, which is the whole failure this parsing exists to prevent.
func TestParseOpenAIResponse_ReadsReasoningContentSpelling(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant",
	  "content":"","reasoning_content":"thinking here"}}]}`)
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Reasoning != "thinking here" {
		t.Errorf("reasoning_content spelling was dropped: %q", resp.Reasoning)
	}
}

// Reasoning must never displace a real answer.
func TestParseOpenAIResponse_ContentWinsOverReasoning(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant",
	  "content":"the answer","reasoning":"the thinking"}}]}`)
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Content != "the answer" {
		t.Errorf("content = %q, want the answer", resp.Content)
	}
}
