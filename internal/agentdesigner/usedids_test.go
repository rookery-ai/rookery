package agentdesigner

import (
	"reflect"
	"testing"
)

func TestMergeUsedIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []string
		want []string
		why  string
	}{{
		name: "dry run adds a connection the build never touched",
		a:    []string{"build-1"},
		b:    []string{"dry-1"},
		want: []string{"build-1", "dry-1"},
		why:  "this IS the bug: the rehearsal's evidence was discarded entirely",
	}, {
		name: "the build's ids are never replaced by the rehearsal's",
		a:    []string{"build-1", "build-2"},
		b:    []string{"dry-1"},
		want: []string{"build-1", "build-2", "dry-1"},
		why:  "substituting would unbind a connection the build proved was needed",
	}, {
		name: "an id both observed appears once",
		a:    []string{"shared", "build-only"},
		b:    []string{"shared", "dry-only"},
		want: []string{"shared", "build-only", "dry-only"},
	}, {
		name: "first-seen order is stable",
		a:    []string{"c", "a"},
		b:    []string{"b", "a"},
		want: []string{"c", "a", "b"},
		why:  "these reach AutoBindTargets; unstable order makes bindings irreproducible",
	}, {
		name: "blank ids are dropped",
		a:    []string{"", "real"},
		b:    []string{""},
		want: []string{"real"},
		why:  "an empty id binds nothing and then fails to resolve, reading as a lost connection",
	}, {
		name: "nothing observed stays nil",
		a:    nil,
		b:    nil,
		want: nil,
	}, {
		name: "only blanks observed stays nil",
		a:    []string{""},
		b:    []string{""},
		want: nil,
	}, {
		name: "no dry-run ids leaves the build's list untouched",
		a:    []string{"build-1", "build-2"},
		b:    nil,
		want: []string{"build-1", "build-2"},
		why:  "a failed or tool-less rehearsal must not disturb what the build found",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeUsedIDs(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergeUsedIDs(%v, %v) = %v, want %v\n%s", tc.a, tc.b, got, tc.want, tc.why)
			}
		})
	}
}

// The inputs must not be aliased into the result: usedConns is reassigned from
// this and then persisted, and a shared backing array would let a later append
// mutate the caller's slice.
func TestMergeUsedIDsDoesNotAliasItsInputs(t *testing.T) {
	a := []string{"build-1"}
	got := mergeUsedIDs(a, nil)
	if len(got) > 0 {
		got[0] = "mutated"
	}
	if a[0] != "build-1" {
		t.Fatalf("merging aliased its input: a[0] = %q", a[0])
	}
}
