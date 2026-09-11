package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestTrackerToDispatcherEndToEnd(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	const (
		adminUser = "integration-dispatcher"
		adminPass = "integration-password-123"
		vehicle   = "AMB-TEST"
		deviceKey = "integration-device-key-0123456789abcdef"
	)
	if err := s.BootstrapAdmin(ctx, adminUser, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	if err := s.BootstrapDemoDevice(ctx, vehicle, deviceKey); err != nil {
		t.Fatalf("bootstrap device: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(s, Config{
		PublicOrigin:     "localhost:*",
		SecureCookies:    false,
		UserSessionTTL:   time.Hour,
		DeviceSessionTTL: 15 * time.Minute,
	}, logger)
	server := httptest.NewServer(a.Routes())
	defer server.Close()

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	loginBody, _ := json.Marshal(map[string]string{"username": adminUser, "password": adminPass})
	loginResp, err := browser.Post(server.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("dispatcher login: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("dispatcher login status %d: %s", loginResp.StatusCode, body)
	}

	serverURL, _ := url.Parse(server.URL)
	var sessionCookie *http.Cookie
	for _, cookie := range jar.Cookies(serverURL) {
		if cookie.Name == "ambulance_session" {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("dispatcher session cookie missing")
	}

	deviceBody, _ := json.Marshal(map[string]string{"vehicle_code": vehicle, "device_key": deviceKey})
	deviceResp, err := http.Post(server.URL+"/api/v1/device/session", "application/json", bytes.NewReader(deviceBody))
	if err != nil {
		t.Fatalf("device auth: %v", err)
	}
	defer deviceResp.Body.Close()
	if deviceResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deviceResp.Body)
		t.Fatalf("device auth status %d: %s", deviceResp.StatusCode, body)
	}
	var deviceSession struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(deviceResp.Body).Decode(&deviceSession); err != nil || deviceSession.AccessToken == "" {
		t.Fatalf("decode device session: %v", err)
	}

	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")
	dispatchHeaders := http.Header{}
	dispatchHeaders.Set("Cookie", sessionCookie.String())
	dispatch, _, err := websocket.Dial(ctx, wsBase+"/api/v1/dispatch/ws", &websocket.DialOptions{HTTPHeader: dispatchHeaders})
	if err != nil {
		t.Fatalf("open dispatcher websocket: %v", err)
	}
	defer dispatch.Close(websocket.StatusNormalClosure, "test complete")

	trackerHeaders := http.Header{}
	trackerHeaders.Set("Authorization", "Bearer "+deviceSession.AccessToken)
	tracker, _, err := websocket.Dial(ctx, wsBase+"/api/v1/tracker/ws", &websocket.DialOptions{HTTPHeader: trackerHeaders})
	if err != nil {
		t.Fatalf("open tracker websocket: %v", err)
	}
	defer tracker.Close(websocket.StatusNormalClosure, "test complete")

	var presence struct {
		Type      string `json:"type"`
		VehicleID string `json:"vehicle_id"`
		Connected bool   `json:"connected"`
	}
	if err := wsjson.Read(ctx, dispatch, &presence); err != nil {
		t.Fatalf("read presence broadcast: %v", err)
	}
	if presence.Type != "presence" || presence.VehicleID == "" || !presence.Connected {
		t.Fatalf("unexpected presence event: %+v", presence)
	}

	fix := store.Location{
		TrackingSessionID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		SequenceNumber:    1,
		RecordedAt:        time.Now().UTC(),
		Latitude:          17.3029,
		Longitude:         -62.7178,
	}
	if err := wsjson.Write(ctx, tracker, fix); err != nil {
		t.Fatalf("write tracker fix: %v", err)
	}

	var ack struct {
		Type           string `json:"type"`
		SequenceNumber int64  `json:"sequence_number"`
		Duplicate      bool   `json:"duplicate"`
	}
	if err := wsjson.Read(ctx, tracker, &ack); err != nil {
		t.Fatalf("read tracker ack: %v", err)
	}
	if ack.Type != "ack" || ack.SequenceNumber != 1 || ack.Duplicate {
		t.Fatalf("unexpected first ack: %+v", ack)
	}

	var live struct {
		Type     string         `json:"type"`
		Location store.Location `json:"location"`
	}
	if err := wsjson.Read(ctx, dispatch, &live); err != nil {
		t.Fatalf("read dispatcher broadcast: %v", err)
	}
	if live.Type != "location" || live.Location.VehicleCode != vehicle || live.Location.SequenceNumber != 1 {
		t.Fatalf("unexpected dispatcher event: %+v", live)
	}

	fleetResp, err := browser.Get(server.URL + "/api/v1/vehicles")
	if err != nil {
		t.Fatalf("read fleet: %v", err)
	}
	defer fleetResp.Body.Close()
	if fleetResp.StatusCode != http.StatusOK {
		t.Fatalf("fleet status: %d", fleetResp.StatusCode)
	}
	var fleet struct {
		Vehicles []store.VehicleSnapshot `json:"vehicles"`
	}
	if err := json.NewDecoder(fleetResp.Body).Decode(&fleet); err != nil {
		t.Fatalf("decode fleet: %v", err)
	}
	if len(fleet.Vehicles) != 1 || fleet.Vehicles[0].Location == nil || fleet.Vehicles[0].Location.SequenceNumber != 1 {
		t.Fatalf("persisted fleet snapshot missing fix: %+v", fleet.Vehicles)
	}

	historyResp, err := browser.Get(server.URL + "/api/v1/vehicles/" + fleet.Vehicles[0].VehicleID + "/history?hours=1")
	if err != nil {
		t.Fatalf("read playback history: %v", err)
	}
	defer historyResp.Body.Close()
	if historyResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(historyResp.Body)
		t.Fatalf("history status %d: %s", historyResp.StatusCode, body)
	}
	var history struct {
		Locations []store.Location `json:"locations"`
	}
	if err := json.NewDecoder(historyResp.Body).Decode(&history); err != nil {
		t.Fatalf("decode playback history: %v", err)
	}
	if len(history.Locations) == 0 || history.Locations[len(history.Locations)-1].SequenceNumber != 1 {
		t.Fatalf("playback history missing persisted fix: %+v", history.Locations)
	}

	if err := wsjson.Write(ctx, tracker, fix); err != nil {
		t.Fatalf("write duplicate fix: %v", err)
	}
	ack = struct {
		Type           string `json:"type"`
		SequenceNumber int64  `json:"sequence_number"`
		Duplicate      bool   `json:"duplicate"`
	}{}
	if err := wsjson.Read(ctx, tracker, &ack); err != nil {
		t.Fatalf("read duplicate ack: %v", err)
	}
	if !ack.Duplicate {
		t.Fatalf("duplicate was not identified: %+v", ack)
	}
}
