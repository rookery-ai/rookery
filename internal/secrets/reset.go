package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/rookery-ai/rookery/internal/db"
)

// MinMasterPasswordLen matches the settings form's rule.
const MinMasterPasswordLen = 8

// ResetMasterPassword sets a workspace's master password WITHOUT the current
// one, for an owner who has forgotten it. It is possible only because the
// current password is stored encrypted under the system key (the scheduler
// needs it for headless runs): the old password is recovered from there, every
// secret is re-encrypted under the new one, and the stored copy is replaced.
//
// It grants nothing an operator with the data directory did not already have —
// system.key and the database sit side by side — which is why it is a CLI
// command run on the host rather than anything reachable over HTTP.
//
// Secrets are all decrypted BEFORE any is rewritten, so a secret that cannot be
// read under the recovered password aborts the reset with nothing changed.
func ResetMasterPassword(ctx context.Context, database *db.DB, ws *db.Workspace, newPw string, systemKey []byte) error {
	if len(newPw) < MinMasterPasswordLen {
		return fmt.Errorf("new master password must be at least %d characters", MinMasterPasswordLen)
	}
	if ws.SecretsSalt == "" || ws.EncryptedMasterPassword == "" {
		return errors.New("workspace setup is not complete; finish the setup wizard instead")
	}
	oldPw, err := DecryptMasterPassword(ws.EncryptedMasterPassword, systemKey)
	if err != nil {
		return fmt.Errorf("recover current master password (is this the install's own system.key?): %w", err)
	}
	names, err := database.ListSecretNames(ws.ID)
	if err != nil {
		return fmt.Errorf("list secrets: %w", err)
	}
	oldSvc := New(database, ws.ID, oldPw, ws.SecretsSalt)
	values := make(map[string]string, len(names))
	for _, name := range names {
		v, err := oldSvc.Get(ctx, name)
		if err != nil {
			return fmt.Errorf("secret %q does not decrypt under the stored password; nothing was changed: %w", name, err)
		}
		values[name] = v
	}
	newSvc := New(database, ws.ID, newPw, ws.SecretsSalt)
	for _, name := range names {
		if err := newSvc.Set(ctx, name, values[name]); err != nil {
			return fmt.Errorf("re-encrypt secret %q: %w", name, err)
		}
	}
	enc, err := EncryptMasterPassword(newPw, systemKey)
	if err != nil {
		return err
	}
	return database.UpdateWorkspaceMasterPassword(ws.ID, enc, ws.SecretsSalt)
}
