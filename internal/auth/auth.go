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

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

var (
	ErrUserExists   = errors.New("username already taken")
	ErrInvalidCreds = errors.New("invalid username or password")
	ErrAdminExists  = errors.New("admin account already exists")
)

// BootstrapAdmin creates the first admin user. Returns ErrAdminExists if one already exists.
func BootstrapAdmin(database *db.DB, username, password string) (*db.User, error) {
	exists, err := database.AdminExists()
	if err != nil {
		return nil, fmt.Errorf("check admin: %w", err)
	}
	if exists {
		return nil, ErrAdminExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	u := &db.User{
		ID:                 uuid.New().String(),
		Username:           username,
		PasswordHash:       string(hash),
		Role:               RoleAdmin,
		NeedsSetup:         false,
		MustChangePassword: false,
	}

	if err := database.CreateUser(u); err != nil {
		return nil, fmt.Errorf("create admin: %w", err)
	}
	return u, nil
}

// CreateUser creates a new regular user with a temporary password.
// Returns the user and temp password.
func CreateUser(database *db.DB, username string) (*db.User, string, error) {
	existing, err := database.GetUserByUsername(username)
	if err == nil && existing != nil {
		return nil, "", ErrUserExists
	}

	tempPw := generateTempPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(tempPw), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash password: %w", err)
	}

	u := &db.User{
		ID:                 uuid.New().String(),
		Username:           username,
		PasswordHash:       string(hash),
		Role:               RoleUser,
		NeedsSetup:         true,
		MustChangePassword: true,
	}

	if err := database.CreateUser(u); err != nil {
		return nil, "", fmt.Errorf("create user: %w", err)
	}
	return u, tempPw, nil
}

// Authenticate validates credentials. Returns the user on success.
func Authenticate(database *db.DB, username, password string) (*db.User, error) {
	u, err := database.GetUserByUsername(username)
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrInvalidCreds
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCreds
	}
	return u, nil
}

// ChangePassword updates the user's password and clears must_change_password.
func ChangePassword(database *db.DB, userID, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return database.UpdateUserPassword(userID, string(hash))
}

// GenerateSecretsSalt returns a fresh random 16-byte hex-encoded salt.
func GenerateSecretsSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateTempPassword creates a 16-char random alphanumeric password.
func generateTempPassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}
