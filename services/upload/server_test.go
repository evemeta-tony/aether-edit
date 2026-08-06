// services/upload/server_test.go

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// recordPublisher records published events and can inject failures.
type recordPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
	fail   bool
}

type recordedEvent struct {
	Subject string
	Data    []byte
}

func (p *recordPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("injected publish failure")
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	p.events = append(p.events, recordedEvent{Subject: subject, Data: cp})
	return nil
}

func (p *recordPublisher) setFail(fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail = fail
}

func (p *recordPublisher) bySubject(subject string) []recordedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []recordedEvent
	for _, e := range p.events {
		if e.Subject == subject {
			out = append(out, e)
		}
	}
	return out
}

const testBucket = "test-bucket"
const testChunkSize = 1024

type testEnv struct {
	t       *testing.T
	store   Store
	fake    *fakeS3
	fakeSrv *httptest.Server
	blobs   *S3BlobStore
	pub     *recordPublisher
	gauge   *InflightGauge
	srv     *Server
	api     *httptest.Server
	token   string
}

type envOption func(*envConfig)

type envConfig struct {
	quotaYAML      string
	inflightCeil   int64
	maxObjectBytes int64
}

func withQuotaYAML(y string) envOption { return func(c *envConfig) { c.quotaYAML = y } }
func withInflightCeiling(n int64) envOption {
	return func(c *envConfig) { c.inflightCeil = n }
}

func newTestEnv(t *testing.T, opts ...envOption) *testEnv {
	t.Helper()
	cfg := &envConfig{
		quotaYAML:      "defaults:\n  maxUploadBytes: 1073741824\n",
		inflightCeil:   1 << 30,
		maxObjectBytes: 1 << 30,
	}
	for _, o := range opts {
		o(cfg)
	}

	fake := newFakeS3(testBucket)
	fakeSrv := httptest.NewServer(fake)
	t.Cleanup(fakeSrv.Close)

	blobs, err := NewS3BlobStore(fakeSrv.URL, "test", testBucket, "test-access", "test-secret", true)
	if err != nil {
		t.Fatalf("NewS3BlobStore: %v", err)
	}

	quotaPath := t.TempDir() + "/quota.yaml"
	if err := writeTestFile(quotaPath, cfg.quotaYAML); err != nil {
		t.Fatal(err)
	}
	quota, err := contracts.LoadConfigQuota(quotaPath)
	if err != nil {
		t.Fatalf("LoadConfigQuota: %v", err)
	}

	env := &testEnv{
		t:       t,
		store:   NewMemStore(),
		fake:    fake,
		fakeSrv: fakeSrv,
		blobs:   blobs,
		pub:     &recordPublisher{},
		gauge:   NewInflightGauge(cfg.inflightCeil),
	}
	env.srv = env.newServer(quota, cfg.maxObjectBytes)
	env.api = httptest.NewServer(env.srv.Routes())
	t.Cleanup(env.api.Close)
	env.token = signTestToken(t, testAuthKey, validClaims(time.Now().Add(time.Hour)))
	return env
}

// newServer builds a Server over the env's shared store, fake S3, and
// publisher. Tests call it a second time to simulate a service restart
// rebuilding state from the persisted store.
func (e *testEnv) newServer(quota contracts.QuotaChecker, maxObjectBytes int64) *Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(e.store, e.blobs, quota, e.pub, e.gauge,
		NewBackoffDirector(100*time.Millisecond, 5*time.Second),
		log, testAuthKey, maxObjectBytes)
	srv.chunkSize = testChunkSize
	return srv
}

func writeTestFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

