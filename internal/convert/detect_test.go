package convert

import "testing"

func TestDetect(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		filename string
		mime     string
		want     Kind
	}{
		{"pdf by magic", []byte("%PDF-1.7\n..."), "", "", KindPDF},
		{"pdf magic beats wrong extension", []byte("%PDF-1.7\n..."), "report.txt", "", KindPDF},
		{"zip is unknown without ooxml part", []byte("PK\x03\x04rest"), "", "", KindUnknown},
		{"html by doctype", []byte("<!DOCTYPE html><html>"), "", "", KindHTML},
		{"html by tag", []byte("\n  <html lang=\"en\">"), "", "", KindHTML},
		{"html by mime", []byte("no markers here"), "", "text/html; charset=utf-8", KindHTML},
		{"markdown by extension", []byte("# Title"), "notes/a.md", "", KindMarkdown},
		{"csv by extension", []byte("a,b\n1,2"), "data.csv", "", KindCSV},
		{"tsv by extension", []byte("a\tb"), "data.tsv", "", KindTSV},
		{"json by mime", []byte(`{"a":1}`), "", "application/json", KindJSON},
		{"png by magic", []byte("\x89PNG\r\n\x1a\n"), "", "", KindImage},
		{"jpeg by magic", []byte("\xff\xd8\xff\xe0"), "", "", KindImage},
		{"plain text default", []byte("just some words"), "", "", KindText},
		{"empty is unknown", nil, "", "", KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.data, tc.filename, tc.mime); got != tc.want {
				t.Errorf("Detect() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectOOXMLByExtension(t *testing.T) {
	// A real OOXML file is a zip; without unzipping we fall back to the extension.
	zip := []byte("PK\x03\x04something")
	for _, tc := range []struct {
		filename string
		want     Kind
	}{
		{"a.docx", KindDOCX},
		{"a.xlsx", KindXLSX},
		{"a.pptx", KindPPTX},
	} {
		if got := Detect(zip, tc.filename, ""); got != tc.want {
			t.Errorf("Detect(%s) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}
