package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Config struct {
	PublicOrigin     string
	SecureCookies    bool
	UserSessionTTL   time.Duration
	DeviceSessionTTL time.Duration
}

type App struct {
	store       *store.Store
	cfg         Config
	log         *slog.Logger
	hub         *Hub
	authLimiter *fixedWindowLimiter
}

type Hub struct {
	mu          sync.RWMutex
	dispatchers map[*websocket.Conn]struct{}
}

func NewHub() *Hub                      { return &Hub{dispatchers: map[*websocket.Conn]struct{}{}} }
func (h *Hub) add(c *websocket.Conn)    { h.mu.Lock(); h.dispatchers[c] = struct{}{}; h.mu.Unlock() }
func (h *Hub) remove(c *websocket.Conn) { h.mu.Lock(); delete(h.dispatchers, c); h.mu.Unlock() }
func (h *Hub) broadcast(v any) {
	h.mu.RLock()
	conns := make([]*websocket.Conn, 0, len(h.dispatchers))
	for c := range h.dispatchers {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = wsjson.Write(ctx, c, v)
		cancel()
	}
}

func New(s *store.Store, cfg Config, log *slog.Logger) *App {
	return &App{store: s, cfg: cfg, log: log, hub: NewHub(), authLimiter: newFixedWindowLimiter()}
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.requireUser(a.logout, true))
	mux.HandleFunc("GET /api/v1/vehicles", a.requireUser(a.vehicles, false))
	mux.HandleFunc("GET /api/v1/dispatch/ws", a.requireUser(a.dispatchWS, false))
	mux.HandleFunc("POST /api/v1/device/session", a.deviceSession)
	mux.HandleFunc("GET /api/v1/tracker/ws", a.trackerWS)
	mux.HandleFunc("POST /api/v1/tracker/history", a.deviceAuth(a.history))
	return securityHeaders(requestLog(mux, a.log))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(self)")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' wss:; img-src 'self' data: blob: https://tile.openstreetmap.org; worker-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; font-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func clientIP(r *http.Request) net.IP {
	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(r.RemoteAddr)
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.store.Health(ctx); err != nil {
		writeJSON(w, 503, map[string]string{"status": "unhealthy"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.authLimiter.allow("login:"+ip.String(), 10, time.Minute) {
		w.Header().Set("Retry-After", "60")
		a.store.Audit(r.Context(), "user", "", "login", "session", "", ip, "rate_limited", nil)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many authentication attempts"})
		return
	}

	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	u, err := a.store.AuthenticateUser(r.Context(), in.Username, in.Password)
	if err != nil {
		a.store.Audit(r.Context(), "user", in.Username, "login", "session", "", ip, "denied", nil)
		time.Sleep(250 * time.Millisecond)
		writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := randomToken()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
		return
	}
	csrf, err := randomToken()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
		return
	}
	expires := time.Now().Add(a.cfg.UserSessionTTL)
	if err := a.store.CreateUserSession(r.Context(), u.ID, token, csrf, expires); err != nil {
		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ambulance_session", Value: token, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(a.cfg.UserSessionTTL.Seconds())})
	a.store.Audit(r.Context(), "user", u.ID, "login", "session", "", ip, "success", nil)
	writeJSON(w, 200, map[string]any{"user": u, "csrf_token": csrf, "expires_at": expires})
}

func (a *App) sessionUser(r *http.Request) (store.User, string, error) {
	c, err := r.Cookie("ambulance_session")
	if err != nil {
		return store.User{}, "", err
	}
	u, err := a.store.UserFromSession(r.Context(), c.Value)
	return u, c.Value, err
}

func (a *App) requireUser(next http.HandlerFunc, csrf bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, token, err := a.sessionUser(r)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		if csrf && !a.store.ValidateCSRF(r.Context(), token, r.Header.Get("X-CSRF-Token")) {
			writeJSON(w, 403, map[string]string{"error": "csrf validation failed"})
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey{}, u)
		next(w, r.WithContext(ctx))
	}
}

type userContextKey struct{}

func userFromContext(ctx context.Context) store.User {
	u, _ := ctx.Value(userContextKey{}).(store.User)
	return u
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	c, _ := r.Cookie("ambulance_session")
	if c != nil {
		a.store.RevokeUserSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "ambulance_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	a.store.Audit(r.Context(), "user", u.ID, "logout", "session", "", clientIP(r), "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) vehicles(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	vs, err := a.store.VehicleSnapshots(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "query failed"})
		return
	}
	a.store.Audit(r.Context(), "user", u.ID, "vehicles.read", "fleet", "", clientIP(r), "success", map[string]any{"count": len(vs)})
	writeJSON(w, 200, map[string]any{"vehicles": vs, "server_time": time.Now().UTC()})
}

