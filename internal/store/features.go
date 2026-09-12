package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type MFAConfig struct {
	Enabled   bool
	SecretEnc []byte
}

type Enrollment struct {
	ID               string     `json:"id"`
	VehicleCode      string     `json:"vehicle_code"`
	VehicleLabel     string     `json:"vehicle_label"`
	DeviceName       string     `json:"device_name"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
	ConsumedAt       *time.Time `json:"consumed_at,omitempty"`
	ConsumedDeviceID *string    `json:"consumed_device_id,omitempty"`
}

type AdminDevice struct {
	ID          string     `json:"id"`
	VehicleID   string     `json:"vehicle_id"`
	VehicleCode string     `json:"vehicle_code"`
	VehicleLabel string    `json:"vehicle_label"`
	Name        string     `json:"name"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}

type APIToken struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type VehicleEvent struct {
	ID                string         `json:"id"`
	VehicleID         string         `json:"vehicle_id"`
	VehicleCode       string         `json:"vehicle_code,omitempty"`
	DeviceID          string         `json:"device_id"`
	TrackingSessionID string         `json:"tracking_session_id"`
	EventType         string         `json:"event_type"`
	Severity          string         `json:"severity"`
	RecordedAt        time.Time      `json:"recorded_at"`
	ReceivedAt        time.Time      `json:"received_at"`
	Latitude          *float64       `json:"latitude,omitempty"`
	Longitude         *float64       `json:"longitude,omitempty"`
	Metadata          map[string]any `json:"metadata"`
	AcknowledgedAt    *time.Time     `json:"acknowledged_at,omitempty"`
	AcknowledgedBy    *string        `json:"acknowledged_by,omitempty"`
}

func (s *Store) UserMFAConfig(ctx context.Context, userID string) (MFAConfig, error) {
	var cfg MFAConfig
	err := s.pool.QueryRow(ctx, `SELECT totp_enabled, COALESCE(totp_secret_enc, ''::bytea) FROM users WHERE id=$1::uuid AND active=true`, userID).Scan(&cfg.Enabled, &cfg.SecretEnc)
	return cfg, err
}

func (s *Store) SetUserTOTPSecret(ctx context.Context, userID string, encrypted []byte) error {
	if len(encrypted) == 0 {
		return errors.New("encrypted TOTP secret is required")
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET totp_secret_enc=$2, totp_enabled=false, totp_confirmed_at=NULL WHERE id=$1::uuid AND active=true`, userID, encrypted)
	return err
}

func (s *Store) EnableUserTOTP(ctx context.Context, userID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET totp_enabled=true, totp_confirmed_at=now() WHERE id=$1::uuid AND active=true AND totp_secret_enc IS NOT NULL`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("TOTP setup not found")
	}
	return nil
}

func (s *Store) CreateMFAChallenge(ctx context.Context, userID, token string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO mfa_login_challenges(id,user_id,token_hash,expires_at) VALUES(gen_random_uuid(),$1::uuid,$2,$3)`, userID, hashToken(token), expires)
	return err
}

func (s *Store) MFAChallenge(ctx context.Context, token string) (User, []byte, error) {
	var u User
	var secret []byte
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text,u.username,u.role,u.totp_secret_enc
		FROM mfa_login_challenges c
		JOIN users u ON u.id=c.user_id
		WHERE c.token_hash=$1 AND c.consumed_at IS NULL AND c.expires_at>now()
		  AND u.active=true AND u.totp_enabled=true AND u.totp_secret_enc IS NOT NULL`, hashToken(token)).Scan(&u.ID, &u.Username, &u.Role, &secret)
	return u, secret, err
}

