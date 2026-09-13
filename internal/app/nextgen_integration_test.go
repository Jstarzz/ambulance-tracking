package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

func TestTOTPEncryptionAndVerification(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	encrypted, err := encryptSecret(key, secret)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("encrypted payload contains plaintext secret")
	}
	decrypted, err := decryptSecret(key, encrypted)
	if err != nil {
		t.Fatalf("decrypt secret: %v", err)
	}
	if decrypted != secret {
		t.Fatal("decrypted secret mismatch")
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	code, err := totpCode(secret, now)
	if err != nil {
		t.Fatalf("generate TOTP: %v", err)
	}
	if !verifyTOTP(secret, code, now) {
		t.Fatal("valid TOTP rejected")
	}
	if verifyTOTP(secret, "000000", now) && code != "000000" {
		t.Fatal("invalid TOTP accepted")
	}
}

func TestEnrollmentCompressedReplayEventsAndAIContext(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		vehicle   = "AMB-NEXTGEN"
	)
	if err := s.BootstrapAdmin(ctx, adminUser, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(s, Config{
		PublicOrigin:     "localhost:*",
		SecureCookies:    false,
		UserSessionTTL:   time.Hour,
		DeviceSessionTTL: 15 * time.Minute,
		MFAKey:           bytes.Repeat([]byte{0x33}, 32),
	}, logger)
	server := httptest.NewServer(a.Routes())
	defer server.Close()

	browser, csrfToken := loginNextgenAdmin(t, server.URL, adminUser, adminPass)
	enrollPayload, _ := json.Marshal(map[string]any{
		"vehicle_code":    vehicle,
		"vehicle_label":   "Next-gen integration ambulance",
		"device_name":     "Integration phone",
		"expires_minutes": 15,
	})
	enrollReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/admin/enrollments", bytes.NewReader(enrollPayload))
	enrollReq.Header.Set("Content-Type", "application/json")
	enrollReq.Header.Set("X-CSRF-Token", csrfToken)
	enrollResp, err := browser.Do(enrollReq)
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	defer enrollResp.Body.Close()
	if enrollResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(enrollResp.Body)
		t.Fatalf("create enrollment status %d: %s", enrollResp.StatusCode, body)
	}
	var enrollment struct {
		EnrollmentCode string `json:"enrollment_code"`
	}
	if err := json.NewDecoder(enrollResp.Body).Decode(&enrollment); err != nil || enrollment.EnrollmentCode == "" {
		t.Fatalf("decode enrollment: %v", err)
	}

	deviceKey := enrollTestDevice(t, server.URL, enrollment.EnrollmentCode, http.StatusCreated)
	_ = enrollTestDevice(t, server.URL, enrollment.EnrollmentCode, http.StatusUnauthorized)
	token := authenticateTestDevice(t, server.URL, vehicle, deviceKey)

	sessionID := "77777777-7777-4777-8777-777777777777"
	now := time.Now().UTC()
	locations := []store.Location{
		{TrackingSessionID: sessionID, SequenceNumber: 1, RecordedAt: now.Add(-2 * time.Second), Latitude: 17.31, Longitude: -62.74},
		{TrackingSessionID: sessionID, SequenceNumber: 2, RecordedAt: now, Latitude: 17.312, Longitude: -62.738},
	}
	replayBody, _ := json.Marshal(map[string]any{"locations": locations})
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(replayBody); err != nil {
		t.Fatalf("gzip replay: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	replayReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/tracker/history", bytes.NewReader(compressed.Bytes()))
	replayReq.Header.Set("Authorization", "Bearer "+token)
	replayReq.Header.Set("Content-Type", "application/json")
	replayReq.Header.Set("Content-Encoding", "gzip")
	replayResp, err := http.DefaultClient.Do(replayReq)
	if err != nil {
		t.Fatalf("compressed replay: %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(replayResp.Body)
		t.Fatalf("compressed replay status %d: %s", replayResp.StatusCode, body)
	}
	var replayResult struct {
		Accepted int `json:"accepted"`
	}
	if err := json.NewDecoder(replayResp.Body).Decode(&replayResult); err != nil || replayResult.Accepted != 2 {
		t.Fatalf("unexpected replay result: %+v err=%v", replayResult, err)
	}

	eventPayload, _ := json.Marshal(map[string]any{
		"id":                  "88888888-8888-4888-8888-888888888888",
		"tracking_session_id": sessionID,
		"event_type":          "CRASH_SUSPECTED",
		"severity":            "warning",
		"recorded_at":         now,
		"latitude":            17.312,
		"longitude":           -62.738,
		"metadata":            map[string]any{"g_force": 3.4, "requires_human_verification": true},
	})
	eventReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/tracker/events", bytes.NewReader(eventPayload))
	eventReq.Header.Set("Authorization", "Bearer "+token)
	eventReq.Header.Set("Content-Type", "application/json")
	eventResp, err := http.DefaultClient.Do(eventReq)
	if err != nil {
		t.Fatalf("post tracker event: %v", err)
	}
	defer eventResp.Body.Close()
	if eventResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(eventResp.Body)
		t.Fatalf("event status %d: %s", eventResp.StatusCode, body)
	}

	u, err := s.AuthenticateUser(ctx, adminUser, adminPass)
	if err != nil {
		t.Fatalf("authenticate admin for API token: %v", err)
	}
	rawAIToken := "ait_integration_read_only_token"
	if _, err := s.CreateAPIToken(ctx, u.ID, "integration-ai", rawAIToken, []string{"fleet:read", "events:read"}, nil); err != nil {
		t.Fatalf("create AI token: %v", err)
	}
	aiReq, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/ai/fleet-context", nil)
	aiReq.Header.Set("Authorization", "Bearer "+rawAIToken)
	aiResp, err := http.DefaultClient.Do(aiReq)
	if err != nil {
		t.Fatalf("read AI context: %v", err)
	}
	defer aiResp.Body.Close()
	if aiResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(aiResp.Body)
		t.Fatalf("AI context status %d: %s", aiResp.StatusCode, body)
	}
	var aiContext struct {
		PatientDataIncluded bool `json:"patient_data_included"`
		Vehicles            []struct {
			VehicleCode string `json:"vehicle_code"`
		} `json:"vehicles"`
		UnacknowledgedEvents []store.VehicleEvent `json:"unacknowledged_events"`
	}
	if err := json.NewDecoder(aiResp.Body).Decode(&aiContext); err != nil {
		t.Fatalf("decode AI context: %v", err)
	}
	if aiContext.PatientDataIncluded {
		t.Fatal("AI context incorrectly claims patient data is included")
	}
	found := false
	for _, v := range aiContext.Vehicles {
		if v.VehicleCode == vehicle {
			found = true
		}
	}
	if !found {
		t.Fatalf("AI fleet context missing %s", vehicle)
	}
	if len(aiContext.UnacknowledgedEvents) == 0 {
		t.Fatal("AI context missing unacknowledged crash event")
	}
}

func loginNextgenAdmin(t *testing.T, serverURL, username, password string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := browser.Post(serverURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("admin login status %d: %s", resp.StatusCode, payload)
	}
	var result struct {
		CSRFToken   string `json:"csrf_token"`
		MFARequired bool   `json:"mfa_required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode admin login: %v", err)
	}
	if result.MFARequired || result.CSRFToken == "" {
		t.Fatalf("unexpected MFA/login state: %+v", result)
	}
	parsed, _ := url.Parse(serverURL)
	if len(jar.Cookies(parsed)) == 0 {
		t.Fatal("admin session cookie missing")
	}
	return browser, result.CSRFToken
}

func enrollTestDevice(t *testing.T, serverURL, enrollmentCode string, expectedStatus int) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"enrollment_code": enrollmentCode})
	resp, err := http.Post(serverURL+"/api/v1/device/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("device enrollment: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("device enrollment status %d want %d: %s", resp.StatusCode, expectedStatus, payload)
	}
	if expectedStatus != http.StatusCreated {
		return ""
	}
	var result struct {
		DeviceKey string `json:"device_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.DeviceKey == "" {
		t.Fatalf("decode device enrollment: %v", err)
	}
	return result.DeviceKey
}