func (e *testEnv) do(method, path string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.api.URL+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if method == http.MethodPut && body != nil {
		req.ContentLength = int64(len(body))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp, respBody
}

func (e *testEnv) createSession(sizeBytes int64) (uploadID string, chunkCount int) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"filename":  "movie.mov",
		"sizeBytes": sizeBytes,
		"mime":      "video/quicktime",
	})
	resp, respBody := e.do(http.MethodPost, "/v1/uploads", body, nil)
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("create session: status %d body %s", resp.StatusCode, respBody)
	}
	var out struct {
		UploadID       string `json:"uploadId"`
		ChunkSizeBytes int64  `json:"chunkSizeBytes"`
		ChunkCount     int    `json:"chunkCount"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		e.t.Fatal(err)
	}
	if out.ChunkSizeBytes != testChunkSize {
		e.t.Fatalf("chunkSizeBytes = %d, want %d", out.ChunkSizeBytes, testChunkSize)
	}
	return out.UploadID, out.ChunkCount
}

func (e *testEnv) putChunk(uploadID string, n int, chunk []byte) (*http.Response, []byte) {
	e.t.Helper()
	sum := sha256.Sum256(chunk)
	return e.do(http.MethodPut,
		fmt.Sprintf("/v1/uploads/%s/chunks/%d", uploadID, n),
		chunk, map[string]string{"X-Chunk-Sha256": hex.EncodeToString(sum[:])})
}

func chunksOf(data []byte, size int) [][]byte {
	var out [][]byte
	for off := 0; off < len(data); off += size {
		end := off + size
		if end > len(data) {
			end = len(data)
		}
		out = append(out, data[off:end])
	}
	return out
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUploadLifecycleAndMintedHash(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize*2+512) // 3 chunks, short tail
	wantSHA := sha256.Sum256(data)
	wantHex := hex.EncodeToString(wantSHA[:])

	uploadID, chunkCount := env.createSession(int64(len(data)))
	if chunkCount != 3 {
		t.Fatalf("chunkCount = %d, want 3", chunkCount)
	}

	for i, chunk := range chunksOf(data, testChunkSize) {
		resp, body := env.putChunk(uploadID, i, chunk)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("chunk %d: status %d body %s", i, resp.StatusCode, body)
		}
	}

	resp, body := env.do(http.MethodGet, "/v1/uploads/"+uploadID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get session: %d %s", resp.StatusCode, body)
	}
	var state struct {
		DoneChunks int    `json:"doneChunks"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if state.DoneChunks != 3 || state.State != SessionActive {
		t.Fatalf("state = %+v", state)
	}

	resp, body = env.do(http.MethodPost, "/v1/uploads/"+uploadID+"/complete", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete: %d %s", resp.StatusCode, body)
	}
	var done struct {
		ObjectKey string `json:"objectKey"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	if err := json.Unmarshal(body, &done); err != nil {
		t.Fatal(err)
	}

	// The minted hash must equal the independently computed hash over
	// the assembled bytes, and the object key must embed it.
	if done.SHA256 != wantHex {
		t.Fatalf("minted sha256 = %s, want %s", done.SHA256, wantHex)
	}
	wantKey := "assets/ws-1/sha256/" + wantHex
	if done.ObjectKey != wantKey {
		t.Fatalf("objectKey = %s, want %s", done.ObjectKey, wantKey)
	}
	stored, ok := env.fake.object(wantKey)
	if !ok {
		t.Fatalf("object %s not in storage", wantKey)
	}
	if !bytes.Equal(stored, data) {
		t.Fatal("stored object bytes differ from uploaded data")
	}
	if _, ok := env.fake.object("staging/" + uploadID); ok {
		t.Fatal("staging object was not garbage collected")
	}

	landed := env.pub.bySubject(contracts.SubjectUploadLanded)
	if len(landed) != 1 {
		t.Fatalf("landed events = %d, want 1", len(landed))
	}
	var evt contracts.LandedObjectEvent
	if err := json.Unmarshal(landed[0].Data, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.UploadID != uploadID || evt.WorkspaceID != "ws-1" || evt.UserID != "user-1" ||
		evt.ObjectKey != wantKey || evt.SHA256 != wantHex ||
		evt.SizeBytes != int64(len(data)) || evt.Mime != "video/quicktime" ||
		evt.LandedAt.IsZero() {
		t.Fatalf("landed event = %+v", evt)
	}

	metering := env.pub.bySubject(contracts.SubjectMetering)
	if len(metering) != 2 {
		t.Fatalf("metering events = %d, want 2 (session_created, upload_completed)", len(metering))
	}
	var kinds []contracts.MeteringKind
	for _, m := range metering {
		var me contracts.MeteringEvent
		if err := json.Unmarshal(m.Data, &me); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, me.Kind)
		if me.WorkspaceID != "ws-1" || me.EventID == "" || me.At.IsZero() {
			t.Fatalf("metering event = %+v", me)
		}
		if me.Bytes == nil || *me.Bytes != int64(len(data)) {
			t.Fatalf("metering bytes = %v", me.Bytes)
		}
	}
	if kinds[0] != contracts.MeteringUploadSessionCreated || kinds[1] != contracts.MeteringUploadCompleted {
		t.Fatalf("metering kinds = %v", kinds)
	}

	// Complete is idempotent once COMPLETED.
	resp, body = env.do(http.MethodPost, "/v1/uploads/"+uploadID+"/complete", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second complete: %d %s", resp.StatusCode, body)
	}
}

func TestResumeAfterRestart(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize*3)
	chunks := chunksOf(data, testChunkSize)

	uploadID, _ := env.createSession(int64(len(data)))
	if resp, body := env.putChunk(uploadID, 0, chunks[0]); resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk 0: %d %s", resp.StatusCode, body)
	}

	// Simulate a service restart: a brand new Server over the same
	// persisted store and object storage.
	quota, err := contracts.NewConfigQuota(contracts.QuotaConfigFile{})
	if err != nil {
		t.Fatal(err)
	}
	restarted := env.newServer(quota, 1<<30)
	env.api.Close()
	env.api = httptest.NewServer(restarted.Routes())
	t.Cleanup(env.api.Close)

	// The resume query reflects the persisted chunk map.
	resp, body := env.do(http.MethodGet, "/v1/uploads/"+uploadID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get after restart: %d %s", resp.StatusCode, body)
	}
	var state struct {
		Chunks []struct {
			Index int    `json:"index"`
			State string `json:"state"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Chunks) != 3 || state.Chunks[0].State != ChunkDone ||
		state.Chunks[1].State != ChunkPending || state.Chunks[2].State != ChunkPending {
		t.Fatalf("chunk map after restart = %+v", state.Chunks)
	}

	// Idempotent re upload of the DONE chunk is a no op.
	before := env.fake.partPutCount()
	resp, body = env.putChunk(uploadID, 0, chunks[0])
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re upload done chunk: %d %s", resp.StatusCode, body)
	}
	var noop struct {
		Noop bool `json:"noop"`
	}
	if err := json.Unmarshal(body, &noop); err != nil {
		t.Fatal(err)
	}
	if !noop.Noop {
		t.Fatalf("want noop=true, got %s", body)
	}
	if env.fake.partPutCount() != before {
		t.Fatal("no op re upload must not write a part")
	}

	// Finish the remaining chunks on the restarted service and land.
	for i := 1; i < 3; i++ {
		if resp, body := env.putChunk(uploadID, i, chunks[i]); resp.StatusCode != http.StatusOK {
			t.Fatalf("chunk %d: %d %s", i, resp.StatusCode, body)
		}
	}
	resp, body = env.do(http.MethodPost, "/v1/uploads/"+uploadID+"/complete", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete after restart: %d %s", resp.StatusCode, body)
	}
	wantSHA := sha256.Sum256(data)
	if _, ok := env.fake.object("assets/ws-1/sha256/" + hex.EncodeToString(wantSHA[:])); !ok {
		t.Fatal("assembled object missing after restart flow")
	}
}

