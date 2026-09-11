package app

import (
	"testing"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

func TestValidateLocation(t *testing.T) {
	valid := store.Location{
		TrackingSessionID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		SequenceNumber:    1,
		RecordedAt:        time.Now(),
		Latitude:          17.3,
		Longitude:         -62.7,
	}
	if err := validateLocation(&valid); err != nil {
		t.Fatalf("valid location rejected: %v", err)
	}
	bad := valid
	bad.Latitude = 91
	if validateLocation(&bad) == nil {
		t.Fatal("invalid latitude accepted")
	}
	old := valid
	old.RecordedAt = time.Now().Add(-8 * 24 * time.Hour)
	if validateLocation(&old) == nil {
		t.Fatal("stale location accepted")
	}
}
