package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Deel wraps every write body in {"data": {…}} and versions by header; both are easy
// to drop in a YAML edit and invisible until a live call 400s.
func TestDeelWriteWrapsDataAndPinsVersion(t *testing.T) {
	var body map[string]map[string]any
	var version, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version, auth = r.Header.Get("X-Version"), r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.Write([]byte(`{"data":{"created":true,"id":"adj1","status":"pending"}}`))
	}))
	defer srv.Close()
	reg := testRegistry(t)
	a, ok := reg.Action("deel", "deel_create_invoice_adjustment")
	if !ok || !a.PublicWrite || !a.Mutating {
		t.Fatal("deel_create_invoice_adjustment must exist and be public_write — it changes someone's pay")
	}
	a.Request.URL = srv.URL
	reg.actions["deel"] = []Action{a}
	res, err := Execute(context.Background(), reg, fakeStore{tok: "TOK"}, srv.Client(),
		ConnRef{ID: "c", Provider: "deel"}, "deel_create_invoice_adjustment",
		map[string]any{"contract_id": "37nex2x", "type": "bonus", "amount": 500.0, "description": "Q3", "date_submitted": "2026-10-01"}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if version != "2026-01-01" || auth != "Bearer TOK" {
		t.Fatalf("headers: X-Version=%q Authorization=%q", version, auth)
	}
	if body["data"]["amount"] != 500.0 || body["data"]["type"] != "bonus" {
		t.Fatalf("body = %v", body)
	}
	if string(res.Data) != `{"created":true,"id":"adj1","status":"pending"}` {
		t.Fatalf("extract = %s", res.Data)
	}
}
