package store

import (
	"context"
	"time"
)

// LocationHistory returns chronological telemetry for dispatcher playback. Large
// windows are deterministically down-sampled in PostgreSQL so the browser never
// has to ingest an unbounded 1 Hz trace.
func (s *Store) LocationHistory(ctx context.Context, vehicleID string, since time.Time, maxPoints int) ([]Location, error) {
	if maxPoints < 2 {
		maxPoints = 2
	}
	if maxPoints > 5000 {
		maxPoints = 5000
	}

	rows, err := s.pool.Query(ctx, `
		WITH filtered AS (
			SELECT
				l.device_id::text AS device_id,
				l.vehicle_id::text AS vehicle_id,
				v.code AS vehicle_code,
				l.tracking_session_id::text AS tracking_session_id,
				l.sequence_number,
				l.recorded_at,
				l.latitude,
				l.longitude,
				l.accuracy_m,
				l.speed_mps,
				l.bearing_deg,
				l.altitude_m,
				l.battery_pct,
				l.network_type,
				row_number() OVER (ORDER BY l.recorded_at) AS rn,
				count(*) OVER () AS total
			FROM location_events l
			JOIN vehicles v ON v.id = l.vehicle_id
			WHERE l.vehicle_id::text = $1 AND l.recorded_at >= $2
		), sampled AS (
			SELECT *
			FROM filtered
			WHERE total <= $3
				OR rn = 1
				OR rn = total
				OR mod(rn - 1, GREATEST(1, CEIL(total::numeric / $3)::bigint)) = 0
		)
		SELECT device_id, vehicle_id, vehicle_code, tracking_session_id,
			sequence_number, recorded_at, latitude, longitude, accuracy_m,
			speed_mps, bearing_deg, altitude_m, battery_pct, network_type
		FROM sampled
		ORDER BY recorded_at ASC`, vehicleID, since, maxPoints)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	locations := make([]Location, 0)
	for rows.Next() {
		var l Location
		if err := rows.Scan(
			&l.DeviceID,
			&l.VehicleID,
			&l.VehicleCode,
			&l.TrackingSessionID,
			&l.SequenceNumber,
			&l.RecordedAt,
			&l.Latitude,
			&l.Longitude,
			&l.AccuracyM,
			&l.SpeedMPS,
			&l.BearingDeg,
			&l.AltitudeM,
			&l.BatteryPct,
			&l.NetworkType,
		); err != nil {
			return nil, err
		}
		locations = append(locations, l)
	}
	return locations, rows.Err()
}
