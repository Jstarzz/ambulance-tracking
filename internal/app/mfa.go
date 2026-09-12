package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

const totpIssuer = "SKN EMS Tracker"

func generateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

func encryptSecret(key []byte, plaintext string) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("MFA encryption key is not configured")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func decryptSecret(key, ciphertext []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("MFA encryption key is not configured")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid encrypted MFA secret")
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func totpCode(secret string, at time.Time) (string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / 30)
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	binaryCode := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", binaryCode%1_000_000), nil
}

func verifyTOTP(secret, candidate string, now time.Time) bool {
	candidate = strings.TrimSpace(candidate)
	if len(candidate) != 6 {
		return false
	}
	if _, err := strconv.Atoi(candidate); err != nil {
		return false
	}
	for step := -1; step <= 1; step++ {
		expected, err := totpCode(secret, now.Add(time.Duration(step)*30*time.Second))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(candidate)) == 1 {
			return true
		}
	}
	return false
}

func (a *App) issueUserSession(w http.ResponseWriter, r *http.Request, u store.User) {
	token, err := randomToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session creation failed"})
		return
	}
	csrf, err := randomToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session creation failed"})
		return
	}
	expires := time.Now().Add(a.cfg.UserSessionTTL)
	if err := a.store.CreateUserSession(r.Context(), u.ID, token, csrf, expires); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session creation failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "ambulance_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(a.cfg.UserSessionTTL.Seconds()),
	})
	a.store.Audit(r.Context(), "user", u.ID, "login", "session", "", clientIP(r), "success", nil)
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "csrf_token": csrf, "expires_at": expires, "mfa_required": false})
}

func (a *App) verifyMFA(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.authLimiter.allow("mfa:"+ip.String(), 12, time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many verification attempts"})
		return
	}
	var in struct {
		ChallengeToken string `json:"challenge_token"`
		Code           string `json:"code"`
	}
	if readJSON(r, &in) != nil || strings.TrimSpace(in.ChallengeToken) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	u, encrypted, err := a.store.MFAChallenge(r.Context(), in.ChallengeToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired MFA challenge"})
		return
	}
	secret, err := decryptSecret(a.cfg.MFAKey, encrypted)
	if err != nil {
		a.log.Error("MFA secret unavailable", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "MFA verification unavailable"})
		return
	}
	if !verifyTOTP(secret, in.Code, time.Now()) {
		a.store.Audit(r.Context(), "user", u.ID, "mfa.verify", "session", "", ip, "denied", nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid MFA code"})
		return
	}
	if err := a.store.ConsumeMFAChallenge(r.Context(), in.ChallengeToken); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "MFA challenge already used"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "mfa.verify", "session", "", ip, "success", nil)
	a.issueUserSession(w, r, u)
}

func (a *App) setupMFA(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	if len(a.cfg.MFAKey) != 32 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "MFA encryption key is not configured"})
		return
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "MFA setup failed"})
		return
	}
	encrypted, err := encryptSecret(a.cfg.MFAKey, secret)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "MFA setup failed"})
		return
	}
	if err := a.store.SetUserTOTPSecret(r.Context(), u.ID, encrypted); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "MFA setup failed"})
		return
	}
	label := url.QueryEscape(totpIssuer + ":" + u.Username)
	issuer := url.QueryEscape(totpIssuer)
	otpauth := fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30", label, secret, issuer)
	a.store.Audit(r.Context(), "user", u.ID, "mfa.setup.begin", "user", u.ID, clientIP(r), "success", nil)
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth_uri": otpauth, "issuer": totpIssuer})
}

func (a *App) confirmMFA(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	var in struct {
		Code string `json:"code"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	cfg, err := a.store.UserMFAConfig(r.Context(), u.ID)
	if err != nil || len(cfg.SecretEnc) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "MFA setup has not been started"})
		return
	}
	secret, err := decryptSecret(a.cfg.MFAKey, cfg.SecretEnc)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "MFA verification unavailable"})
		return
	}
	if !verifyTOTP(secret, in.Code, time.Now()) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid MFA code"})
		return
	}
	if err := a.store.EnableUserTOTP(r.Context(), u.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "MFA setup failed"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "mfa.enable", "user", u.ID, clientIP(r), "success", nil)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

func (a *App) authSession(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	cfg, err := a.store.UserMFAConfig(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "mfa_enabled": cfg.Enabled})
}

func parseMFAKey(raw string) []byte {
	if raw == "" {
		return nil
	}
	decoded, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(raw)
	}
	if err != nil || len(decoded) != 32 {
		return nil
	}
	return decoded
}
