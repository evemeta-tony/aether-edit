// services/telemetry/cmd/telemetry/main.go

// Command telemetry runs the aether-edit telemetry service (FT-4): the 1 Hz
// hardware sampler, the job progress aggregator, the log stream consumer,
// and the three SSE endpoints of contract 4.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/config"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/logstream"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/pipeline"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/sampler"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/server"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Sampler: CPU always; GPU via NVML with honest absence when unavailable.
	cpu, err := sampler.NewCPU("/proc/stat")
	if err != nil {
		return err
	}
	var (
		gpu       sampler.GPUReader
		gpuAbsent string
	)
	nv, err := sampler.NewNVML()
	if err != nil {
		var ue *sampler.UnavailableError
		if !errors.As(err, &ue) {
			return err
		}
		gpuAbsent = ue.Reason
		log.Warn("gpu telemetry unavailable", "reason", ue.Reason)
	} else {
		gpu = nv
	}
	sys := sampler.NewSystem(cpu, gpu, gpuAbsent)
	defer sys.Close()

	hardwareHub := hub.New(cfg.StreamBufferSize)
	jobsHub := hub.New(cfg.StreamBufferSize)
	logsHub := hub.New(cfg.StreamBufferSize)

	// The hardware pipeline runs under its own cancel and is waited on before
	// sys.Close fires: a sample in flight must never race NVML shutdown.
	// Deferred order on return: cancel the pipeline, wait for it to exit,
	// then Close (deferred above) releases NVML.
	pipeCtx, pipeCancel := context.WithCancel(ctx)
	pipelineDone := make(chan struct{})
	go func() {
		defer close(pipelineDone)
		pipeline.RunHardware(pipeCtx, sys, hardwareHub, cfg.SampleInterval, log)
	}()
	defer func() { <-pipelineDone }()
	defer pipeCancel()

	agg := jobs.New(jobsHub)
	logCons := logstream.New(logsHub)

	nc, err := nats.Connect(cfg.NATSURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return err
	}
	defer nc.Drain()

	if _, err := nc.Subscribe(jobs.SubjectJobState, func(m *nats.Msg) {
		if herr := agg.HandleState(m.Data); herr != nil {
			log.Warn("rejected job state event", "err", herr)
		}
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe(jobs.SubjectJobProgress, func(m *nats.Msg) {
		if herr := agg.HandleProgress(m.Data); herr != nil {
			log.Warn("rejected job progress event", "err", herr)
		}
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe(logstream.SubjectLog, func(m *nats.Msg) {
		if herr := logCons.Handle(m.Data); herr != nil {
			log.Warn("rejected log event", "err", herr)
		}
	}); err != nil {
		return err
	}

	handler := server.New(server.Options{
		AuthToken: cfg.AuthToken,
		Hardware:  hardwareHub,
		Jobs:      jobsHub,
		Logs:      logsHub,
		Heartbeat: cfg.HeartbeatInterval,
		Health: func() map[string]string {
			gpuState := sampler.GPUStateOK
			if gpu == nil {
				gpuState = sampler.GPUStateUnavailable
			}
			natsState := "disconnected"
			if nc.IsConnected() {
				natsState = "connected"
			}
			return map[string]string{"gpu": gpuState, "nats": natsState}
		},
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Tie request contexts to ctx so open SSE streams end on shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("telemetry service listening", "addr", cfg.ListenAddr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
