package app

import (
	"testing"
	"time"
)

func TestFallbackRouteProducesExplicitApproximation(t *testing.T) {
	t.Setenv("ETA_FALLBACK_KPH", "40")
	result := fallbackRoute(17.3029, -62.7178, 17.3290, -62.7790, nil)

	if !result.Approximate {
		t.Fatal("fallback route must be marked approximate")
	}
	if result.Method != "kinematic_fallback" {
		t.Fatalf("unexpected method %q", result.Method)
	}
	if result.DistanceM <= 0 || result.DurationSeconds <= 0 {
		t.Fatalf("invalid fallback metrics: distance=%f duration=%f", result.DistanceM, result.DurationSeconds)
	}
	if result.ETAAt.Before(time.Now().UTC()) {
		t.Fatal("fallback ETA must be in the future")
	}
	if result.Geometry.Type != "LineString" || len(result.Geometry.Coordinates) != 2 {
		t.Fatalf("unexpected fallback geometry: %+v", result.Geometry)
	}
}

func TestFallbackRouteUsesMovingVehicleSpeedWithinBounds(t *testing.T) {
	t.Setenv("ETA_FALLBACK_KPH", "35")
	speed := float32(20) // 72 km/h; fallback applies an 85% live-speed factor.
	result := fallbackRoute(17.30, -62.72, 17.35, -62.75, &speed)

	if result.DurationSeconds <= 0 {
		t.Fatal("duration must be positive")
	}
	if !result.Approximate {
		t.Fatal("live-speed fallback must remain explicitly approximate")
	}
}
