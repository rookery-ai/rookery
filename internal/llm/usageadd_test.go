package llm

import (
	"reflect"
	"testing"
)

// Every field of Usage must survive summing.
//
// This is a REFLECTION test on purpose. Two hand-written summing functions
// existed — one in internal/coder, one in internal/agentrunner — and the second
// enumerated three fields, so CachedTokens and CacheReported were parsed
// correctly, carried out of the engine correctly, and then silently discarded
// one layer up. The run log said "n/a" for a provider that reports cache
// statistics on every response, and nothing failed.
//
// Asserting field-by-field would have the same blind spot as the code: a field
// added later is a field the test does not mention. Walking the struct means a
// new field fails here until Add handles it.
func TestUsageAddCarriesEveryField(t *testing.T) {
	// Non-zero in every field, so anything dropped shows up as a zero.
	b := Usage{
		PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8,
		CachedTokens: 2, CacheReported: true,
		Cost: 0.25, CostReported: true,
	}

	got := Usage{}.Add(b)

	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if f.IsZero() {
			t.Errorf("Add dropped %s — every field of Usage must be summed or carried", name)
		}
	}
}

// The counts add rather than being replaced: a run is the sum of its turns.
func TestUsageAddSumsCounts(t *testing.T) {
	a := Usage{PromptTokens: 10, TotalTokens: 10, CachedTokens: 4, Cost: 0.5}
	got := a.Add(Usage{PromptTokens: 7, TotalTokens: 7, CachedTokens: 1, Cost: 0.25})

	if got.PromptTokens != 17 || got.TotalTokens != 17 {
		t.Errorf("token counts did not sum: %+v", got)
	}
	if got.CachedTokens != 5 {
		t.Errorf("cached tokens = %d, want 5", got.CachedTokens)
	}
	if got.Cost != 0.75 {
		t.Errorf("cost = %v, want 0.75", got.Cost)
	}
}

// The flags OR rather than AND: one call reporting is enough to make the run's
// number meaningful, and requiring all of them would erase a real measurement
// the moment a single response omitted the field.
func TestUsageAddOrsTheReportedFlags(t *testing.T) {
	reported := Usage{CacheReported: true, CostReported: true}
	silent := Usage{}

	if got := silent.Add(reported); !got.CacheReported || !got.CostReported {
		t.Errorf("a reporting call was masked by a silent one: %+v", got)
	}
	if got := reported.Add(silent); !got.CacheReported || !got.CostReported {
		t.Errorf("a silent call erased an earlier measurement: %+v", got)
	}
	if got := silent.Add(silent); got.CacheReported || got.CostReported {
		t.Errorf("two silent calls invented a measurement: %+v", got)
	}
}
