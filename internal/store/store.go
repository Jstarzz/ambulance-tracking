package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed schema.sql
var schemaSQL string

type Store struct{ pool *pgxpool.Pool }

type Device struct {
	ID          string `json:"id"`
	VehicleID   string `json:"vehicle_id"`
	VehicleCode string `json:"vehicle_code"`
	Name        string `json:"name"`
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type Location struct {
	DeviceID          string    `json:"device_id,omitempty"`
	VehicleID         string    `json:"vehicle_id,omitempty"`
	VehicleCode       string    `json:"vehicle_code,omitempty"`
	TrackingSessionID string    `json:"tracking_session_id"`
	SequenceNumber    int64     `json:"sequence_number"`
	RecordedAt        time.Time `json:"recorded_at"`
	Latitude          float64   `json:"latitude"`
	Longitude         float64   `json:"longitude"`
	AccuracyM         *float32  `json:"accuracy_m,omitempty"`
	SpeedMPS          *float32  `json:"speed_mps,omitempty"`
	BearingDeg        *float32  `json:"bearing_deg,omitempty"`
	AltitudeM         *float32  `json:"altitude_m,omitempty"`
	BatteryPct        *int16    `json:"battery_pct,omitempty"`
	NetworkType       string    `json:"network_type,omitempty"`
}

type VehicleSnapshot struct {
	VehicleID   string    `json:"vehicle_id"`
	VehicleCode string    `json:"vehicle_code"`
	Label       string    `json:"label"`
	Status      string    `json:"status"`
	Location    *Location `json:"location,omitempty"`
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                            { s.pool.Close() }
func (s *Store) Migrate(ctx context.Context) error { _, err := s.pool.Exec(ctx, schemaSQL); return err }

func hashToken(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }

func (s *Store) BootstrapAdmin(ctx context.Context, username, password string) error {
	if username == "" || password == "" {
		return errors.New("bootstrap admin credentials required")
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO users(id, username, password_hash, role) VALUES(gen_random_uuid(), $1, $2, 'admin')`, username, string(hashed))
	return err
}

func (s *Store) BootstrapDemoDevice(ctx context.Context, code, key string) error {
	if code == "" || key == "" {
		return nil
	}
	keyHash := sha256.Sum256([]byte(key))
	var vehicleID string
	err := s.pool.QueryRow(ctx, `INSERT INTO vehicles(id, code, label) VALUES(gen_random_uuid(), $1, $1)
        ON CONFLICT(code) DO UPDATE SET label=excluded.label RETURNING id`, code).Scan(&vehicleID)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO devices(id, vehicle_id, name, key_hash)
        SELECT gen_random_uuid(), $1::uuid, $2, $3 WHERE NOT EXISTS (SELECT 1 FROM devices WHERE vehicle_id=$1::uuid)`, vehicleID, code+" tracker", keyHash[:])
	return err
}

func (s *Store) AuthenticateUser(ctx context.Context, username, password string) (User, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT id::text, username, role, password_hash FROM users WHERE username=$1 AND active=true`, username).Scan(&u.ID, &u.Username, &u.Role, &hash)
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, errors.New("invalid credentials")
	}
	_, _ = s.pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1::uuid`, u.ID)
	return u, nil
}

func (s *Store) CreateUserSession(ctx context.Context, userID, token, csrf string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO user_sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES(gen_random_uuid(),$1::uuid,$2,$3,$4)`, userID, hashToken(token), hashToken(csrf), expires)
	return err
}

func (s *Store) UserFromSession(ctx context.Context, token string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `SELECT u.id::text,u.username,u.role FROM user_sessions s JOIN users u ON u.id=s.user_id
        WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.active=true`, hashToken(token)).Scan(&u.ID, &u.Username, &u.Role)
	return u, err
}

func (s *Store) ValidateCSRF(ctx context.Context, token, csrf string) bool {
	var stored []byte
	err := s.pool.QueryRow(ctx, `SELECT csrf_hash FROM user_sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, hashToken(token)).Scan(&stored)
	if err != nil {
		return false
	}
	got := hashToken(csrf)
	return len(stored) == len(got) && subtle.ConstantTimeCompare(stored, got) == 1
}

