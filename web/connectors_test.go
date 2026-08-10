package web

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
)

func TestSaveConnectorStoresConfigForMultiField(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
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

func TestTestConnectorUsesSpecValidate(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ws := uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: ws, Name: "tester"}); err != nil {
		t.Fatal(err)
	}

	// A spec whose Validate echoes a fixed identity, no network.
	gateway.RegisterCredSpec(gateway.CredSpec{Platform: "cs-validate", Label: "CSV", Fields: []gateway.CredField{{Key: "token"}},
		Validate: func(v map[string]string) (gateway.BotIdentity, error) {
			return gateway.BotIdentity{Username: "bot-ident"}, nil
		}})
	enc, _ := gateway.EncryptToken("tok", make([]byte, 32))
	if err := d.UpsertPlatformConnection(&db.PlatformConnection{ID: uuid.New().String(), WorkspaceID: ws, Platform: "cs-validate", EncryptedToken: enc, Active: true}); err != nil {
		t.Fatal(err)
	}
	s := &Server{db: d, systemKey: make([]byte, 32)}
	id, err := s.testConnectorIdentity(ws, "cs-validate")
	if err != nil || id.Username != "bot-ident" {
		t.Fatalf("testConnectorIdentity = %+v, %v", id, err)
	}
}

func TestSlackConnectorTwoFieldSaveAndRender(t *testing.T) {
	spec, ok := gateway.CredSpecFor("slack")
	if !ok {
		t.Fatal("slack spec not registered")
	}
	tok, cfg, err := gateway.SplitCreds(spec, map[string]string{"token": "xoxb-1", "app_token": "xapp-1"})
	if err != nil || tok != "xoxb-1" || cfg != `{"app_token":"xapp-1"}` {
		t.Fatalf("slack SplitCreds wrong: tok=%q cfg=%q err=%v", tok, cfg, err)
	}
}
