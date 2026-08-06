// services/upload/main.go

// Command upload is the aether-edit resumable chunked upload service
// (FT-2). It exposes the /v1/uploads session API, persists sessions and
// chunk maps in Postgres, stages chunks as S3 multipart parts, mints
// the content hash on completion, and emits the frozen v1 contract
// events over NATS JetStream.
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

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := LoadConfig()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	quota, err := contracts.LoadConfigQuota(cfg.QuotaConfigPath)
	if err != nil {
		log.Error("quota config", "err", err)
		os.Exit(1)
	}

	store, err := NewPostgresStore(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	blobs, err := NewS3BlobStore(cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
		cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3PathStyle)
	if err != nil {
		log.Error("s3", "err", err)
		os.Exit(1)
	}

	pub, err := NewJetStreamPublisher(ctx, cfg.NATSURL)
	if err != nil {
		log.Error("nats", "err", err)
		os.Exit(1)
	}
	defer pub.Close()

	srv := NewServer(store, blobs, quota, pub,
		NewInflightGauge(cfg.MaxInflightBytes),
		NewBackoffDirector(time.Second, 60*time.Second),
		log, cfg.AuthHS256Key, cfg.MaxObjectBytes)

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Routes(),
		// Chunk bodies are up to 64 MiB over slow links, so no global
		// read or write deadline; the header read is bounded.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	log.Info("upload service listening", "addr", cfg.ListenAddr)

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
			os.Exit(1)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			os.Exit(1)
		}
	}
}
