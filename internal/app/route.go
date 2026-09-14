package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type routeGeometry struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}

type routeResponse struct {
	VehicleID          string         `json:"vehicle_id"`
	Origin             map[string]any `json:"origin"`
	Destination        map[string]any `json:"destination"`
	DistanceM          float64        `json:"distance_m"`
	DurationSeconds    float64        `json:"duration_seconds"`
	ETAAt              time.Time      `json:"eta_at"`
	Method             string         `json:"method"`
	Approximate        bool           `json:"approximate"`
	Geometry           routeGeometry  `json:"geometry"`
	LocationRecordedAt time.Time      `json:"location_recorded_at"`
	GeneratedAt        time.Time      `json:"generated_at"`
}

// RoutingRoutes is separate from the core mux so deployments can keep the
// existing app surface stable while the optional routing provider is configured.
func (a *App) RoutingRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/vehicles/{vehicleID}/route", a.requireUser(a.vehicleRoute, false))
	return securityHeaders(requestLog(mux, a.log))
}

func (a *App) vehicleRoute(w http.ResponseWriter, r *http.Request) {
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

	result, routeErr := roadRoute(r.Context(), latest.Latitude, latest.Longitude, lat, lon)
	if routeErr != nil {
		result = fallbackRoute(latest.Latitude, latest.Longitude, lat, lon, latest.SpeedMPS)
	}
	result.VehicleID = vehicleID
	result.Origin = map[string]any{
		"latitude":   latest.Latitude,
		"longitude":  latest.Longitude,
		"accuracy_m": latest.AccuracyM,
	}
	result.Destination = map[string]any{"latitude": lat, "longitude": lon}
	result.LocationRecordedAt = latest.RecordedAt
	result.GeneratedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, result)
}

func roadRoute(ctx context.Context, originLat, originLon, destLat, destLon float64) (routeResponse, error) {
	routerURL := strings.TrimSpace(os.Getenv("ROUTER_URL"))
	if routerURL == "" {
		return routeResponse{}, fmt.Errorf("routing provider not configured")
	}
	base := strings.TrimRight(routerURL, "/")
	u := fmt.Sprintf(
		"%s/route/v1/driving/%.6f,%.6f;%.6f,%.6f?overview=full&geometries=geojson&steps=false&alternatives=false",
		base,
		originLon,
		originLat,
		destLon,
		destLat,
	)

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return routeResponse{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return routeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return routeResponse{}, fmt.Errorf("router returned %d", resp.StatusCode)
	}

	var body struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64       `json:"distance"`
			Duration float64       `json:"duration"`
			Geometry routeGeometry `json:"geometry"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return routeResponse{}, err
	}
	if body.Code != "Ok" || len(body.Routes) == 0 || len(body.Routes[0].Geometry.Coordinates) < 2 {
		return routeResponse{}, fmt.Errorf("no usable route returned")
	}

	now := time.Now().UTC()
	return routeResponse{
		DistanceM:       body.Routes[0].Distance,
		DurationSeconds: body.Routes[0].Duration,
		ETAAt:           now.Add(time.Duration(body.Routes[0].Duration * float64(time.Second))),
		Method:          "road_route",
		Approximate:     false,
		Geometry:        body.Routes[0].Geometry,
	}, nil
}

func fallbackRoute(originLat, originLon, destLat, destLon float64, speedMPS *float32) routeResponse {
	straightLine := haversineMeters(originLat, originLon, destLat, destLon)
	distanceM := straightLine * 1.25
	speedKPH := 35.0
	if raw := strings.TrimSpace(os.Getenv("ETA_FALLBACK_KPH")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 {
			speedKPH = parsed
		}
	}
	if speedMPS != nil {
		liveKPH := float64(*speedMPS) * 3.6
		if liveKPH >= 15 {
			speedKPH = math.Max(20, math.Min(80, liveKPH*0.85))
		}
	}
	durationSeconds := distanceM / (speedKPH / 3.6)
	now := time.Now().UTC()
	return routeResponse{
		DistanceM:       distanceM,
		DurationSeconds: durationSeconds,
		ETAAt:           now.Add(time.Duration(durationSeconds * float64(time.Second))),
		Method:          "kinematic_fallback",
		Approximate:     true,
		Geometry: routeGeometry{
			Type: "LineString",
			Coordinates: [][]float64{
				{originLon, originLat},
				{destLon, destLat},
			},
		},
	}
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6_371_000.0
	toRad := math.Pi / 180
	p1 := lat1 * toRad
	p2 := lat2 * toRad
	dp := (lat2 - lat1) * toRad
	dl := (lon2 - lon1) * toRad
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
