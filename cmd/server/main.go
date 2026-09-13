package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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

func envFloat(name string, fallback float64) float64 {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envKey32(name string) []byte {
	raw := os.Getenv(name)
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

	mfaKey := envKey32("MFA_ENCRYPTION_KEY")
	if os.Getenv("MFA_ENCRYPTION_KEY") != "" && len(mfaKey) != 32 {
		log.Warn("MFA_ENCRYPTION_KEY is invalid; MFA setup/verification will remain unavailable")
	}

	a := app.New(s, app.Config{
		PublicOrigin:     env("PUBLIC_ORIGIN", "localhost:*"),
		SecureCookies:    envBool("SECURE_COOKIES", true),
		UserSessionTTL:   8 * time.Hour,
		DeviceSessionTTL: 15 * time.Minute,
		MFAKey:           mfaKey,
		RouterURL:        os.Getenv("ROUTER_URL"),
		ETAFallbackKPH:   envFloat("ETA_FALLBACK_KPH", 35),
	}, log)
	srv := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           a.Routes(),
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
