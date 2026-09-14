package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func enrollmentCodeHash(code string) []byte {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	h := sha256.Sum256([]byte(normalized))
	return h[:]
}

// CreateDeviceEnrollmentCode invalidates any older unused code for the vehicle
// and creates a short-lived, one-time registration code.
func (s *Store) CreateDeviceEnrollmentCode(ctx context.Context, vehicleID, code string, expiresAt time.Time) error {
	if strings.TrimSpace(vehicleID) == "" || strings.TrimSpace(code) == "" {
		return errors.New("vehicle and enrollment code are required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE device_enrollment_codes
		SET used_at=now()
		WHERE vehicle_id=$1::uuid AND used_at IS NULL`, vehicleID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO device_enrollment_codes(id, vehicle_id, code_hash, expires_at)
		VALUES(gen_random_uuid(), $1::uuid, $2, $3)`, vehicleID, enrollmentCodeHash(code), expiresAt); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// EnrollDevice consumes a one-time enrollment code and atomically replaces the
// active tracker for that ambulance. Historical telemetry remains attached to
// the old device, but old device sessions are revoked immediately.
func (s *Store) EnrollDevice(ctx context.Context, code, deviceName, deviceKey string) (Device, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(deviceKey) == "" {
		return Device{}, errors.New("enrollment code and device key are required")
	}
	if strings.TrimSpace(deviceName) == "" {
		deviceName = "Android tracker"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback(ctx)

	var enrollmentID string
	var d Device
	if err := tx.QueryRow(ctx, `
		SELECT e.id::text, v.id::text, v.code
		FROM device_enrollment_codes e
		JOIN vehicles v ON v.id=e.vehicle_id
		WHERE e.code_hash=$1 AND e.used_at IS NULL AND e.expires_at>now()
		FOR UPDATE`, enrollmentCodeHash(code)).Scan(&enrollmentID, &d.VehicleID, &d.VehicleCode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, errors.New("invalid or expired enrollment code")
		}
		return Device{}, err
	}

	// MVP invariant: one active tracker per ambulance. Re-registering a phone or
	// replacing hardware cleanly retires the old credential instead of allowing
	// two devices to publish competing positions.
	if _, err := tx.Exec(ctx, `
		UPDATE device_sessions
		SET revoked_at=now()
		WHERE revoked_at IS NULL
		  AND device_id IN (SELECT id FROM devices WHERE vehicle_id=$1::uuid AND active=true)`, d.VehicleID); err != nil {
		return Device{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET active=false WHERE vehicle_id=$1::uuid AND active=true`, d.VehicleID); err != nil {
		return Device{}, err
	}

	keyHash := sha256.Sum256([]byte(deviceKey))
	d.Name = strings.TrimSpace(deviceName)
	if err := tx.QueryRow(ctx, `
		INSERT INTO devices(id, vehicle_id, name, key_hash)
		VALUES(gen_random_uuid(), $1::uuid, $2, $3)
		RETURNING id::text`, d.VehicleID, d.Name, keyHash[:]).Scan(&d.ID); err != nil {
		return Device{}, err
	}

	if _, err := tx.Exec(ctx, `UPDATE device_enrollment_codes SET used_at=now() WHERE id=$1::uuid`, enrollmentID); err != nil {
		return Device{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Device{}, err
	}
	return d, nil
}
