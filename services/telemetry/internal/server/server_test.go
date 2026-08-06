// services/telemetry/internal/server/server_test.go

package server

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/logstream"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/pipeline"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/sampler"
)

const testToken = "test-token-0123456789abcdef"

// fakeSampler is a test double for the Sampler interface (the NVML
// implementation ships in internal/sampler; this double exists only in
// test code).
type fakeSampler struct {
	sample sampler.Sample
}

func (f *fakeSampler) Sample(ctx context.Context) (sampler.Sample, error) { return f.sample, nil }
func (f *fakeSampler) Close() error                                       { return nil }

type sseEvent struct {
	name string
	data string
}

// readSSE collects parsed SSE events from url for d.
func readSSE(t *testing.T, url, token string, d time.Duration) []sseEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var evs []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.name != "" || cur.data != "" {
				evs = append(evs, cur)
			}
			cur = sseEvent{}
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return evs
}

func newTestServer(t *testing.T, smp sampler.Sampler) *httptest.Server {
	t.Helper()
	hw := hub.New(64)
	jobsHub := hub.New(64)
	logsHub := hub.New(64)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go pipeline.RunHardware(ctx, smp, hw, 20*time.Millisecond, slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(New(Options{
		AuthToken: testToken,
		Hardware:  hw,
		Jobs:      jobsHub,
		Logs:      logsHub,
		Heartbeat: time.Second,
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestSamplerToStreamPipeline(t *testing.T) {
	smp := &fakeSampler{sample: sampler.Sample{
		CPUUtilPct: 42.5,
		CPUValid:   true,
		GPU: &sampler.GPUSample{
			UtilPct:         77,
			VRAMUsedMB:      8192,
			VRAMTotalMB:     24576,
			JunctionC:       63,
			PowerW:          181.4,
			EncoderSessions: 2,
		},
		GPUStatus: sampler.GPUStatus{State: sampler.GPUStateOK},
	}}
	ts := newTestServer(t, smp)

	evs := readSSE(t, ts.URL+"/v1/streams/hardware", testToken, 300*time.Millisecond)
	var status, sample map[string]any
	for _, ev := range evs {
		switch ev.name {
		case "status":
			if status == nil {
				status = decode(t, ev.data)
			}
		case "sample":
			if sample == nil {
				sample = decode(t, ev.data)
			}
		}
	}
	if status == nil || status["gpu"] != "ok" || status["stream"] != "hardware" {
		t.Fatalf("status event = %v", status)
	}
	if sample == nil {
		t.Fatal("no sample event observed")
	}
	want := map[string]float64{
		"gpuUtilPct": 77, "vramUsedMB": 8192, "vramTotalMB": 24576,
		"junctionC": 63, "powerW": 181.4, "encoderSessions": 2, "cpuUtilPct": 42.5,
	}
	for k, v := range want {
		got, ok := sample[k].(float64)
		if !ok || got != v {
			t.Fatalf("sample[%q] = %v, want %v (full: %v)", k, sample[k], v, sample)
		}
	}
	if len(sample) != len(want) {
		t.Fatalf("sample has unexpected fields: %v", sample)
	}
}

func TestHonestAbsenceWithoutGPU(t *testing.T) {
	smp := &fakeSampler{sample: sampler.Sample{
		CPUUtilPct: 12.5,
		CPUValid:   true,
		GPU:        nil,
		GPUStatus: sampler.GPUStatus{
			State:  sampler.GPUStateUnavailable,
			Reason: "nvml init: ERROR_LIBRARY_NOT_FOUND",
		},
	}}
	ts := newTestServer(t, smp)

	evs := readSSE(t, ts.URL+"/v1/streams/hardware", testToken, 300*time.Millisecond)
	var status, sample map[string]any
	for _, ev := range evs {
		switch ev.name {
		case "status":
			if status == nil {
				status = decode(t, ev.data)
			}
		case "sample":
			if sample == nil {
				sample = decode(t, ev.data)
			}
		}
	}
	if status == nil {
		t.Fatal("no typed status event observed")
	}
	if status["gpu"] != "unavailable" || status["reason"] != "nvml init: ERROR_LIBRARY_NOT_FOUND" {
		t.Fatalf("status event = %v", status)
	}
	if sample == nil {
		t.Fatal("no sample event observed")
	}
	if got := sample["cpuUtilPct"].(float64); got != 12.5 {
		t.Fatalf("cpuUtilPct = %v", got)
	}
	// GPU keys must be absent entirely, never fabricated zeros.
	for _, k := range []string{"gpuUtilPct", "vramUsedMB", "vramTotalMB", "junctionC", "powerW", "encoderSessions"} {
		if _, present := sample[k]; present {
			t.Fatalf("field %q must be absent on a GPU-less host: %v", k, sample)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	ts := newTestServer(t, &fakeSampler{})
	for _, path := range []string{"/v1/streams/hardware", "/v1/streams/jobs", "/v1/streams/logs"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without token: status %d, want 401", path, resp.StatusCode)
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer wrong-token-0123456789")
		resp2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s with wrong token: status %d, want 401", path, resp2.StatusCode)
		}
	}
}

func TestLogsTagFilterValidation(t *testing.T) {
	ts := newTestServer(t, &fakeSampler{})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/streams/logs?tag=NOT%20VALID", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tag filter: status %d, want 400", resp.StatusCode)
	}
}

func TestLogStreamTaggedDelivery(t *testing.T) {
	hw := hub.New(64)
	jobsHub := hub.New(64)
	logsHub := hub.New(64)
	cons := logstream.New(logsHub)
	ts := httptest.NewServer(New(Options{
		AuthToken: testToken,
		Hardware:  hw,
		Jobs:      jobsHub,
		Logs:      logsHub,
		Heartbeat: time.Second,
	}))
	defer ts.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		msgs := []string{
			`{"line":"probe done","tag":"job","level":"info","at":"2026-08-06T10:00:00Z"}`,
			`{"line":"chunk uploaded","tag":"transfer","level":"debug","at":"2026-08-06T10:00:01Z"}`,
		}
		for _, m := range msgs {
			if err := cons.Handle([]byte(m)); err != nil {
				panic(err)
			}
		}
	}()

	evs := readSSE(t, ts.URL+"/v1/streams/logs?tag=transfer", testToken, 400*time.Millisecond)
	var logEvs []map[string]any
	for _, ev := range evs {
		if ev.name == "log" {
			logEvs = append(logEvs, decode(t, ev.data))
		}
	}
	if len(logEvs) != 1 {
		t.Fatalf("tag filter delivered %d log events, want 1: %v", len(logEvs), logEvs)
	}
	if logEvs[0]["line"] != "chunk uploaded" || logEvs[0]["tag"] != "transfer" || logEvs[0]["level"] != "debug" {
		t.Fatalf("log event = %v", logEvs[0])
	}

	if err := cons.Handle([]byte(`{"line":"x","tag":"BAD TAG","level":"info","at":"2026-08-06T10:00:00Z"}`)); err == nil {
		t.Fatal("invalid log tag must be rejected")
	}
	if err := cons.Handle([]byte(`{"line":"x","tag":"job","level":"loud","at":"2026-08-06T10:00:00Z"}`)); err == nil {
		t.Fatal("invalid log level must be rejected")
	}
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad JSON %q: %v", s, err)
	}
	return m
}
