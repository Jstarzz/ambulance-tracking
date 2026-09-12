package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

type apiTokenContextKey struct{}

func hasScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func (a *App) requireAPIScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := bearer(r)
		if raw == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bearer token required"})
			return
		}
		token, err := a.store.APITokenFromBearer(r.Context(), raw)
		if err != nil || !hasScope(token.Scopes, scope) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "token does not have required scope"})
			return
		}
		ctx := context.WithValue(r.Context(), apiTokenContextKey{}, token)
		next(w, r.WithContext(ctx))
	}
}

func telemetryFreshness(recordedAt time.Time, now time.Time) string {
	age := now.Sub(recordedAt)
	switch {
	case age < 5*time.Second:
		return "LIVE"
	case age < 30*time.Second:
		return "DELAYED"
	case age < 2*time.Minute:
		return "STALE"
	default:
		return "OFFLINE"
	}
}

func (a *App) aiFleetContext(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	vehicles, err := a.store.VehicleSnapshots(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	events, err := a.store.RecentVehicleEvents(r.Context(), now.Add(-time.Hour), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	counts := map[string]int{"total": len(vehicles), "live": 0, "delayed": 0, "stale": 0, "offline": 0, "no_data": 0}
	fleet := make([]map[string]any, 0, len(vehicles))
	for _, vehicle := range vehicles {
		state := "NO_DATA"
		if vehicle.Location != nil {
			state = telemetryFreshness(vehicle.Location.RecordedAt, now)
		}
		switch state {
		case "LIVE":
			counts["live"]++
		case "DELAYED":
			counts["delayed"]++
		case "STALE":
			counts["stale"]++
		case "OFFLINE":
			counts["offline"]++
		default:
			counts["no_data"]++
		}
		fleet = append(fleet, map[string]any{
			"vehicle_id": vehicle.VehicleID,
			"vehicle_code": vehicle.VehicleCode,
			"label": vehicle.Label,
			"status": vehicle.Status,
			"freshness": state,
			"location": vehicle.Location,
		})
	}

	unacked := make([]store.VehicleEvent, 0)
	for _, event := range events {
		if event.AcknowledgedAt == nil {
			unacked = append(unacked, event)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "1.0",
		"generated_at": now,
		"purpose": "read-only operational context for an AI assistant or external automation",
		"patient_data_included": false,
		"fleet_counts": counts,
		"vehicles": fleet,
		"unacknowledged_events": unacked,
	})
}

func (a *App) aiVehicleContext(w http.ResponseWriter, r *http.Request) {
	vehicleID := r.PathValue("vehicleID")
	if vehicleID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle id required"})
		return
	}
	minutes := 60
	if raw := r.URL.Query().Get("minutes"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 360 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "minutes must be between 1 and 360"})
			return
		}
		minutes = v
	}
	from := time.Now().UTC().Add(-time.Duration(minutes) * time.Minute)
	locations, err := a.store.LocationHistory(r.Context(), vehicleID, from, 600)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	var distanceM float64
	var maxSpeedMPS float64
	var speedSum float64
	var speedCount int
	var offlineSamples int
	for i, sample := range locations {
		if i > 0 {
			prev := locations[i-1]
			distanceM += haversineMeters(prev.Latitude, prev.Longitude, sample.Latitude, sample.Longitude)
		}
		if sample.SpeedMPS != nil {
			s := float64(*sample.SpeedMPS)
			if s > maxSpeedMPS {
				maxSpeedMPS = s
			}
			speedSum += s
			speedCount++
		}
		if sample.NetworkType == "NONE" {
			offlineSamples++
		}
	}
	var avgSpeedMPS float64
	if speedCount > 0 {
		avgSpeedMPS = speedSum / float64(speedCount)
	}
	events, _ := a.store.RecentVehicleEvents(r.Context(), from, 100)
	vehicleEvents := make([]store.VehicleEvent, 0)
	for _, event := range events {
		if event.VehicleID == vehicleID {
			vehicleEvents = append(vehicleEvents, event)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "1.0",
		"vehicle_id": vehicleID,
		"window_minutes": minutes,
		"sample_count": len(locations),
		"summary": map[string]any{
			"distance_m": distanceM,
			"average_speed_kph": avgSpeedMPS * 3.6,
			"max_speed_kph": maxSpeedMPS * 3.6,
			"samples_recorded_without_network": offlineSamples,
		},
		"locations": locations,
		"events": vehicleEvents,
		"patient_data_included": false,
	})
}
