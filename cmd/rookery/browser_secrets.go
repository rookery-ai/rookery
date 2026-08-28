package main

import (
	"context"
	"fmt"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/secrets"
)

// secretResolverFor builds the lookup the browser bridge uses to turn a
// ${SECRET_NAME} placeholder into a value typed straight into a page.
//
// It decrypts through the workspace's stored master password under the system
// key — the same headless path the scheduler uses, and for the same reason: a
// 03:00 run has no one to type a password. The value is returned to exactly one
// caller, which forwards it to the browser helper and remembers it only in order
// to redact it back out of every result.
//
// Two things this deliberately does NOT do. It does not fall back to any
// default when the secret is missing, so a typo cannot become a plausible-looking
// string typed into a payment field; the caller fails closed and names the
// secret. And it never returns the value in an error, because an error string is
// the one part of a tool result that routinely reaches a log file.
func secretResolverFor(database *db.DB, sysKey []byte) browser.SecretResolver {
	return func(ctx context.Context, workspaceID, name string) (string, error) {
		ws, err := database.GetWorkspaceByID(workspaceID)
		if err != nil || ws.EncryptedMasterPassword == "" {
			return "", fmt.Errorf("secrets are unavailable for this workspace")
		}
		masterPw, err := secrets.DecryptMasterPassword(ws.EncryptedMasterPassword, sysKey)
		if err != nil {
			return "", fmt.Errorf("secrets are unavailable for this workspace")
		}
		svc := secrets.New(database, workspaceID, masterPw, ws.SecretsSalt)
		v, err := svc.Get(ctx, name)
		if err != nil {
			return "", fmt.Errorf("no such secret")
		}
		return v, nil
	}
}
