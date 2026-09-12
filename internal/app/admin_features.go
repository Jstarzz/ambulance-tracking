package app

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var vehicleCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{1,23}$`)

func (a *App) requireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromContext(r.Context())
		if u.ID == "" || u.Role != role {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next(w, r)
	}
}

func randomEnrollmentCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 8)
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf[:4]) + "-" + string(buf[4:]), nil
}

func canonicalEnrollmentCode(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, " ", "")
	if len(v) == 8 && !strings.Contains(v, "-") {
		v = v[:4] + "-" + v[4:]
	}
	return v
}

func (a *App) createEnrollment(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	var in struct {
		VehicleCode    string `json:"vehicle_code"`
		VehicleLabel   string `json:"vehicle_label"`
		DeviceName     string `json:"device_name"`
		ExpiresMinutes int    `json:"expires_minutes"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	in.VehicleCode = strings.ToUpper(strings.TrimSpace(in.VehicleCode))
	in.VehicleLabel = strings.TrimSpace(in.VehicleLabel)
	in.DeviceName = strings.TrimSpace(in.DeviceName)
	if !vehicleCodePattern.MatchString(in.VehicleCode) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_code must be 2-24 uppercase letters, digits, or hyphens"})
		return
	}
	if in.VehicleLabel == "" {
		in.VehicleLabel = in.VehicleCode
	}
	if len(in.VehicleLabel) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_label is too long"})
		return
	}
	if in.DeviceName == "" {
		in.DeviceName = in.VehicleCode + " tracker"
	}
	if len(in.DeviceName) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_name is too long"})
		return
	}
	if in.ExpiresMinutes == 0 {
		in.ExpiresMinutes = 15
	}
	if in.ExpiresMinutes < 5 || in.ExpiresMinutes > 1440 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_minutes must be between 5 and 1440"})
		return
	}
	code, err := randomEnrollmentCode()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create enrollment"})
		return
	}
	expires := time.Now().Add(time.Duration(in.ExpiresMinutes) * time.Minute)
	e, err := a.store.CreateEnrollment(r.Context(), u.ID, code, in.VehicleCode, in.VehicleLabel, in.DeviceName, expires)
	if err != nil {
		a.log.Error("create enrollment", "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not create enrollment"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "device.enrollment.create", "vehicle", in.VehicleCode, clientIP(r), "success", map[string]any{"expires_at": expires})
	writeJSON(w, http.StatusCreated, map[string]any{"enrollment": e, "enrollment_code": code})
}

func (a *App) listEnrollments(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListEnrollments(r.Context(), 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollments": rows})
}

func (a *App) deviceEnroll(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.authLimiter.allow("enroll:"+ip.String(), 12, time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many enrollment attempts"})
		return
	}
	var in struct {
		EnrollmentCode string `json:"enrollment_code"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	code := canonicalEnrollmentCode(in.EnrollmentCode)
	if len(code) != 9 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired enrollment code"})
		return
	}
	deviceKey, err := randomToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "enrollment failed"})
		return
	}
	d, err := a.store.ConsumeEnrollment(r.Context(), code, deviceKey)
	if err != nil {
		a.log.Warn("device enrollment denied", "error", err)
		a.store.Audit(r.Context(), "device", "", "device.enroll", "device", "", ip, "denied", nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired enrollment code"})
		return
	}
	a.store.Audit(r.Context(), "device", d.ID, "device.enroll", "vehicle", d.VehicleID, ip, "success", map[string]any{"vehicle_code": d.VehicleCode})
	writeJSON(w, http.StatusCreated, map[string]any{
		"device":      d,
		"device_key":  deviceKey,
		"server_time": time.Now().UTC(),
	})
}

func (a *App) listDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := a.store.ListDevices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (a *App) revokeDevice(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	deviceID := r.PathValue("deviceID")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device id required"})
		return
	}
	if err := a.store.RevokeDevice(r.Context(), deviceID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "active device not found"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "device.revoke", "device", deviceID, clientIP(r), "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func normalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{"fleet:read"}, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		switch scope {
		case "fleet:read", "events:read":
		default:
			return nil, fmt.Errorf("unsupported scope %q", scope)
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	return out, nil
}

func (a *App) createAPIToken(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	var in struct {
		Name        string   `json:"name"`
		Scopes      []string `json:"scopes"`
		ExpiresDays int      `json:"expires_days"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required and must be <=80 characters"})
		return
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var expires *time.Time
	if in.ExpiresDays != 0 {
		if in.ExpiresDays < 1 || in.ExpiresDays > 365 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_days must be between 1 and 365"})
			return
		}
		t := time.Now().Add(time.Duration(in.ExpiresDays) * 24 * time.Hour)
		expires = &t
	}
	random, err := randomToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token creation failed"})
		return
	}
	raw := "ait_" + random
	token, err := a.store.CreateAPIToken(r.Context(), u.ID, in.Name, raw, scopes, expires)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token creation failed"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "api_token.create", "api_token", token.ID, clientIP(r), "success", map[string]any{"scopes": scopes})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "access_token": raw})
}

func (a *App) listAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := a.store.ListAPITokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (a *App) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	id := r.PathValue("tokenID")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token id required"})
		return
	}
	if err := a.store.RevokeAPIToken(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "api_token.revoke", "api_token", id, clientIP(r), "success", nil)
	w.WriteHeader(http.StatusNoContent)
}
