package iolimit

import (
	"fmt"
	"io"
)

// CappingWriter wraps w and fails the moment cumulative bytes written exceed
// limit, so a streaming download whose source has no size bound of its own
// (e.g. slack.Client.GetFile, which takes a plain io.Writer) is bounded AS IT
// IS WRITTEN rather than after the whole thing has already been buffered.
// This is the write-side analogue of ReadCapped: ReadCapped bounds a Reader
// callers pull from; CappingWriter bounds a Writer callers get pushed into,
// which is the only shape available when the download API insists on
// io.Writer (there is no stdlib io.LimitWriter).
//
// A Write call that would cross the limit is rejected in full — nothing from
// that call reaches the wrapped writer — so an over-limit source never ends
// up partially buffered either. Writing exactly limit bytes (whether in one
// call or accumulated across several) is allowed; the (limit+1)th byte is
// not.
type CappingWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

// NewCappingWriter returns a CappingWriter that forwards to w and rejects
// once cumulative writes would exceed limit bytes.
func NewCappingWriter(w io.Writer, limit int64) *CappingWriter {
	return &CappingWriter{w: w, limit: limit}
}

// Write implements io.Writer. If accepting all of p would push the running
// total over the limit, NONE of p is written and an ErrTooLarge-wrapped
// error is returned instead — matching ReadCapped's "reject, don't silently
// truncate" contract.
func (cw *CappingWriter) Write(p []byte) (int, error) {
	if cw.n+int64(len(p)) > cw.limit {
		return 0, fmt.Errorf("%w: %d bytes", ErrTooLarge, cw.limit)
	}
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}