func TestCorruptedChunkRejectionAndReclaim(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize*2)
	chunks := chunksOf(data, testChunkSize)
	uploadID, _ := env.createSession(int64(len(data)))

	// Claimed checksum does not match the body: reject and mark RETRY.
	wrong := sha256.Sum256([]byte("not the chunk"))
	resp, body := env.do(http.MethodPut,
		fmt.Sprintf("/v1/uploads/%s/chunks/0", uploadID),
		chunks[0], map[string]string{"X-Chunk-Sha256": hex.EncodeToString(wrong[:])})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("corrupted chunk: status %d body %s", resp.StatusCode, body)
	}

	resp, body = env.do(http.MethodGet, "/v1/uploads/"+uploadID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("get session failed")
	}
	var state struct {
		Chunks []struct {
			Index int    `json:"index"`
			State string `json:"state"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if state.Chunks[0].State != ChunkRetry {
		t.Fatalf("chunk 0 state = %s, want RETRY", state.Chunks[0].State)
	}

	// Re claim with the correct body succeeds.
	if resp, body := env.putChunk(uploadID, 0, chunks[0]); resp.StatusCode != http.StatusOK {
		t.Fatalf("re claim: %d %s", resp.StatusCode, body)
	}
}

func TestCompleteWithMissingChunkRejected(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize*2)
	chunks := chunksOf(data, testChunkSize)
	uploadID, _ := env.createSession(int64(len(data)))

	if resp, body := env.putChunk(uploadID, 0, chunks[0]); resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk 0: %d %s", resp.StatusCode, body)
	}
	resp, body := env.do(http.MethodPost, "/v1/uploads/"+uploadID+"/complete", nil, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("complete with missing chunk: status %d body %s", resp.StatusCode, body)
	}
	var out struct {
		MissingChunks []int `json:"missingChunks"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.MissingChunks) != 1 || out.MissingChunks[0] != 1 {
		t.Fatalf("missingChunks = %v, want [1]", out.MissingChunks)
	}
	if len(env.pub.bySubject(contracts.SubjectUploadLanded)) != 0 {
		t.Fatal("no landed event may be emitted for an incomplete upload")
	}
}

