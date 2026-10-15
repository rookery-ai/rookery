package secrets_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/stretchr/testify/require"
)

func TestResetMasterPasswordReEncryptsWithoutTheOldPassword(t *testing.T) {
	ctx := context.Background()
	database := newTestDB(t)
	seedUser(t, database)
	key := bytes.Repeat([]byte{7}, 32)
	enc, err := secrets.EncryptMasterPassword("forgotten-one", key)
	require.NoError(t, err)
	require.NoError(t, database.UpdateWorkspaceMasterPassword(testUserID, enc, testSalt))
	require.NoError(t, secrets.New(database, testUserID, "forgotten-one", testSalt).Set(ctx, "API_KEY", "s3cret"))

	ws, err := database.GetWorkspaceByID(testUserID)
	require.NoError(t, err)
	require.NoError(t, secrets.ResetMasterPassword(ctx, database, ws, "brand-new-pw", key))

	got, err := secrets.New(database, testUserID, "brand-new-pw", testSalt).Get(ctx, "API_KEY")
	require.NoError(t, err)
	require.Equal(t, "s3cret", got)

	ws, _ = database.GetWorkspaceByID(testUserID)
	stored, err := secrets.DecryptMasterPassword(ws.EncryptedMasterPassword, key)
	require.NoError(t, err)
	require.Equal(t, "brand-new-pw", stored)
}

func TestResetMasterPasswordRefusesShortAndChangesNothingOnBadKey(t *testing.T) {
	ctx := context.Background()
	database := newTestDB(t)
	seedUser(t, database)
	key := bytes.Repeat([]byte{7}, 32)
	enc, _ := secrets.EncryptMasterPassword("forgotten-one", key)
	require.NoError(t, database.UpdateWorkspaceMasterPassword(testUserID, enc, testSalt))
	ws, _ := database.GetWorkspaceByID(testUserID)

	require.Error(t, secrets.ResetMasterPassword(ctx, database, ws, "short", key))
	require.Error(t, secrets.ResetMasterPassword(ctx, database, ws, "brand-new-pw", bytes.Repeat([]byte{9}, 32)))
	after, _ := database.GetWorkspaceByID(testUserID)
	require.Equal(t, ws.EncryptedMasterPassword, after.EncryptedMasterPassword)
}