func (s *Store) RevokeUserSession(ctx context.Context, token string) {
	_, _ = s.pool.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE token_hash=$1`, hashToken(token))
}

func (s *Store) AuthenticateDevice(ctx context.Context, deviceID, key string) (Device, error) {
	var d Device
	var stored []byte
	err := s.pool.QueryRow(ctx, `SELECT d.id::text,d.vehicle_id::text,v.code,d.name,d.key_hash FROM devices d JOIN vehicles v ON v.id=d.vehicle_id WHERE d.id=$1::uuid AND d.active=true`, deviceID).Scan(&d.ID, &d.VehicleID, &d.VehicleCode, &d.Name, &stored)
	if err != nil {
		return Device{}, err
	}
	got := sha256.Sum256([]byte(key))
	if len(stored) != len(got) || subtle.ConstantTimeCompare(stored, got[:]) != 1 {
		return Device{}, errors.New("invalid device key")
	}
	return d, nil
}

func (s *Store) DeviceByVehicleCodeAndKey(ctx context.Context, code, key string) (Device, error) {
	var d Device
	var stored []byte
	err := s.pool.QueryRow(ctx, `SELECT d.id::text,d.vehicle_id::text,v.code,d.name,d.key_hash FROM devices d JOIN vehicles v ON v.id=d.vehicle_id WHERE v.code=$1 AND d.active=true`, code).Scan(&d.ID, &d.VehicleID, &d.VehicleCode, &d.Name, &stored)
	if err != nil {
		return Device{}, err
	}
	got := sha256.Sum256([]byte(key))
	if len(stored) != len(got) || subtle.ConstantTimeCompare(stored, got[:]) != 1 {
		return Device{}, errors.New("invalid device key")
	}
	return d, nil
}

func (s *Store) CreateDeviceSession(ctx context.Context, deviceID, token string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO device_sessions(id,device_id,token_hash,expires_at) VALUES(gen_random_uuid(),$1::uuid,$2,$3)`, deviceID, hashToken(token), expires)
	return err
}

func (s *Store) DeviceFromSession(ctx context.Context, token string) (Device, error) {
	var d Device
	err := s.pool.QueryRow(ctx, `SELECT d.id::text,d.vehicle_id::text,v.code,d.name FROM device_sessions s JOIN devices d ON d.id=s.device_id JOIN vehicles v ON v.id=d.vehicle_id
        WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND d.active=true`, hashToken(token)).Scan(&d.ID, &d.VehicleID, &d.VehicleCode, &d.Name)
	return d, err
}

func (s *Store) SaveLocation(ctx context.Context, d Device, l Location) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO location_events(vehicle_id,device_id,tracking_session_id,sequence_number,recorded_at,latitude,longitude,accuracy_m,speed_mps,bearing_deg,altitude_m,battery_pct,network_type)
        VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(device_id,tracking_session_id,sequence_number) DO NOTHING`, d.VehicleID, d.ID, l.TrackingSessionID, l.SequenceNumber, l.RecordedAt, l.Latitude, l.Longitude, l.AccuracyM, l.SpeedMPS, l.BearingDeg, l.AltitudeM, l.BatteryPct, l.NetworkType)
	if err != nil {
		return false, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE devices SET last_seen_at=now() WHERE id=$1::uuid`, d.ID)
	return tag.RowsAffected() == 1, nil
}

func (s *Store) VehicleSnapshots(ctx context.Context) ([]VehicleSnapshot, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.id::text,v.code,v.label,v.status,
        l.tracking_session_id::text,l.sequence_number,l.recorded_at,l.latitude,l.longitude,l.accuracy_m,l.speed_mps,l.bearing_deg,l.altitude_m,l.battery_pct,l.network_type
        FROM vehicles v LEFT JOIN LATERAL (
            SELECT tracking_session_id,sequence_number,recorded_at,latitude,longitude,accuracy_m,speed_mps,bearing_deg,altitude_m,battery_pct,network_type
            FROM location_events WHERE vehicle_id=v.id ORDER BY recorded_at DESC LIMIT 1
        ) l ON true ORDER BY v.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VehicleSnapshot
	for rows.Next() {
		var v VehicleSnapshot
		var session *string
		var seq *int64
		var recorded *time.Time
		var lat, lon *float64
		var acc, spd, bearing, alt *float32
		var battery *int16
		var network *string
		if err := rows.Scan(&v.VehicleID, &v.VehicleCode, &v.Label, &v.Status, &session, &seq, &recorded, &lat, &lon, &acc, &spd, &bearing, &alt, &battery, &network); err != nil {
			return nil, err
		}
		if session != nil && seq != nil && recorded != nil && lat != nil && lon != nil {
			v.Location = &Location{VehicleID: v.VehicleID, VehicleCode: v.VehicleCode, TrackingSessionID: *session, SequenceNumber: *seq, RecordedAt: *recorded, Latitude: *lat, Longitude: *lon, AccuracyM: acc, SpeedMPS: spd, BearingDeg: bearing, AltitudeM: alt, BatteryPct: battery}
			if network != nil {
				v.Location.NetworkType = *network
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Audit(ctx context.Context, actorType, actorID, action, resourceType, resourceID string, ip net.IP, outcome string, metadata map[string]any) {
	payload, _ := json.Marshal(metadata)
	_, _ = s.pool.Exec(ctx, `INSERT INTO audit_log(actor_type,actor_id,action,resource_type,resource_id,source_ip,outcome,metadata) VALUES($1,NULLIF($2,''),$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8::jsonb)`, actorType, actorID, action, resourceType, resourceID, ip, outcome, string(payload))
}

func IsNotFound(err error) bool                   { return errors.Is(err, pgx.ErrNoRows) }
func (s *Store) Health(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *Store) String() string                   { return fmt.Sprintf("Store(%p)", s) }
