package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/app"
	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func isEnrollmentRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/api/v1/device/enroll" {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/vehicles/") && strings.HasSuffix(r.URL.Path, "/enrollment")
}

func isRoutingRequest(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.HasPrefix(r.URL.Path, "/api/v1/vehicles/") &&
		strings.HasSuffix(r.URL.Path, "/route")
}

func isFleetUtilityRequest(r *http.Request) bool {
	return (r.Method == http.MethodPost && r.URL.Path == "/api/v1/vehicles") ||
		(r.Method == http.MethodGet && r.URL.Path == "/api/v1/device/fleet")
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()
	s, err := store.Open(ctx, env("DATABASE_URL", "postgres://ambulance:ambulance@localhost:5432/ambulance?sslmode=disable"))
	if err != nil {
		log.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	if err := s.BootstrapAdmin(ctx, os.Getenv("BOOTSTRAP_ADMIN_USERNAME"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")); err != nil {
		log.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}
	if err := s.BootstrapDemoDevice(ctx, os.Getenv("DEMO_VEHICLE_CODE"), os.Getenv("DEMO_DEVICE_KEY")); err != nil {
		log.Error("demo device bootstrap failed", "error", err)
		os.Exit(1)
	}
	a := app.New(s, app.Config{
		PublicOrigin:     env("PUBLIC_ORIGIN", "localhost:*"),
		SecureCookies:    envBool("SECURE_COOKIES", true),
		UserSessionTTL:   8 * time.Hour,
		DeviceSessionTTL: 15 * time.Minute,
	}, log)

	coreRoutes := a.Routes()
	enrollmentRoutes := a.EnrollmentRoutes()
	routingRoutes := a.RoutingRoutes()
	fleetRoutes := a.FleetRoutes()
	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isEnrollmentRequest(r) {
			enrollmentRoutes.ServeHTTP(w, r)
			return
		}
		if isRoutingRequest(r) {
			routingRoutes.ServeHTTP(w, r)
			return
		}
		if isFleetUtilityRequest(r) {
			fleetRoutes.ServeHTTP(w, r)
			return
		}
		coreRoutes.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           rootHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		log.Info("server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
