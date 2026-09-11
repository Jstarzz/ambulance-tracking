package app

import (
	"net/http"
	"strconv"
	"time"
)

func (a *App) vehicleHistory(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	vehicleID := r.PathValue("vehicleID")
	if vehicleID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle id required"})
		return
	}

	hours := 1
	if raw := r.URL.Query().Get("hours"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 24 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hours must be between 1 and 24"})
			return
		}
		hours = parsed
	}

	to := time.Now().UTC()
	from := to.Add(-time.Duration(hours) * time.Hour)
	locations, err := a.store.LocationHistory(r.Context(), vehicleID, from, 5000)
	if err != nil {
		a.log.Error("query playback", "vehicle_id", vehicleID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	a.store.Audit(
		r.Context(),
		"user",
		u.ID,
		"vehicle.history.read",
		"vehicle",
		vehicleID,
		clientIP(r),
		"success",
		map[string]any{"hours": hours, "points": len(locations)},
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"vehicle_id": vehicleID,
		"from":       from,
		"to":         to,
		"locations":  locations,
	})
}
