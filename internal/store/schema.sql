CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS vehicles (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    label text NOT NULL,
    status text NOT NULL DEFAULT 'AVAILABLE',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS devices (
    id uuid PRIMARY KEY,
    vehicle_id uuid NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    name text NOT NULL,
    key_hash bytea NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz
);

CREATE TABLE IF NOT EXISTS device_enrollment_codes (
    id uuid PRIMARY KEY,
    vehicle_id uuid NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    code_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at timestamptz
);
CREATE INDEX IF NOT EXISTS device_enrollment_codes_active_idx
    ON device_enrollment_codes(vehicle_id, expires_at)
    WHERE used_at IS NULL;

CREATE TABLE IF NOT EXISTS device_sessions (
    id uuid PRIMARY KEY,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX IF NOT EXISTS device_sessions_lookup_idx ON device_sessions(token_hash) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role text NOT NULL CHECK (role IN ('dispatcher','admin')),
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

CREATE TABLE IF NOT EXISTS user_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX IF NOT EXISTS user_sessions_lookup_idx ON user_sessions(token_hash) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS location_events (
    id bigserial PRIMARY KEY,
    vehicle_id uuid NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    tracking_session_id uuid NOT NULL,
    sequence_number bigint NOT NULL,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    accuracy_m real CHECK (accuracy_m IS NULL OR accuracy_m >= 0),
    speed_mps real CHECK (speed_mps IS NULL OR speed_mps >= 0),
    bearing_deg real CHECK (bearing_deg IS NULL OR (bearing_deg >= 0 AND bearing_deg < 360)),
    altitude_m real,
    battery_pct smallint CHECK (battery_pct IS NULL OR battery_pct BETWEEN 0 AND 100),
    network_type text,
    geom geography(Point,4326) GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(longitude, latitude),4326)::geography) STORED,
    UNIQUE(device_id, tracking_session_id, sequence_number)
);
CREATE INDEX IF NOT EXISTS location_events_vehicle_time_idx ON location_events(vehicle_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS location_events_geom_idx ON location_events USING gist(geom);

CREATE TABLE IF NOT EXISTS audit_log (
    id bigserial PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_type text NOT NULL,
    actor_id text,
    action text NOT NULL,
    resource_type text,
    resource_id text,
    source_ip inet,
    outcome text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS audit_log_time_idx ON audit_log(occurred_at DESC);
