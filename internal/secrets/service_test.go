package secrets_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/stretchr/testify/require"
)

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	migrationsDir := findMigrations(t)
	database, err := db.Open(dbPath, migrationsDir)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return database
}

func findMigrations(t *testing.T) string {
	t.Helper()
	// Walk up from test file to find migrations/ directory.
	dir, _ := os.Getwd()
	for {
		candidate := filepath.Join(dir, "migrations")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("migrations directory not found")
	return ""
}

const (
	testUserID = "user-test-001"
	testSalt   = "aabbccddeeff00112233445566778899" // 32 hex chars = 16 bytes
)

func seedUser(t *testing.T, database *db.DB) {
	t.Helper()
	err := database.CreateWorkspace(&db.Workspace{
		ID:          testUserID,
		Name:        "testuser",
		SecretsSalt: testSalt,
	})
	require.NoError(t, err)
}

func TestSetAndGet(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "masterpassword1", testSalt)
	ctx := context.Background()

	require.NoError(t, svc.Set(ctx, "API_KEY", "supersecret"))

	got, err := svc.Get(ctx, "API_KEY")
	require.NoError(t, err)
	require.Equal(t, "supersecret", got)
}

func TestGetNotFound(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "masterpassword1", testSalt)
	ctx := context.Background()

	_, err := svc.Get(ctx, "NONEXISTENT")
	require.ErrorIs(t, err, secrets.ErrNotFound)
}

func TestWrongMasterPassword(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "correct-password", testSalt)
	ctx := context.Background()
	require.NoError(t, svc.Set(ctx, "TOKEN", "abc123"))

	wrongSvc := secrets.New(database, testUserID, "wrong-password", testSalt)
	_, err := wrongSvc.Get(ctx, "TOKEN")
	require.ErrorIs(t, err, secrets.ErrWrongPassword)
}

func TestList(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "pw", testSalt)
	ctx := context.Background()

	require.NoError(t, svc.Set(ctx, "KEY_A", "v1"))
	require.NoError(t, svc.Set(ctx, "KEY_B", "v2"))

	names, err := svc.List(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"KEY_A", "KEY_B"}, names)
}

func TestDelete(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "pw", testSalt)
	ctx := context.Background()

	require.NoError(t, svc.Set(ctx, "TO_DELETE", "val"))
	require.NoError(t, svc.Delete(ctx, "TO_DELETE"))

	_, err := svc.Get(ctx, "TO_DELETE")
	require.ErrorIs(t, err, secrets.ErrNotFound)
}

func TestUpsert(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "pw", testSalt)
	ctx := context.Background()

	require.NoError(t, svc.Set(ctx, "K", "first"))
	require.NoError(t, svc.Set(ctx, "K", "second")) // overwrite

	got, err := svc.Get(ctx, "K")
	require.NoError(t, err)
	require.Equal(t, "second", got)

	names, err := svc.List(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, len(names), "upsert must not duplicate")
}

func TestProxy(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "pw", testSalt)
	ctx := context.Background()

	require.NoError(t, svc.Set(ctx, "DB_PASS", "hunter2"))
	require.NoError(t, svc.Set(ctx, "API_KEY", "sk-xyz"))

	code := `print("${DB_PASS} ${API_KEY} ${MISSING}")`
	result, err := svc.Proxy(ctx, code)
	require.NoError(t, err)
	require.Equal(t, `print("hunter2 sk-xyz ${MISSING}")`, result)
}

func TestProxyNoPlaceholders(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database)

	svc := secrets.New(database, testUserID, "pw", testSalt)
	ctx := context.Background()

	code := `print("hello world")`
	result, err := svc.Proxy(ctx, code)
	require.NoError(t, err)
	require.Equal(t, code, result)
}

func TestEncryptDecryptMasterPassword(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	enc, err := secrets.EncryptMasterPassword("my-master-pw", key)
	require.NoError(t, err)
	require.NotEmpty(t, enc)

	dec, err := secrets.DecryptMasterPassword(enc, key)
	require.NoError(t, err)
	require.Equal(t, "my-master-pw", dec)
}

func TestEncryptMasterPasswordWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = 0xFF
	}

	enc, err := secrets.EncryptMasterPassword("pw", key1)
	require.NoError(t, err)

	_, err = secrets.DecryptMasterPassword(enc, key2)
	require.Error(t, err)
}

func TestEncryptMasterPasswordBadKeySize(t *testing.T) {
	_, err := secrets.EncryptMasterPassword("pw", []byte("tooshort"))
	require.Error(t, err)
}
