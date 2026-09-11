package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

func TestDispatcherPasswordRotationRevokesSessions(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	const (
		username    = "integration-dispatcher"
		oldPassword = "integration-password-123"
		newPassword = "rotated-integration-password-456"
		token       = "rotation-integration-session-token"
		csrf        = "rotation-integration-csrf-token"
	)

	if err := s.BootstrapAdmin(ctx, username, oldPassword); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	u, err := s.AuthenticateUser(ctx, username, oldPassword)
	if err != nil {
		t.Fatalf("authenticate original password: %v", err)
	}
	if err := s.CreateUserSession(ctx, u.ID, token, csrf, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create pre-rotation session: %v", err)
	}

	// Always restore the deterministic integration password for the other app
	// integration tests, regardless of this test's assertion outcome.
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := s.RotateUserPassword(cleanupCtx, username, oldPassword); err != nil {
			t.Errorf("restore integration password: %v", err)
		}
	}()

	if _, err := s.RotateUserPassword(ctx, username, newPassword); err != nil {
		t.Fatalf("rotate password: %v", err)
	}
	if _, err := s.AuthenticateUser(ctx, username, oldPassword); err == nil {
		t.Fatal("old password still authenticates after rotation")
	}
	if _, err := s.AuthenticateUser(ctx, username, newPassword); err != nil {
		t.Fatalf("new password does not authenticate: %v", err)
	}
	if _, err := s.UserFromSession(ctx, token); err == nil {
		t.Fatal("pre-rotation dispatcher session remained valid")
	}
}
