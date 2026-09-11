package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// RotateUserPassword replaces an active user's password and revokes all of that
// user's existing sessions in the same transaction. It is intended for host-side
// operational tooling, not the public HTTP API.
func (s *Store) RotateUserPassword(ctx context.Context, username, password string) (string, error) {
	if username == "" {
		return "", errors.New("username is required")
	}
	if len(password) < 16 {
		return "", errors.New("password must be at least 16 characters")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var userID string
	if err := tx.QueryRow(ctx, `
		UPDATE users
		SET password_hash=$2
		WHERE username=$1 AND active=true
		RETURNING id::text`, username, string(hashed)).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("active user not found")
		}
		return "", err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE user_sessions
		SET revoked_at=now()
		WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	s.Audit(ctx, "system", "adminctl", "user.password.rotate", "user", userID, nil, "success", map[string]any{"username": username})
	return userID, nil
}
