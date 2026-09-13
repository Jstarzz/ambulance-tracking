package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type etaCacheEntry struct {
	payload   map[string]any
	expiresAt time.Time
}

func (a *App) vehicleETA(w http.ResponseWriter, r *http.Request) {
	vehicleID := r.PathValue("vehicleID")
	lat, errLat := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	if vehicleID == "" || errLat != nil || errLon != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle id and valid lat/lon destination are required"})
		return
	}
	latest, err := a.store.LatestLocation(r.Context(), vehicleID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle has no location"})
		return
	}

	// Round destination coordinates to ~11 m for cache reuse while keeping the
	// estimate responsive to meaningful map clicks / target changes.
	cacheKey := fmt.Sprintf("%s:%.4f:%.4f:%.4f:%.4f", vehicleID, latest.Latitude, latest.Longitude, lat, lon)
	a.etaMu.Lock()
	if cached, ok := a.etaCache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		payload := cached.payload
		a.etaMu.Unlock()
		writeJSON(w, http.StatusOK, payload)
		return
	}
	a.etaMu.Unlock()

	payload, routeErr := a.routeETA(r.Context(), latest.Latitude, latest.Longitude, lat, lon)
	if routeErr != nil {
		payload = a.fallbackETA(latest.Latitude, latest.Longitude, lat, lon, latest.SpeedMPS)
	}
	payload["vehicle_id"] = vehicleID
	payload["origin"] = map[string]float64{"latitude": latest.Latitude, "longitude": latest.Longitude}
	payload["destination"] = map[string]float64{"latitude": lat, "longitude": lon}
	payload["location_recorded_at"] = latest.RecordedAt
	payload["generated_at"] = time.Now().UTC()

	a.etaMu.Lock()
	if len(a.etaCache) > 512 {
		a.etaCache = make(map[string]etaCacheEntry)
	}
	a.etaCache[cacheKey] = etaCacheEntry{payload: payload, expiresAt: time.Now().Add(15 * time.Second)}
	a.etaMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) routeETA(ctx context.Context, originLat, originLon, destLat, destLon float64) (map[string]any, error) {
	if strings.TrimSpace(a.cfg.RouterURL) == "" {
		return nil, fmt.Errorf("routing provider not configured")
	}
	base := strings.TrimRight(a.cfg.RouterURL, "/")
	u := fmt.Sprintf("%s/route/v1/driving/%.6f,%.6f;%.6f,%.6f?overview=false&steps=false&alternatives=false", base, originLon, originLat, destLon, destLat)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("router returned %d", resp.StatusCode)
	}
	var body struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&body); err != nil {
		return nil, err
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		return nil, fmt.Errorf("no route returned")
	}
	return map[string]any{
		"distance_m":       body.Routes[0].Distance,
		"duration_seconds": body.Routes[0].Duration,
		"eta_at":           time.Now().UTC().Add(time.Duration(body.Routes[0].Duration * float64(time.Second))),
		"method":           "road_route",
		"approximate":      false,
	}, nil
}

func (a *App) fallbackETA(originLat, originLon, destLat, destLon float64, speedMPS *float32) map[string]any {
	// The fallback is intentionally marked approximate. A road routing engine is
	// the authoritative path for dispatch decisions; straight-line distance is
	// expanded by a road factor to avoid pretending geometry is a route.
	distanceM := haversineMeters(originLat, originLon, destLat, destLon) * 1.25
	speedKPH := a.cfg.ETAFallbackKPH
	if speedKPH <= 0 {
		speedKPH = 35
	}
	if speedMPS != nil {
		liveKPH := float64(*speedMPS) * 3.6
		if liveKPH >= 15 {
			speedKPH = math.Max(20, math.Min(80, liveKPH*0.85))
		}
	}
	durationSeconds := distanceM / (speedKPH / 3.6)
	return map[string]any{
		"distance_m":        distanceM,
		"duration_seconds":  durationSeconds,
		"eta_at":            time.Now().UTC().Add(time.Duration(durationSeconds * float64(time.Second))),
		"method":            "kinematic_fallback",
		"approximate":       true,
		"assumed_speed_kph": speedKPH,
	}
}