func (a *App) deviceSession(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.authLimiter.allow("device:"+ip.String(), 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		a.store.Audit(r.Context(), "device", "", "device.session.create", "device", "", ip, "rate_limited", nil)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many authentication attempts"})
		return
	}

	var in struct {
		VehicleCode string `json:"vehicle_code"`
		DeviceKey   string `json:"device_key"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	d, err := a.store.DeviceByVehicleCodeAndKey(r.Context(), strings.TrimSpace(in.VehicleCode), in.DeviceKey)
	if err != nil {
		a.store.Audit(r.Context(), "device", in.VehicleCode, "device.session.create", "device", "", ip, "denied", nil)
		writeJSON(w, 401, map[string]string{"error": "invalid device credentials"})
		return
	}
	token, err := randomToken()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
		return
	}
	expires := time.Now().Add(a.cfg.DeviceSessionTTL)
	if err := a.store.CreateDeviceSession(r.Context(), d.ID, token, expires); err != nil {
		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
		return
	}
	a.store.Audit(r.Context(), "device", d.ID, "device.session.create", "device", d.ID, ip, "success", nil)
	writeJSON(w, 200, map[string]any{"access_token": token, "expires_at": expires, "device": d})
}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	p := strings.SplitN(v, " ", 2)
	if len(p) == 2 && strings.EqualFold(p[0], "Bearer") {
		return strings.TrimSpace(p[1])
	}
	return ""
}

func (a *App) deviceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		d, err := a.store.DeviceFromSession(r.Context(), token)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), deviceContextKey{}, d)
		next(w, r.WithContext(ctx))
	}
}

type deviceContextKey struct{}

func deviceFromContext(ctx context.Context) store.Device {
	d, _ := ctx.Value(deviceContextKey{}).(store.Device)
	return d
}

func validateLocation(l *store.Location) error {
	if l.TrackingSessionID == "" || l.SequenceNumber < 0 || l.RecordedAt.IsZero() {
		return errors.New("missing required fields")
	}
	if l.Latitude < -90 || l.Latitude > 90 || l.Longitude < -180 || l.Longitude > 180 {
		return errors.New("invalid coordinates")
	}
	if l.RecordedAt.After(time.Now().Add(5*time.Minute)) || l.RecordedAt.Before(time.Now().Add(-7*24*time.Hour)) {
		return errors.New("recorded_at outside accepted window")
	}
	return nil
}

func (a *App) trackerWS(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	d, err := a.store.DeviceFromSession(r.Context(), token)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{a.cfg.PublicOrigin}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")
	c.SetReadLimit(8 << 10)
	a.store.Audit(r.Context(), "device", d.ID, "tracker.connect", "vehicle", d.VehicleID, clientIP(r), "success", nil)
	nextAuthCheck := time.Now().Add(30 * time.Second)
	for {
		var l store.Location
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		err := wsjson.Read(ctx, c, &l)
		cancel()
		if err != nil {
			return
		}
		if time.Now().After(nextAuthCheck) {
			if _, err := a.store.DeviceFromSession(r.Context(), token); err != nil {
				_ = c.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
			nextAuthCheck = time.Now().Add(30 * time.Second)
		}
		if err := validateLocation(&l); err != nil {
			_ = wsjson.Write(r.Context(), c, map[string]any{"type": "nack", "sequence_number": l.SequenceNumber, "error": err.Error()})
			continue
		}
		inserted, err := a.store.SaveLocation(r.Context(), d, l)
		if err != nil {
			a.log.Error("save location", "error", err)
			_ = wsjson.Write(r.Context(), c, map[string]any{"type": "nack", "sequence_number": l.SequenceNumber, "error": "persist failed"})
			continue
		}
		l.DeviceID = d.ID
		l.VehicleID = d.VehicleID
		l.VehicleCode = d.VehicleCode
		if inserted {
			a.hub.broadcast(map[string]any{"type": "location", "location": l, "received_at": time.Now().UTC()})
		}
		_ = wsjson.Write(r.Context(), c, map[string]any{"type": "ack", "sequence_number": l.SequenceNumber, "duplicate": !inserted})
	}
}

func (a *App) history(w http.ResponseWriter, r *http.Request) {
	d := deviceFromContext(r.Context())
	var in struct {
		Locations []store.Location `json:"locations"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Locations) == 0 || len(in.Locations) > 500 {
		writeJSON(w, 400, map[string]string{"error": "invalid batch"})
		return
	}
	accepted := 0
	duplicates := 0
	for i := range in.Locations {
		l := &in.Locations[i]
		if validateLocation(l) != nil {
			continue
		}
		inserted, err := a.store.SaveLocation(r.Context(), d, *l)
		if err != nil {
			continue
		}
		if inserted {
			accepted++
			l.DeviceID = d.ID
			l.VehicleID = d.VehicleID
			l.VehicleCode = d.VehicleCode
			a.hub.broadcast(map[string]any{"type": "location", "location": l, "received_at": time.Now().UTC(), "replayed": true})
		} else {
			duplicates++
		}
	}
	a.store.Audit(r.Context(), "device", d.ID, "tracker.replay", "vehicle", d.VehicleID, clientIP(r), "success", map[string]any{"accepted": accepted, "duplicates": duplicates})
	writeJSON(w, 200, map[string]int{"accepted": accepted, "duplicates": duplicates})
}

func (a *App) dispatchWS(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	sessionCookie, err := r.Cookie("ambulance_session")
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{a.cfg.PublicOrigin}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	a.hub.add(c)
	defer a.hub.remove(c)
	defer c.Close(websocket.StatusNormalClosure, "bye")
	a.store.Audit(r.Context(), "user", u.ID, "dispatch.connect", "fleet", "", clientIP(r), "success", nil)

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := a.store.UserFromSession(r.Context(), sessionCookie.Value); err != nil {
				_ = c.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := c.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
