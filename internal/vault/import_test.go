package vault

import (
	"strings"
	"testing"
)

func TestImportFileWritesNoteWithFrontmatter(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	res, err := v.ImportFile(ws, ImportInput{
		Data:      []byte("Region,Sales\nEMEA,120\n"),
		Filename:  "q3 sales.csv",
		SourceURL: "https://example.com/q3.csv",
	})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.HasPrefix(res.NotePath, "notes/") || !strings.HasSuffix(res.NotePath, ".md") {
		t.Errorf("NotePath = %q, want a markdown note under notes/", res.NotePath)
	}

	data, err := v.ReadNote(ws, res.NotePath)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"---\n",
		`source: "https://example.com/q3.csv"`,
		"kind: csv",
		"extractor: pure-go",
		"converted_at:",
		"| Region | Sales |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note missing %q, got:\n%s", want, body)
		}
	}
}

func TestImportFileKeepsOriginal(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "data.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if res.OriginalPath == "" {
		t.Fatal("the original bytes must be preserved: conversion is lossy")
	}
	orig, err := v.ReadNote(ws, res.OriginalPath)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(orig) != "a,b\n1,2\n" {
		t.Errorf("original bytes altered: %q", orig)
	}
	note, _ := v.ReadNote(ws, res.NotePath)
	if !strings.Contains(string(note), res.OriginalPath) {
		t.Error("the note must link to the preserved original")
	}
}

func TestImportFileSanitizesName(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("x,y\n1,2\n"), Filename: "../../etc/passwd.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if strings.Contains(res.NotePath, "..") {
		t.Errorf("path traversal survived sanitization: %q", res.NotePath)
	}
}

func TestImportFileUniqueOnCollision(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	first, _ := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "dup.csv"})
	second, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n3,4\n"), Filename: "dup.csv"})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if first.NotePath == second.NotePath {
		t.Error("a second import of the same name must not overwrite the first")
	}
	data, _ := v.ReadNote(ws, first.NotePath)
	if !strings.Contains(string(data), "| 1 | 2 |") {
		t.Error("the first note was overwritten")
	}
}

func TestImportFileRespectsDestDir(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "x.csv", DestDir: "notes/finance"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.HasPrefix(res.NotePath, "notes/finance/") {
		t.Errorf("NotePath = %q, want it under the requested folder", res.NotePath)
	}
}

func TestImportFileUnsupportedIsError(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	if _, err := v.ImportFile(ws, ImportInput{Data: []byte{0x00, 0x01, 0x02}, Filename: "x.bin"}); err == nil {
		t.Error("an unconvertible file must error rather than create a blank note")
	}
}
