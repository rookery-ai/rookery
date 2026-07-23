package iolimit

import (
	"bytes"
	"errors"
	"testing"
)

func TestCappingWriterUnderLimit(t *testing.T) {
	var buf bytes.Buffer
	cw := NewCappingWriter(&buf, 10)
	n, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("under-limit write must not error: %v", err)
	}
	if n != 5 {
		t.Fatalf("n = %d, want 5", n)
	}
	if buf.String() != "hello" {
		t.Fatalf("buf = %q, want %q", buf.String(), "hello")
	}
}

func TestCappingWriterAtBoundary(t *testing.T) {
	var buf bytes.Buffer
	cw := NewCappingWriter(&buf, 5)
	n, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("exactly-at-limit write must be allowed: %v", err)
	}
	if n != 5 || buf.String() != "hello" {
		t.Fatalf("n=%d buf=%q, want 5/%q", n, buf.String(), "hello")
	}
}

func TestCappingWriterOverBoundary(t *testing.T) {
	var buf bytes.Buffer
	cw := NewCappingWriter(&buf, 5)
	_, err := cw.Write([]byte("hello!")) // 6 bytes, limit 5
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-limit write must be ErrTooLarge, got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("an over-limit write must not partially buffer, got %d bytes", buf.Len())
	}
}

func TestCappingWriterCumulativeOverLimit(t *testing.T) {
	var buf bytes.Buffer
	cw := NewCappingWriter(&buf, 5)
	if _, err := cw.Write([]byte("abc")); err != nil {
		t.Fatalf("first write (3 of 5) must pass: %v", err)
	}
	if _, err := cw.Write([]byte("de")); err != nil {
		t.Fatalf("second write reaching the exact cumulative limit must pass: %v", err)
	}
	if _, err := cw.Write([]byte("f")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write crossing the cumulative limit must be ErrTooLarge, got %v", err)
	}
	if buf.String() != "abcde" {
		t.Fatalf("buf = %q, want %q", buf.String(), "abcde")
	}
}

func TestCappingWriterReadableAfterPartialWrites(t *testing.T) {
	var buf bytes.Buffer
	cw := NewCappingWriter(&buf, 100)
	cw.Write([]byte("chunk1-"))
	cw.Write([]byte("chunk2"))
	if buf.String() != "chunk1-chunk2" {
		t.Fatalf("wrapped buffer = %q, want accumulated writes", buf.String())
	}
}
