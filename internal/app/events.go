package app

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

func isUUID(value string) bool {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (a *App) trackerEvent(w http.ResponseWriter, r *http.Request) {
	d := deviceFromContext(r.Context())
	var in store.VehicleEvent
	if readJSON(r, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if !isUUID(in.ID) || !isUUID(in.TrackingSessionID) || in.RecordedAt.IsZero() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id, tracking_session_id, and recorded_at are required"})
		return
	}
	if in.EventType != "CRASH_SUSPECTED" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported event_type"})
		return
	}
	if in.Severity != "warning" && in.Severity != "critical" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "severity must be warning or critical"})
		return
	}
	if in.RecordedAt.After(time.Now().Add(5*time.Minute)) || in.RecordedAt.Before(time.Now().Add(-7*24*time.Hour)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recorded_at outside accepted window"})
		return
	}
	if in.Latitude != nil && (*in.Latitude < -90 || *in.Latitude > 90) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid latitude"})
		return
	}
	if in.Longitude != nil && (*in.Longitude < -180 || *in.Longitude > 180) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid longitude"})
		return
	}
	if len(in.Metadata) > 24 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metadata contains too many fields"})
		return
	}
	inserted, err := a.store.SaveVehicleEvent(r.Context(), d, in)
	if err != nil {
		a.log.Error("save tracker event", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "event persistence failed"})
		return
	}
	in.VehicleID = d.VehicleID
	in.VehicleCode = d.VehicleCode
	in.DeviceID = d.ID
	in.ReceivedAt = time.Now().UTC()
	if inserted {
		a.hub.broadcast(map[string]any{"type": "vehicle_event", "event": in})
	}
	a.store.Audit(r.Context(), "device", d.ID, "tracker.event", "vehicle", d.VehicleID, clientIP(r), "success", map[string]any{"event_type": in.EventType, "duplicate": !inserted})
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": !inserted, "event_id": in.ID})
}

func (a *App) recentEvents(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 168 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hours must be between 1 and 168"})
			return
		}
		hours = parsed
	}
	events, err := a.store.RecentVehicleEvents(r.Context(), time.Now().UTC().Add(-time.Duration(hours)*time.Hour), 500)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "server_time": time.Now().UTC()})
}

func (a *App) acknowledgeEvent(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	id := r.PathValue("eventID")
	if !isUUID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event id"})
		return
	}
	if err := a.store.AcknowledgeVehicleEvent(r.Context(), id, u.ID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "vehicle_event.ack", "vehicle_event", id, clientIP(r), "success", nil)
	w.WriteHeader(http.StatusNoContent)
}
