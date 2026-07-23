// Package iolimit holds the ONE shared "read at most N+1 bytes, then reject if
// over" pattern for every door into the system that receives a document,
// attachment, or request body whose size is caller-controlled and could be
// arbitrarily large.
//
// Before this package existed the same six-line pattern was hand-copied at
// four doors independently (the KB upload endpoint, the Telegram and Discord
// attachment downloads, the CLI-coder bridge's /convert and /search) — and
// then, when a FIFTH door needed it (the API engine's save_to_kb URL fetch),
// nothing forced that copy to match the other four: it read into a plain
// io.LimitReader with no over-limit check at all, so an oversized source was
// silently truncated instead of rejected. A single shared helper is what
// makes that class of drift structurally impossible going forward — every
// caller gets the same "reject with a clear size, don't silently cut" contract
// by construction.
package iolimit

import (
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge is wrapped into the error ReadCapped returns when the source
// exceeded the limit (as opposed to a genuine I/O failure reading it). It is
// exported so a caller that needs to render a distinct response for "too big"
// (typically HTTP 413) vs. "couldn't read it" (typically 400/500) can tell the
// two apart with errors.Is instead of parsing the error string.
var ErrTooLarge = errors.New("exceeds size limit")

// ReadCapped reads at most limit+1 bytes from r. Reading one byte past the
// limit is what makes an over-limit source DETECTABLE: a plain
// io.LimitReader(r, limit) can never distinguish "the source was exactly
// limit bytes" from "the source was larger and got cut off at limit" — both
// produce an identical limit-byte read. Reading one extra byte breaks that
// ambiguity: exactly limit+1 (or more, if r ignores the limit) bytes back
// means the source was over, which is reported as an ErrTooLarge-wrapped error
// naming the limit rather than silently returned as a truncated — and
// therefore CORRUPTED — result.
func ReadCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, limit)
	}
	return data, nil
}
