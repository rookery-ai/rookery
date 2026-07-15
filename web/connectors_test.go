package web

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
)

func TestSaveConnectorStoresConfigForMultiField(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ws := uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: ws, Name: "tester"}); err != nil {
		t.Fatal(err)
	}

	gateway.RegisterCredSpec(gateway.CredSpec{Platform: "cs-multi", Fields: []gateway.CredField{
		{Key: "token"}, {Key: "server_url"},
	}}) // no Validate → no network probe
	s := &Server{db: d, systemKey: make([]byte, 32)}
	_, _, err = s.saveConnector(ws, "cs-multi", map[string]string{"token": "t", "server_url": "https://mm.example"})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := d.GetPlatformConnection(ws, "cs-multi")
	if err != nil {
		t.Fatal(err)
	}
	if conn.EncryptedConfig == "" {
		t.Fatal("expected encrypted_config to be populated")
	}
}
