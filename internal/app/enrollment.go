package app

import (
	"crypto/rand"
	"net/http"
	"strings"
	"time"
)

const enrollmentAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomEnrollmentCode(length int) (string, error) {
	out := make([]byte, length)
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		out[i] = enrollmentAlphabet[int(b)%len(enrollmentAlphabet)]
	}
	return string(out), nil
}

// EnrollmentRoutes is mounted beside the main application router so device
// registration can evolve independently from the tracker transport endpoints.
func (a *App) EnrollmentRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/device/enroll", a.deviceEnroll)
	mux.HandleFunc("POST /api/v1/vehicles/{vehicleID}/enrollment", a.requireUser(a.createEnrollmentCode, true))
	return securityHeaders(requestLog(mux, a.log))
}

func (a *App) createEnrollmentCode(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	vehicleID := strings.TrimSpace(r.PathValue("vehicleID"))
	if vehicleID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle is required"})
		return
	}

	code, err := randomEnrollmentCode(8)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create registration code"})
		return
	}
	expiresAt := time.Now().Add(10 * time.Minute)
	if err := a.store.CreateDeviceEnrollmentCode(r.Context(), vehicleID, code, expiresAt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not create registration code"})
		return
	}

	a.store.Audit(r.Context(), "user", user.ID, "device.enrollment.create", "vehicle", vehicleID, clientIP(r), "success", map[string]any{"expires_at": expiresAt})
	writeJSON(w, http.StatusCreated, map[string]any{
		"vehicle_id": vehicleID,
		"code":       code,
		"expires_at": expiresAt,
	})
}

func (a *App) deviceEnroll(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.authLimiter.allow("enroll:"+ip.String(), 12, time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many registration attempts"})
		return
	}

	var in struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
	}
	if readJSON(r, &in) != nil || strings.TrimSpace(in.Code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "registration code is required"})
		return
	}

	deviceKey, err := randomToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "device registration failed"})
		return
	}
	device, err := a.store.EnrollDevice(r.Context(), in.Code, in.DeviceName, deviceKey)
	if err != nil {
		a.store.Audit(r.Context(), "device", "", "device.enrollment.consume", "device", "", ip, "denied", nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired registration code"})
		return
	}

	a.store.Audit(r.Context(), "device", device.ID, "device.enrollment.consume", "vehicle", device.VehicleID, ip, "success", map[string]any{"device_name": device.Name})
	writeJSON(w, http.StatusCreated, map[string]any{
		"vehicle_code": device.VehicleCode,
		"device_key":   deviceKey,
		"device":       device,
	})
}
