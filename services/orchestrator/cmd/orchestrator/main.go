// services/orchestrator/cmd/orchestrator/main.go
//
// Entry point for the aether-edit transcode job service (FT-3). Wires the
// Postgres store, the NATS JetStream queue and event streams, the FFmpeg
// TranscodeEngine adapter (with the gpl/nonfree buildconf gate: a forbidden
// build logs and exits), the quota hook, the scheduler (farm-of-one, three
// slots by default), the landed-object consumer, and the HTTP API.
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

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/auth"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/config"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/consumer"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine/ffmpegadapter"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/httpapi"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/metering"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/natsio"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/objstore"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/quota"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/scheduler"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// License gate first: refuse to run against a gpl or nonfree ffmpeg.
	eng, err := ffmpegadapter.New(ctx, ffmpegadapter.Config{
		FFmpegPath:  cfg.FFmpegPath,
		FFprobePath: cfg.FFprobePath,
	}, nil)
	if err != nil {
		if errors.Is(err, ffmpegadapter.ErrForbiddenBuild) {
			log.Error("ffmpeg build gate failed", "err", err)
			os.Exit(1)
		}
		return err
	}
	log.Info("ffmpeg build gate passed", "ffmpeg", cfg.FFmpegPath)

	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	objects, err := objstore.NewS3(cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
		cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3PathStyle)
	if err != nil {
		return err
	}
	scratch, err := scheduler.StagingRoot(cfg.ScratchDir)
	if err != nil {
		return err
	}

	conn, err := natsio.Connect(cfg.NATSURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	landedStream, err := conn.EnsureStreamForSubject(ctx, "AETHER_FT_UPLOAD", events.SubjectUploadLanded)
	if err != nil {
		return err
	}
	if _, err := conn.EnsureStreamForSubject(ctx, "AETHER_FT_METERING", events.SubjectMetering); err != nil {
		return err
	}

	meter := metering.New(conn)
	progress := natsio.NewProgressPublisher(conn)
	quotaChecker, err := quota.NewFromFile(cfg.QuotaConfigPath, st.CountActiveJobs)
	if err != nil {
		return err
	}

	sched, err := scheduler.New(scheduler.Config{
		Slots:        cfg.SchedulerSlots,
		PollInterval: cfg.SchedulerPollInterval,
		StagingDir:   scratch,
	}, st, objects, eng, progress, meter, log)
	if err != nil {
		return err
	}

	landed := consumer.New(consumer.Config{
		ScratchDir:     scratch,
		AutoCreateJobs: cfg.AutoCreateJobs,
	}, st, objects, eng, meter, progress, sched, log)
	stopConsumer, err := landed.Start(ctx, conn, landedStream)
	if err != nil {
		return err
	}
	defer stopConsumer()

	verifier, err := auth.NewVerifier(cfg.JWTSecret)
	if err != nil {
		return err
	}
	api := httpapi.New(st, sched, quotaChecker, meter, progress, log)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(verifier),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("http api listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		if err := sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	log.Info("shutdown complete")
	return nil
}
