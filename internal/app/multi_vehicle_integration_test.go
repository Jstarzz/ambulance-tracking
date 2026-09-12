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

func TestMultipleTrackersAndPlaybackIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	// BootstrapAdmin intentionally creates only the first user in a deployment.
	// Reuse the same deterministic bootstrap credentials as integration_test.go so
	// these tests are independent of execution order while sharing one test DB.
	const (
		adminUser = "integration-dispatcher"
		adminPass = "integration-password-123"
		vehicleA  = "AMB-MULTI-A"
		vehicleB  = "AMB-MULTI-B"
		deviceA   = "multi-device-a-key-0123456789abcdef"
		deviceB   = "multi-device-b-key-0123456789abcdef"
		oldA      = "11111111-1111-4111-8111-111111111111"
		newA      = "22222222-2222-4222-8222-222222222222"
		sessionB  = "33333333-3333-4333-8333-333333333333"
	)

	if err := s.BootstrapAdmin(ctx, adminUser, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	if err := s.BootstrapDemoDevice(ctx, vehicleA, deviceA); err != nil {
		t.Fatalf("bootstrap vehicle A: %v", err)
	}
	if err := s.BootstrapDemoDevice(ctx, vehicleB, deviceB); err != nil {
		t.Fatalf("bootstrap vehicle B: %v", err)
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

	browser, sessionCookie := loginTestDispatcher(t, server.URL, adminUser, adminPass)
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	dispatchHeaders := http.Header{}
	dispatchHeaders.Set("Cookie", sessionCookie.String())
	dispatch, _, err := websocket.Dial(ctx, wsBase+"/api/v1/dispatch/ws", &websocket.DialOptions{HTTPHeader: dispatchHeaders})
	if err != nil {
		t.Fatalf("open dispatcher websocket: %v", err)
	}
	defer dispatch.Close(websocket.StatusNormalClosure, "test complete")

	tokenA := authenticateTestDevice(t, server.URL, vehicleA, deviceA)
	trackerA := openTestTracker(t, ctx, wsBase, tokenA)
	defer trackerA.Close(websocket.StatusNormalClosure, "test complete")
	presenceA := readPresence(t, ctx, dispatch)
	if !presenceA.Connected {
		t.Fatalf("vehicle A did not connect: %+v", presenceA)
	}

	tokenB := authenticateTestDevice(t, server.URL, vehicleB, deviceB)
	trackerB := openTestTracker(t, ctx, wsBase, tokenB)
	defer trackerB.Close(websocket.StatusNormalClosure, "test complete")
	presenceB := readPresence(t, ctx, dispatch)
	if !presenceB.Connected || presenceB.VehicleID == presenceA.VehicleID {
		t.Fatalf("vehicle B presence invalid: A=%+v B=%+v", presenceA, presenceB)
	}

	now := time.Now().UTC()
	oldFix := store.Location{
		TrackingSessionID: oldA,
		SequenceNumber:    1,
		RecordedAt:        now.Add(-2 * time.Minute),
		Latitude:          37.7749,
		Longitude:         -122.4194,
	}
	newFix := store.Location{
		TrackingSessionID: newA,
		SequenceNumber:    1,
		RecordedAt:        now.Add(-time.Minute),
		Latitude:          17.3029,
		Longitude:         -62.7178,
	}
	fixB := store.Location{
		TrackingSessionID: sessionB,
		SequenceNumber:    1,
		RecordedAt:        now,
		Latitude:          17.3250,
		Longitude:         -62.7400,
	}

	writeFixAndAssert(t, ctx, trackerA, dispatch, vehicleA, oldFix)
	writeFixAndAssert(t, ctx, trackerA, dispatch, vehicleA, newFix)
	writeFixAndAssert(t, ctx, trackerB, dispatch, vehicleB, fixB)

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

	var snapA, snapB *store.VehicleSnapshot
	for i := range fleet.Vehicles {
		switch fleet.Vehicles[i].VehicleCode {
		case vehicleA:
			snapA = &fleet.Vehicles[i]
		case vehicleB:
			snapB = &fleet.Vehicles[i]
		}
	}
	if snapA == nil || snapA.Location == nil || snapA.Location.TrackingSessionID != newA {
		t.Fatalf("vehicle A latest snapshot incorrect: %+v", snapA)
	}
	if snapB == nil || snapB.Location == nil || snapB.Location.TrackingSessionID != sessionB {
		t.Fatalf("vehicle B latest snapshot incorrect: %+v", snapB)
	}

	historyResp, err := browser.Get(server.URL + "/api/v1/vehicles/" + snapA.VehicleID + "/history?hours=1")
	if err != nil {
		t.Fatalf("read history: %v", err)
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
		t.Fatalf("decode history: %v", err)
	}
	if len(history.Locations) != 1 {
		t.Fatalf("expected only latest session in playback, got %d locations: %+v", len(history.Locations), history.Locations)
	}
	if history.Locations[0].TrackingSessionID != newA || history.Locations[0].Latitude != newFix.Latitude {
		t.Fatalf("playback leaked an older tracking session: %+v", history.Locations)
	}
}

func loginTestDispatcher(t *testing.T, serverURL, username, password string) (*http.Client, *http.Cookie) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := browser.Post(serverURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("dispatcher login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("dispatcher login status %d: %s", resp.StatusCode, payload)
	}
	parsed, _ := url.Parse(serverURL)
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == "ambulance_session" {
			return browser, cookie
		}
	}
	t.Fatal("dispatcher session cookie missing")
	return nil, nil
}

