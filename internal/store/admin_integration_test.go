package store

import (
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestRotateUserPasswordRevokesSessions(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	const (
		username    = "rotate-test-user"
		oldPassword = "old-password-for-test-123"
		newPassword = "new-password-for-test-456"
		token       = "rotate-test-session-token"
		csrf        = "rotate-test-csrf-token"
	)

	oldHash, err := bcrypt.GenerateFromPassword([]byte(oldPassword), 12)
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	var userID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO users(id, username, password_hash, role)
		VALUES(gen_random_uuid(), $1, $2, 'dispatcher')
		ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash, active=true
		RETURNING id::text`, username, string(oldHash)).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := s.CreateUserSession(ctx, userID, token, csrf, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := s.UserFromSession(ctx, token); err != nil {
		t.Fatalf("seeded session invalid before rotation: %v", err)
	}

	rotatedID, err := s.RotateUserPassword(ctx, username, newPassword)
	if err != nil {
		t.Fatalf("rotate password: %v", err)
	}
	if rotatedID != userID {
		t.Fatalf("rotated unexpected user: got %s want %s", rotatedID, userID)
	}

	if _, err := s.AuthenticateUser(ctx, username, oldPassword); err == nil {
		t.Fatal("old password still authenticates after rotation")
	}
	if _, err := s.AuthenticateUser(ctx, username, newPassword); err != nil {
		t.Fatalf("new password does not authenticate: %v", err)
	}
	if _, err := s.UserFromSession(ctx, token); err == nil {
		t.Fatal("pre-rotation session remained valid")
	}
}