func TestQuotaDenial(t *testing.T) {
	env := newTestEnv(t, withQuotaYAML(
		"defaults:\n  maxUploadBytes: 1073741824\nworkspaces:\n  ws-1:\n    maxUploadBytes: 100\n"))

	body, _ := json.Marshal(map[string]any{
		"filename":  "big.mov",
		"sizeBytes": 4096,
		"mime":      "video/quicktime",
	})
	resp, respBody := env.do(http.MethodPost, "/v1/uploads", body, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("quota denial: status %d body %s", resp.StatusCode, respBody)
	}
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Code != "quota_denied" || out.Error.Message != contracts.ReasonUploadSizeExceeded {
		t.Fatalf("error = %+v", out.Error)
	}
	if len(env.pub.bySubject(contracts.SubjectMetering)) != 0 {
		t.Fatal("denied session must not emit metering")
	}
}

func TestBackpressure429WithEscalatingHint(t *testing.T) {
	// Ceiling below one chunk: every chunk PUT is denied with a server
	// directed backoff that escalates per session.
	env := newTestEnv(t, withInflightCeiling(100))
	data := randomBytes(t, testChunkSize)
	uploadID, _ := env.createSession(int64(len(data)))

	var lastHint int64
	for i := 0; i < 3; i++ {
		resp, body := env.putChunk(uploadID, 0, data)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: status %d body %s", i, resp.StatusCode, body)
		}
		if resp.Header.Get("Retry-After") == "" {
			t.Fatal("missing Retry-After header")
		}
		var out struct {
			Backoff struct {
				RetryAfterMs int64 `json:"retryAfterMs"`
			} `json:"backoff"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		if out.Backoff.RetryAfterMs <= lastHint {
			t.Fatalf("attempt %d: hint %d did not escalate past %d", i, out.Backoff.RetryAfterMs, lastHint)
		}
		lastHint = out.Backoff.RetryAfterMs
	}
	if env.gauge.Current() != 0 {
		t.Fatalf("gauge leaked: %d", env.gauge.Current())
	}
}

func TestCompleteRetriesAfterPublishOutage(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize)
	uploadID, _ := env.createSession(int64(len(data)))
	if resp, body := env.putChunk(uploadID, 0, data); resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk: %d %s", resp.StatusCode, body)
	}

	env.pub.setFail(true)
	resp, body := env.do(http.MethodPost, "/v1/uploads/"+uploadID+"/complete", nil, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("complete during outage: status %d body %s", resp.StatusCode, body)
	}

	// The object is assembled and the session holds at ASSEMBLED so a
	// retry can republish (at least once delivery).
	resp, body = env.do(http.MethodGet, "/v1/uploads/"+uploadID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("get session failed")
	}
	var state struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if state.State != SessionAssembled {
		t.Fatalf("state = %s, want ASSEMBLED", state.State)
	}

	env.pub.setFail(false)
	resp, body = env.do(http.MethodPost, "/v1/uploads/"+uploadID+"/complete", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete retry: status %d body %s", resp.StatusCode, body)
	}
	if len(env.pub.bySubject(contracts.SubjectUploadLanded)) != 1 {
		t.Fatal("landed event missing after retry")
	}
}

func TestCancelGarbageCollectsParts(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize*2)
	chunks := chunksOf(data, testChunkSize)
	uploadID, _ := env.createSession(int64(len(data)))
	if resp, _ := env.putChunk(uploadID, 0, chunks[0]); resp.StatusCode != http.StatusOK {
		t.Fatal("chunk 0 failed")
	}
	if env.fake.uploadCount() != 1 {
		t.Fatalf("multipart uploads = %d, want 1", env.fake.uploadCount())
	}

	resp, body := env.do(http.MethodDelete, "/v1/uploads/"+uploadID, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: status %d body %s", resp.StatusCode, body)
	}
	if env.fake.uploadCount() != 0 {
		t.Fatal("multipart upload (and its parts) was not garbage collected")
	}

	// Cancel is idempotent; chunk writes after cancel are refused.
	if resp, _ := env.do(http.MethodDelete, "/v1/uploads/"+uploadID, nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatal("second cancel not idempotent")
	}
	if resp, _ := env.putChunk(uploadID, 1, chunks[1]); resp.StatusCode != http.StatusConflict {
		t.Fatal("chunk write after cancel must conflict")
	}
}

func TestBoundaryValidation(t *testing.T) {
	env := newTestEnv(t)
	data := randomBytes(t, testChunkSize)
	uploadID, _ := env.createSession(int64(len(data)))

	t.Run("unknown body field rejected", func(t *testing.T) {
		resp, _ := env.do(http.MethodPost, "/v1/uploads",
			[]byte(`{"filename":"a","sizeBytes":10,"mime":"video/mp4","extra":1}`), nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("bad mime rejected", func(t *testing.T) {
		resp, _ := env.do(http.MethodPost, "/v1/uploads",
			[]byte(`{"filename":"a","sizeBytes":10,"mime":"nonsense"}`), nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("negative size rejected", func(t *testing.T) {
		resp, _ := env.do(http.MethodPost, "/v1/uploads",
			[]byte(`{"filename":"a","sizeBytes":-5,"mime":"video/mp4"}`), nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("path traversal filename rejected", func(t *testing.T) {
		resp, _ := env.do(http.MethodPost, "/v1/uploads",
			[]byte(`{"filename":"../../etc/passwd","sizeBytes":10,"mime":"video/mp4"}`), nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("chunk index out of range rejected", func(t *testing.T) {
		resp, _ := env.putChunk(uploadID, 99, data)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("missing checksum header rejected", func(t *testing.T) {
		resp, _ := env.do(http.MethodPut,
			fmt.Sprintf("/v1/uploads/%s/chunks/0", uploadID), data, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("wrong content length rejected", func(t *testing.T) {
		short := data[:100]
		sum := sha256.Sum256(short)
		resp, _ := env.do(http.MethodPut,
			fmt.Sprintf("/v1/uploads/%s/chunks/0", uploadID),
			short, map[string]string{"X-Chunk-Sha256": hex.EncodeToString(sum[:])})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("non uuid id rejected", func(t *testing.T) {
		resp, _ := env.do(http.MethodGet, "/v1/uploads/not-a-uuid", nil, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
}

func TestAuthMiddlewareAtTheBoundary(t *testing.T) {
	env := newTestEnv(t)

	req, _ := http.NewRequest(http.MethodGet, env.api.URL+"/v1/uploads/"+"00000000-0000-0000-0000-000000000000", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token: status %d, want 401", resp.StatusCode)
	}

	req.Header.Set("Authorization", "Bearer garbage")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("garbage token: status %d, want 401", resp.StatusCode)
	}

	// A workspace cannot see another workspace's session.
	data := randomBytes(t, 64)
	uploadID, _ := env.createSession(int64(len(data)))
	otherToken := signTestToken(t, testAuthKey, map[string]any{
		"sub": "user-2", "workspaceId": "ws-2", "exp": time.Now().Add(time.Hour).Unix(),
	})
	req, _ = http.NewRequest(http.MethodGet, env.api.URL+"/v1/uploads/"+uploadID, nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross workspace read: status %d, want 404", resp.StatusCode)
	}
}