func authenticateTestDevice(t *testing.T, serverURL, vehicleCode, deviceKey string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"vehicle_code": vehicleCode, "device_key": deviceKey})
	resp, err := http.Post(serverURL+"/api/v1/device/session", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("device auth %s: %v", vehicleCode, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("device auth %s status %d: %s", vehicleCode, resp.StatusCode, payload)
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil || session.AccessToken == "" {
		t.Fatalf("decode device session %s: %v", vehicleCode, err)
	}
	return session.AccessToken
}

func openTestTracker(t *testing.T, ctx context.Context, wsBase, token string) *websocket.Conn {
	t.Helper()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/tracker/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("open tracker websocket: %v", err)
	}
	return conn
}

type testPresence struct {
	Type      string `json:"type"`
	VehicleID string `json:"vehicle_id"`
	Connected bool   `json:"connected"`
}

func readPresence(t *testing.T, ctx context.Context, dispatch *websocket.Conn) testPresence {
	t.Helper()
	var message testPresence
	if err := wsjson.Read(ctx, dispatch, &message); err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if message.Type != "presence" || message.VehicleID == "" {
		t.Fatalf("unexpected presence event: %+v", message)
	}
	return message
}

func writeFixAndAssert(t *testing.T, ctx context.Context, tracker, dispatch *websocket.Conn, vehicleCode string, fix store.Location) {
	t.Helper()
	if err := wsjson.Write(ctx, tracker, fix); err != nil {
		t.Fatalf("write %s fix: %v", vehicleCode, err)
	}
	var ack struct {
		Type           string `json:"type"`
		SequenceNumber int64  `json:"sequence_number"`
		Duplicate      bool   `json:"duplicate"`
	}
	if err := wsjson.Read(ctx, tracker, &ack); err != nil {
		t.Fatalf("read %s ack: %v", vehicleCode, err)
	}
	if ack.Type != "ack" || ack.SequenceNumber != fix.SequenceNumber || ack.Duplicate {
		t.Fatalf("unexpected %s ack: %+v", vehicleCode, ack)
	}
	var live struct {
		Type     string         `json:"type"`
		Location store.Location `json:"location"`
	}
	if err := wsjson.Read(ctx, dispatch, &live); err != nil {
		t.Fatalf("read %s dispatcher broadcast: %v", vehicleCode, err)
	}
	if live.Type != "location" || live.Location.VehicleCode != vehicleCode || live.Location.TrackingSessionID != fix.TrackingSessionID {
		t.Fatalf("unexpected %s dispatcher event: %+v", vehicleCode, live)
	}
}
