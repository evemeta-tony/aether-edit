// services/tenancy/main.go

// Command tenancy is the aether-edit tenancy, auth, quota, metering,
// and API key service (FT-6a, S5 first deliverable). It runs the
// Google OIDC authorization-code flow, issues the short-lived HS256
// access tokens every FT service middleware verifies, manages
// workspaces and roles, meters usage from aether.ft.metering.v1 into
// Postgres rollups, enforces plan tier quotas through the frozen
// contracts.QuotaChecker interface, and manages per-workspace API
// keys.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// newUserID mints a uuidv7 user id (time-ordered, index friendly).
// uuid.NewV7 only fails if the system entropy source fails, which is
// not a condition to paper over with a fallback; Must panics loudly.
func newUserID() string {
	return uuid.Must(uuid.NewV7()).String()
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := LoadConfig()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tiers, err := LoadTierConfig(cfg.TiersConfigPath)
	if err != nil {
		log.Error("tier config", "err", err)
		os.Exit(1)
	}

	signer, err := newHS256Signer(cfg.AuthHS256Key)
	if err != nil {
		log.Error("signer", "err", err)
		os.Exit(1)
	}

	store, err := NewPostgresStore(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	oidc, err := NewOIDCClient(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL, nil)
	if err != nil {
		log.Error("oidc discovery", "err", err)
		os.Exit(1)
	}

	consumer, err := NewMeteringConsumer(ctx, cfg.NATSURL, store, log)
	if err != nil {
		log.Error("metering consumer", "err", err)
		os.Exit(1)
	}
	defer consumer.Close()

	quota := NewMeteredQuota(store, tiers)
	srv := NewServer(store, signer, oidc, tiers, quota, log,
		cfg.AccessTokenTTL, cfg.RefreshTokenTTL, cfg.InternalToken, cfg.CookieSecure)

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("tenancy service listening", "addr", cfg.ListenAddr, "alg", signer.Alg(),
		"defaultTier", tiers.DefaultTier, "issuer", cfg.OIDCIssuer)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http server", "err", err)
		os.Exit(1)
	}
	log.Info("tenancy service stopped")
}
