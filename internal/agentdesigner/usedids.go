package agentdesigner

// mergeUsedIDs unions two lists of ids the build and the dry run each observed in
// use, preserving first-seen order and dropping duplicates and blanks.
//
// Order is preserved rather than sorted because these ids reach AutoBindTargets,
// and a stable order keeps a build's bindings reproducible; a set with random
// iteration order would make the same build bind the same connections in a
// different sequence each time, which is the kind of nondeterminism that turns a
// real regression into "it worked yesterday".
//
// Blank ids are dropped because an empty string is not a connection: it would
// survive into the bound set and then fail to resolve, which reads to the owner as
// a connection that vanished rather than as one that never existed.
//
// A nil result is deliberate when both inputs are empty. The field is persisted as
// JSON on agent_drafts and consumed as "did we observe anything?", so returning an
// allocated empty slice would only distinguish "observed nothing" from "observed
// nothing" at the cost of a marshalling difference.
func mergeUsedIDs(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, id := range list {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
