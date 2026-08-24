package llm

import "testing"

// A provider that reports cache statistics must have them carried through, or a
// run cannot be told apart from one paying full price for the same bytes on
// every turn.
func TestParseOpenAIResponse_CapturesCachedTokens(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
	  "usage":{"prompt_tokens":20000,"completion_tokens":50,"total_tokens":20050,
	           "prompt_tokens_details":{"cached_tokens":18000}}}`)
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Usage.CachedTokens != 18000 {
		t.Errorf("cached tokens = %d, want 18000", resp.Usage.CachedTokens)
	}
	if !resp.Usage.CacheReported {
		t.Error("cache was reported by the provider but not marked as such")
	}
}

// The distinction the whole field exists for: a provider that omits the details
// object has told us nothing, and must not be recorded as a zero-hit rate.
func TestParseOpenAIResponse_AbsentCacheDetailsAreNotZero(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
	  "usage":{"prompt_tokens":20000,"completion_tokens":50,"total_tokens":20050}}`)
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Usage.CacheReported {
		t.Error("an absent details object was reported as a real cache measurement")
	}
}

// A provider CAN legitimately report zero, and that is a finding rather than
// missing data — so it must be marked as reported.
func TestParseOpenAIResponse_ReportedZeroIsAMeasurement(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
	  "usage":{"prompt_tokens":20000,"prompt_tokens_details":{"cached_tokens":0}}}`)
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !resp.Usage.CacheReported {
		t.Error("a reported zero was treated as missing data")
	}
}

// Anthropic names the same quantity differently and puts it at the top level of
// usage; reading only the OpenAI spelling would leave that path blind.
func TestParseAnthropicResponse_CapturesCacheReads(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
	  "usage":{"input_tokens":9000,"output_tokens":40,"cache_read_input_tokens":8500}}`)
	resp, err := parseAnthropicResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Usage.CachedTokens != 8500 || !resp.Usage.CacheReported {
		t.Errorf("anthropic cache reads were dropped: %+v", resp.Usage)
	}
}
