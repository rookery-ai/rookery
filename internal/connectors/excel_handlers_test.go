package connectors

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestA1Range(t *testing.T) {
	for _, tc := range []struct {
		start      string
		rows, cols int
		want       string
	}{
		{"B3", 2, 3, "B3:D4"}, {"A1", 1, 1, "A1"}, {"Sheet1!Z1", 1, 2, "Z1:AA1"},
		{"$AZ$10", 3, 2, "AZ10:BA12"}, {"a1", 1, 26, "A1:Z1"},
	} {
		got, err := a1Range(tc.start, tc.rows, tc.cols)
		if err != nil || got != tc.want {
			t.Errorf("a1Range(%q,%d,%d) = %q,%v want %q", tc.start, tc.rows, tc.cols, got, err, tc.want)
		}
	}
	if _, err := a1Range("B", 1, 1); err == nil {
		t.Error("a malformed cell must fail")
	}
}

func TestExcelWriteCellsPadsAndAddresses(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"address":"Data!C2:D3","values":[["a",1],["b",""]],"formulas":[["a",1],["b",""]],"numberFormat":[["General","General"],["General","General"]]}`))
	}))
	defer srv.Close()
	old := graphDriveItems
	graphDriveItems = srv.URL + "/items/"
	defer func() { graphDriveItems = old }()
	res, err := Execute(t.Context(), testRegistry(t), fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c", Provider: "excel"}, "excel_write_cells",
		map[string]any{"item_id": "IT", "sheet": "Data", "start_cell": "C2", "values": []any{[]any{"a", 1.0}, []any{"b"}}}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "range(address='C2:D3')") {
		t.Fatalf("path = %s", gotPath)
	}
	var body struct{ Values [][]any }
	json.Unmarshal([]byte(gotBody), &body)
	if len(body.Values[1]) != 2 {
		t.Fatalf("ragged row not padded: %s", gotBody)
	}
	if strings.Contains(string(res.Data), "numberFormat") || !strings.Contains(string(res.Data), `"applied"`) {
		t.Fatalf("result = %s", res.Data)
	}
}
