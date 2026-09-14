package app

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

var vehicleCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{1,31}$`)

// FleetRoutes contains the small fleet-management surface that is shared by
// dispatch administration and registered ambulance phones.
func (a *App) FleetRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/vehicles", a.requireUser(a.createVehicle, true))
	mux.HandleFunc("GET /api/v1/device/fleet", a.deviceAuth(a.deviceFleet))
	return securityHeaders(requestLog(mux, a.log))
}

func (a *App) createVehicle(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}

	var in struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	code := strings.ToUpper(strings.TrimSpace(in.Code))
	label := strings.TrimSpace(in.Label)
	if !vehicleCodePattern.MatchString(code) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code must be 2-32 characters using letters, numbers, - or _"})
		return
	}
	if len(label) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label must be 80 characters or fewer"})
		return
	}

	vehicle, err := a.store.CreateVehicle(r.Context(), code, label)
	if err != nil {
		status := http.StatusInternalServerError
		message := "could not create ambulance"
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
			message = "an ambulance with that code already exists"
		}
		writeJSON(w, status, map[string]string{"error": message})
		return
	}

	a.store.Audit(r.Context(), "user", user.ID, "vehicle.create", "vehicle", vehicle.VehicleID, clientIP(r), "success", map[string]any{
		"code":  vehicle.VehicleCode,
		"label": vehicle.Label,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"vehicle": vehicle})
}

func (a *App) deviceFleet(w http.ResponseWriter, r *http.Request) {
	device := deviceFromContext(r.Context())
	vehicles, err := a.store.VehicleSnapshots(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vehicles":        vehicles,
		"self_vehicle_id": device.VehicleID,
		"server_time":     time.Now().UTC(),
	})
}