func (s *Store) ConsumeMFAChallenge(ctx context.Context, token string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE mfa_login_challenges SET consumed_at=now() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now()`, hashToken(token))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("MFA challenge expired or already used")
	}
	return nil
}

func (s *Store) CreateEnrollment(ctx context.Context, createdBy, code, vehicleCode, vehicleLabel, deviceName string, expires time.Time) (Enrollment, error) {
	var e Enrollment
	err := s.pool.QueryRow(ctx, `
		INSERT INTO device_enrollments(id,code_hash,vehicle_code,vehicle_label,device_name,expires_at,created_by)
		VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6::uuid)
		RETURNING id::text,vehicle_code,vehicle_label,device_name,expires_at,created_at`, hashToken(code), vehicleCode, vehicleLabel, deviceName, expires, createdBy).Scan(
		&e.ID, &e.VehicleCode, &e.VehicleLabel, &e.DeviceName, &e.ExpiresAt, &e.CreatedAt,
	)
	return e, err
}

func (s *Store) ListEnrollments(ctx context.Context, limit int) ([]Enrollment, error) {
	limit = max(1, min(limit, 100))
	rows, err := s.pool.Query(ctx, `
		SELECT id::text,vehicle_code,vehicle_label,device_name,expires_at,created_at,consumed_at,consumed_device_id::text
		FROM device_enrollments
		WHERE created_at>now()-interval '7 days'
		ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Enrollment
	for rows.Next() {
		var e Enrollment
		if err := rows.Scan(&e.ID, &e.VehicleCode, &e.VehicleLabel, &e.DeviceName, &e.ExpiresAt, &e.CreatedAt, &e.ConsumedAt, &e.ConsumedDeviceID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ConsumeEnrollment(ctx context.Context, code, deviceKey string) (Device, error) {
	if code == "" || deviceKey == "" {
		return Device{}, errors.New("enrollment code and device key are required")
	}
	keyHash := sha256.Sum256([]byte(deviceKey))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback(ctx)

	var enrollmentID, vehicleCode, vehicleLabel, deviceName string
	err = tx.QueryRow(ctx, `
		SELECT id::text,vehicle_code,vehicle_label,device_name
		FROM device_enrollments
		WHERE code_hash=$1 AND consumed_at IS NULL AND expires_at>now()
		FOR UPDATE`, hashToken(code)).Scan(&enrollmentID, &vehicleCode, &vehicleLabel, &deviceName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, errors.New("invalid or expired enrollment code")
		}
		return Device{}, err
	}

	var vehicleID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO vehicles(id,code,label) VALUES(gen_random_uuid(),$1,$2)
		ON CONFLICT(code) DO UPDATE SET label=excluded.label,updated_at=now()
		RETURNING id::text`, vehicleCode, vehicleLabel).Scan(&vehicleID); err != nil {
		return Device{}, err
	}

	var activeCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM devices WHERE vehicle_id=$1::uuid AND active=true`, vehicleID).Scan(&activeCount); err != nil {
		return Device{}, err
	}
	if activeCount > 0 {
		return Device{}, errors.New("vehicle already has an active tracker")
	}

	var d Device
	if err := tx.QueryRow(ctx, `
		INSERT INTO devices(id,vehicle_id,name,key_hash)
		VALUES(gen_random_uuid(),$1::uuid,$2,$3)
		RETURNING id::text,vehicle_id::text,$4,name`, vehicleID, deviceName, keyHash[:], vehicleCode).Scan(&d.ID, &d.VehicleID, &d.VehicleCode, &d.Name); err != nil {
		return Device{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE device_enrollments SET consumed_at=now(),consumed_device_id=$2::uuid WHERE id=$1::uuid`, enrollmentID, d.ID); err != nil {
		return Device{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, err
	}
	return d, nil
}

func (s *Store) ListDevices(ctx context.Context) ([]AdminDevice, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id::text,d.vehicle_id::text,v.code,v.label,d.name,d.active,d.created_at,d.last_seen_at
		FROM devices d JOIN vehicles v ON v.id=d.vehicle_id ORDER BY v.code,d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminDevice
	for rows.Next() {
		var d AdminDevice
		if err := rows.Scan(&d.ID, &d.VehicleID, &d.VehicleCode, &d.VehicleLabel, &d.Name, &d.Active, &d.CreatedAt, &d.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RevokeDevice(ctx context.Context, deviceID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE devices SET active=false WHERE id=$1::uuid AND active=true`, deviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("active device not found")
	}
	if _, err := tx.Exec(ctx, `UPDATE device_sessions SET revoked_at=now() WHERE device_id=$1::uuid AND revoked_at IS NULL`, deviceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateAPIToken(ctx context.Context, createdBy, name, rawToken string, scopes []string, expiresAt *time.Time) (APIToken, error) {
	var t APIToken
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens(id,name,token_hash,scopes,created_by,expires_at)
		VALUES(gen_random_uuid(),$1,$2,$3,$4::uuid,$5)
		RETURNING id::text,name,scopes,expires_at,created_at`, name, hashToken(rawToken), scopes, createdBy, expiresAt).Scan(&t.ID, &t.Name, &t.Scopes, &t.ExpiresAt, &t.CreatedAt)
	return t, err
}

func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,name,scopes,expires_at,created_at,last_used_at FROM api_tokens WHERE revoked_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Scopes, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) APITokenFromBearer(ctx context.Context, rawToken string) (APIToken, error) {
	var t APIToken
	err := s.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at=now()
		WHERE token_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())
		RETURNING id::text,name,scopes,expires_at,created_at,last_used_at`, hashToken(rawToken)).Scan(&t.ID, &t.Name, &t.Scopes, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt)
	return t, err
}

func (s *Store) RevokeAPIToken(ctx context.Context, tokenID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_tokens SET revoked_at=now() WHERE id=$1::uuid AND revoked_at IS NULL`, tokenID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("API token not found")
	}
	return nil
}

func (s *Store) SaveVehicleEvent(ctx context.Context, d Device, e VehicleEvent) (bool, error) {
	metadata, err := json.Marshal(e.Metadata)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO vehicle_events(id,vehicle_id,device_id,tracking_session_id,event_type,severity,recorded_at,latitude,longitude,metadata)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10::jsonb)
		ON CONFLICT(id) DO NOTHING`, e.ID, d.VehicleID, d.ID, e.TrackingSessionID, e.EventType, e.Severity, e.RecordedAt, e.Latitude, e.Longitude, string(metadata))
	return tag.RowsAffected() == 1, err
}

func (s *Store) RecentVehicleEvents(ctx context.Context, since time.Time, limit int) ([]VehicleEvent, error) {
	limit = max(1, min(limit, 500))
	rows, err := s.pool.Query(ctx, `
		SELECT e.id::text,e.vehicle_id::text,v.code,e.device_id::text,e.tracking_session_id::text,e.event_type,e.severity,e.recorded_at,e.received_at,e.latitude,e.longitude,e.metadata,e.acknowledged_at,e.acknowledged_by::text
		FROM vehicle_events e JOIN vehicles v ON v.id=e.vehicle_id
		WHERE e.recorded_at >= $1 ORDER BY e.recorded_at DESC LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VehicleEvent
	for rows.Next() {
		var e VehicleEvent
		var raw []byte
		if err := rows.Scan(&e.ID, &e.VehicleID, &e.VehicleCode, &e.DeviceID, &e.TrackingSessionID, &e.EventType, &e.Severity, &e.RecordedAt, &e.ReceivedAt, &e.Latitude, &e.Longitude, &raw, &e.AcknowledgedAt, &e.AcknowledgedBy); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &e.Metadata)
		if e.Metadata == nil {
			e.Metadata = map[string]any{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AcknowledgeVehicleEvent(ctx context.Context, eventID, userID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE vehicle_events SET acknowledged_at=COALESCE(acknowledged_at,now()),acknowledged_by=COALESCE(acknowledged_by,$2::uuid) WHERE id=$1::uuid`, eventID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("event not found")
	}
	return nil
}

func (s *Store) LatestLocation(ctx context.Context, vehicleID string) (Location, error) {
	var l Location
	err := s.pool.QueryRow(ctx, `
		SELECT device_id::text,vehicle_id::text,tracking_session_id::text,sequence_number,recorded_at,latitude,longitude,accuracy_m,speed_mps,bearing_deg,altitude_m,battery_pct,network_type
		FROM location_events WHERE vehicle_id=$1::uuid ORDER BY recorded_at DESC LIMIT 1`, vehicleID).Scan(
		&l.DeviceID, &l.VehicleID, &l.TrackingSessionID, &l.SequenceNumber, &l.RecordedAt, &l.Latitude, &l.Longitude, &l.AccuracyM, &l.SpeedMPS, &l.BearingDeg, &l.AltitudeM, &l.BatteryPct, &l.NetworkType,
	)
	return l, err
}
