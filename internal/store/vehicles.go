package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// CreateVehicle adds an ambulance to the fleet without requiring direct SQL.
// Code is the stable operational identifier used by dispatch and the tracker.
func (s *Store) CreateVehicle(ctx context.Context, code, label string) (VehicleSnapshot, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	label = strings.TrimSpace(label)
	if code == "" {
		return VehicleSnapshot{}, errors.New("vehicle code is required")
	}
	if label == "" {
		label = code
	}

	var vehicle VehicleSnapshot
	err := s.pool.QueryRow(ctx, `
		INSERT INTO vehicles(id, code, label)
		VALUES(gen_random_uuid(), $1, $2)
		ON CONFLICT(code) DO NOTHING
		RETURNING id::text, code, label, status`, code, label).
		Scan(&vehicle.VehicleID, &vehicle.VehicleCode, &vehicle.Label, &vehicle.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return VehicleSnapshot{}, errors.New("vehicle code already exists")
	}
	if err != nil {
		return VehicleSnapshot{}, err
	}
	return vehicle, nil
}
