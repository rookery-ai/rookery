package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrWorkspaceExists = errors.New("workspace name already taken")
	ErrInvalidCreds    = errors.New("invalid username or password")
	ErrOwnerExists     = errors.New("owner account already exists")
)

// BootstrapOwner creates the single owner account. Returns ErrOwnerExists if one
// already exists. The owner logs in and manages workspaces; it is not a tenant.
func BootstrapOwner(database *db.DB, username, password string) (*db.Owner, error) {
	exists, err := database.OwnerExists()
	if err != nil {
		return nil, fmt.Errorf("check owner: %w", err)
	}
	if exists {
		return nil, ErrOwnerExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	o := &db.Owner{
		ID:                 uuid.New().String(),
		Username:           username,
		PasswordHash:       string(hash),
		MustChangePassword: false,
	}

	if err := database.CreateOwner(o); err != nil {
		return nil, fmt.Errorf("create owner: %w", err)
	}
	return o, nil
}

// CreateWorkspace creates a new empty workspace (owner-driven). Workspaces have no
// login of their own; the owner enters them with the workspace master password set
// during the create wizard. Returns ErrWorkspaceExists if the name is taken.
func CreateWorkspace(database *db.DB, name, about string) (*db.Workspace, error) {
	existing, err := database.GetWorkspaceByName(name)
	if err == nil && existing != nil {
		return nil, ErrWorkspaceExists
	}

	w := &db.Workspace{
		ID:         uuid.New().String(),
		Name:       name,
		About:      about,
		CoderKind:  "local",
		NeedsSetup: true,
	}

	if err := database.CreateWorkspace(w); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	return w, nil
}

// Authenticate validates owner credentials. Returns the owner on success.
func Authenticate(database *db.DB, username, password string) (*db.Owner, error) {
	o, err := database.GetOwnerByUsername(username)
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrInvalidCreds
	}
	if err != nil {
		return nil, fmt.Errorf("get owner: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(o.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCreds
	}
	return o, nil
}

// ChangePassword updates the owner's password and clears must_change_password.
func ChangePassword(database *db.DB, ownerID, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return database.UpdateOwnerPassword(ownerID, string(hash))
}

// GenerateSecretsSalt returns a fresh random 16-byte hex-encoded salt.
func GenerateSecretsSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateTempPassword creates a 16-char random alphanumeric password.
func GenerateTempPassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars)))) //nolint:errcheck
		b[i] = chars[n.Int64()]
	}
	return string(b)
}
