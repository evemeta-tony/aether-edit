// services/upload/server.go

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// Server wires the upload service handlers to their dependencies.
type Server struct {
	store   Store
	blobs   BlobStore
	quota   contracts.QuotaChecker
	pub     Publisher
	gauge   *InflightGauge
	backoff *BackoffDirector
	log     *slog.Logger
	authKey []byte

	maxObjectBytes int64
	publishRetries int
	// chunkSize is ChunkSizeBytes in production; tests shrink it so
	// multi chunk flows stay fast.
	chunkSize int64
	now       func() time.Time
}

// NewServer builds a Server. maxObjectBytes caps declared upload sizes
// at the service boundary.
func NewServer(store Store, blobs BlobStore, quota contracts.QuotaChecker, pub Publisher,
	gauge *InflightGauge, backoff *BackoffDirector, log *slog.Logger,
	authKey []byte, maxObjectBytes int64) *Server {
	return &Server{
		store:          store,
		blobs:          blobs,
		quota:          quota,
		pub:            pub,
		gauge:          gauge,
		backoff:        backoff,
		log:            log,
		authKey:        authKey,
		maxObjectBytes: maxObjectBytes,
		publishRetries: 3,
		chunkSize:      ChunkSizeBytes,
		now:            time.Now,
	}
}

// Routes returns the full handler chain: request id and structured
// logging outermost, then bearer auth, then the API mux. /healthz is
// unauthenticated.
func (s *Server) Routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/uploads", s.handleCreate)
	api.HandleFunc("GET /v1/uploads/{id}", s.handleGet)
	api.HandleFunc("PUT /v1/uploads/{id}/chunks/{n}", s.handlePutChunk)
	api.HandleFunc("POST /v1/uploads/{id}/complete", s.handleComplete)
	api.HandleFunc("DELETE /v1/uploads/{id}", s.handleCancel)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.Handle("/v1/", AuthMiddleware(s.authKey, api))
	return s.requestLogger(root)
}

// requestLogger assigns a request id, echoes it as X-Request-Id, and
// emits one structured line per request with workspace and user ids
// when the call authenticated.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		w.Header().Set("X-Request-Id", requestID)
		state := &logState{}
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = context.WithValue(ctx, logStateKey{}, state)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := s.now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		attrs := []any{
			"requestId", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"durationMs", time.Since(start).Milliseconds(),
		}
		if id := state.identity(); id != nil {
			attrs = append(attrs, "workspaceId", id.WorkspaceID, "userId", id.UserID)
		}
		s.log.Info("request", attrs...)
	})
}

// statusRecorder captures the response status for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// createRequest is the strict boundary schema for POST /v1/uploads.
type createRequest struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
	Mime      string `json:"mime"`
}

var mimePattern = regexp.MustCompile(`^[A-Za-z0-9!#$&^_.+-]{1,127}/[A-Za-z0-9!#$&^_.+-]{1,127}$`)
var hex64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no identity")
		return
	}
	log := s.reqLog(r, id)

	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	var req createRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "body must be a JSON object with filename, sizeBytes, mime")
		return
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid_body", "trailing data after JSON object")
		return
	}
	if msg := validateCreate(&req, s.maxObjectBytes); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_body", msg)
		return
	}

	decision, err := s.quota.CheckUploadSession(r.Context(), id.WorkspaceID, req.SizeBytes)
	if err != nil {
		log.Error("quota check failed", "err", err)
		writeError(w, http.StatusInternalServerError, "quota_error", "quota check failed")
		return
	}
	if !decision.Allowed {
		writeError(w, http.StatusForbidden, "quota_denied", decision.Reason)
		return
	}

	sessionID, err := uuid.NewV7()
	if err != nil {
		log.Error("uuid", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "id generation failed")
		return
	}
	stagingKey := "staging/" + sessionID.String()
	s3UploadID, err := s.blobs.CreateMultipart(r.Context(), stagingKey, req.Mime)
	if err != nil {
		log.Error("create multipart failed", "err", err)
		writeError(w, http.StatusBadGateway, "storage_error", "object storage unavailable")
		return
	}

	chunkCount := int((req.SizeBytes + s.chunkSize - 1) / s.chunkSize)
	sess := &Session{
		ID:             sessionID,
		WorkspaceID:    id.WorkspaceID,
		UserID:         id.UserID,
		Filename:       req.Filename,
		SizeBytes:      req.SizeBytes,
		Mime:           req.Mime,
		ChunkSizeBytes: s.chunkSize,
		ChunkCount:     chunkCount,
		State:          SessionActive,
		S3UploadID:     s3UploadID,
		StagingKey:     stagingKey,
	}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		log.Error("persist session failed", "err", err)
		_ = s.blobs.AbortMultipart(r.Context(), stagingKey, s3UploadID)
		writeError(w, http.StatusInternalServerError, "store_error", "could not persist session")
		return
	}

	if err := s.publishMetering(r.Context(), id, contracts.MeteringUploadSessionCreated, &req.SizeBytes, ""); err != nil {
		log.Error("metering publish failed, rolling session back", "err", err)
		_ = s.blobs.AbortMultipart(r.Context(), stagingKey, s3UploadID)
		_ = s.store.SetSessionState(r.Context(), sessionID, SessionCancelled)
		writeError(w, http.StatusServiceUnavailable, "event_publish_failed", "could not emit metering event; retry")
		return
	}

	log.Info("session created", "uploadId", sessionID, "sizeBytes", req.SizeBytes, "chunkCount", chunkCount)
	writeJSON(w, http.StatusCreated, map[string]any{
		"uploadId":       sessionID.String(),
		"chunkSizeBytes": s.chunkSize,
		"chunkCount":     chunkCount,
	})
}

func validateCreate(req *createRequest, maxObjectBytes int64) string {
	if req.Filename == "" || len(req.Filename) > 512 {
		return "filename must be 1..512 characters"
	}
	if strings.ContainsAny(req.Filename, "/\x00") || strings.ContainsFunc(req.Filename, func(r rune) bool { return r < 0x20 }) {
		return "filename must not contain path separators or control characters"
	}
	if req.SizeBytes <= 0 {
		return "sizeBytes must be positive"
	}
	if req.SizeBytes > maxObjectBytes {
		return fmt.Sprintf("sizeBytes exceeds service maximum %d", maxObjectBytes)
	}
	if !mimePattern.MatchString(req.Mime) {
		return "mime must be a type/subtype token"
	}
	return ""
}

// loadOwnedSession fetches the session and enforces workspace
// ownership; foreign sessions read as not found.
func (s *Server) loadOwnedSession(w http.ResponseWriter, r *http.Request) (*Session, Identity, bool) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no identity")
		return nil, Identity{}, false
	}
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "upload id must be a UUID")
		return nil, Identity{}, false
	}
	sess, err := s.store.GetSession(r.Context(), sessionID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such upload")
		return nil, Identity{}, false
	}
	if err != nil {
		s.reqLog(r, id).Error("load session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not load session")
		return nil, Identity{}, false
	}
	if sess.WorkspaceID != id.WorkspaceID {
		writeError(w, http.StatusNotFound, "not_found", "no such upload")
		return nil, Identity{}, false
	}
	return sess, id, true
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	sess, id, ok := s.loadOwnedSession(w, r)
	if !ok {
		return
	}
	chunks, err := s.store.GetChunks(r.Context(), sess.ID)
	if err != nil {
		s.reqLog(r, id).Error("load chunks failed", "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not load chunk map")
		return
	}
	chunkStates := make([]map[string]any, 0, len(chunks))
	done := 0
	for _, c := range chunks {
		chunkStates = append(chunkStates, map[string]any{"index": c.Index, "state": c.State})
		if c.State == ChunkDone {
			done++
		}
	}
	resp := map[string]any{
		"uploadId":       sess.ID.String(),
		"filename":       sess.Filename,
		"sizeBytes":      sess.SizeBytes,
		"mime":           sess.Mime,
		"chunkSizeBytes": sess.ChunkSizeBytes,
		"chunkCount":     sess.ChunkCount,
		"state":          sess.State,
		"doneChunks":     done,
		"chunks":         chunkStates,
	}
	if sess.SHA256 != "" {
		resp["sha256"] = sess.SHA256
		resp["objectKey"] = sess.ObjectKey
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePutChunk(w http.ResponseWriter, r *http.Request) {
	sess, id, ok := s.loadOwnedSession(w, r)
	if !ok {
		return
	}
	log := s.reqLog(r, id)

	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= sess.ChunkCount {
		writeError(w, http.StatusBadRequest, "invalid_chunk_index",
			fmt.Sprintf("chunk index must be an integer in [0,%d)", sess.ChunkCount))
		return
	}
	if sess.State != SessionActive {
		writeError(w, http.StatusConflict, "session_not_active",
			fmt.Sprintf("session is %s", sess.State))
		return
	}

	expected := sess.ChunkSizeBytes
	if n == sess.ChunkCount-1 {
		expected = sess.SizeBytes - sess.ChunkSizeBytes*int64(sess.ChunkCount-1)
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "length_required", "Content-Length is required")
		return
	}
	if r.ContentLength != expected {
		writeError(w, http.StatusBadRequest, "chunk_size_mismatch",
			fmt.Sprintf("chunk %d must be exactly %d bytes", n, expected))
		return
	}
	wantSHA := strings.ToLower(r.Header.Get("X-Chunk-Sha256"))
	if !hex64Pattern.MatchString(wantSHA) {
		writeError(w, http.StatusBadRequest, "invalid_checksum_header",
			"X-Chunk-Sha256 must be 64 lowercase hex characters")
		return
	}

	// Backpressure: admit the body against the inflight bytes ceiling
	// before buffering anything.
	if !s.gauge.TryAcquire(expected) {
		retryAfter := s.backoff.Deny(sess.ID)
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]string{
				"code":    "saturated",
				"message": "inflight byte ceiling reached; retry after the hinted delay",
			},
			"backoff": map[string]any{"retryAfterMs": retryAfter.Milliseconds()},
		})
		return
	}
	defer s.gauge.Release(expected)

	claim, err := s.store.ClaimChunk(r.Context(), sess.ID, n)
	if err != nil {
		log.Error("claim chunk failed", "uploadId", sess.ID, "chunk", n, "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not claim chunk")
		return
	}
	if claim == ClaimAlreadyDone {
		// Idempotent re upload of a DONE chunk is a no op.
		writeJSON(w, http.StatusOK, map[string]any{"chunkIndex": n, "state": ChunkDone, "noop": true})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, expected+1))
	if err != nil || int64(len(body)) != expected {
		_ = s.store.MarkChunkRetry(r.Context(), sess.ID, n)
		writeError(w, http.StatusBadRequest, "chunk_body_truncated",
			fmt.Sprintf("chunk %d body did not match Content-Length", n))
		return
	}
	sum := sha256.Sum256(body)
	gotSHA := hex.EncodeToString(sum[:])
	if gotSHA != wantSHA {
		_ = s.store.MarkChunkRetry(r.Context(), sess.ID, n)
		log.Warn("chunk checksum mismatch", "uploadId", sess.ID, "chunk", n)
		writeError(w, http.StatusUnprocessableEntity, "chunk_checksum_mismatch",
			fmt.Sprintf("chunk %d sha256 does not match X-Chunk-Sha256", n))
		return
	}

	etag, err := s.blobs.UploadPart(r.Context(), sess.StagingKey, sess.S3UploadID, int32(n+1), body)
	if err != nil {
		_ = s.store.MarkChunkRetry(r.Context(), sess.ID, n)
		log.Error("upload part failed", "uploadId", sess.ID, "chunk", n, "err", err)
		writeError(w, http.StatusBadGateway, "storage_error", "object storage write failed")
		return
	}
	if err := s.store.MarkChunkDone(r.Context(), sess.ID, n, gotSHA, etag, expected); err != nil {
		log.Error("mark chunk done failed", "uploadId", sess.ID, "chunk", n, "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not record chunk")
		return
	}
	s.backoff.Reset(sess.ID)
	writeJSON(w, http.StatusOK, map[string]any{"chunkIndex": n, "state": ChunkDone})
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	sess, id, ok := s.loadOwnedSession(w, r)
	if !ok {
		return
	}
	log := s.reqLog(r, id)

	switch sess.State {
	case SessionCancelled:
		writeError(w, http.StatusConflict, "session_cancelled", "session was cancelled")
		return
	case SessionCompleted:
		writeJSON(w, http.StatusOK, completeResponse(sess))
		return
	}

	chunks, err := s.store.GetChunks(r.Context(), sess.ID)
	if err != nil {
		log.Error("load chunks failed", "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not load chunk map")
		return
	}
	var missing []int
	parts := make([]CompletedPart, 0, len(chunks))
	for _, c := range chunks {
		if c.State != ChunkDone {
			if len(missing) < 100 {
				missing = append(missing, c.Index)
			}
			continue
		}
		parts = append(parts, CompletedPart{PartNumber: int32(c.Index + 1), ETag: c.ETag})
	}
	if len(missing) > 0 || len(parts) != sess.ChunkCount {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code":    "upload_incomplete",
				"message": "not all chunks are DONE",
			},
			"missingChunks": missing,
		})
		return
	}

	if sess.State == SessionActive {
		if err := s.assemble(r.Context(), log, sess, parts); err != nil {
			log.Error("assembly failed", "uploadId", sess.ID, "err", err)
			writeError(w, http.StatusBadGateway, "assembly_failed", "object assembly failed; retry complete")
			return
		}
	}

	landed := contracts.LandedObjectEvent{
		UploadID:    sess.ID.String(),
		WorkspaceID: sess.WorkspaceID,
		UserID:      sess.UserID,
		ObjectKey:   sess.ObjectKey,
		SHA256:      sess.SHA256,
		SizeBytes:   sess.SizeBytes,
		Mime:        sess.Mime,
		LandedAt:    s.now().UTC(),
	}
	if err := s.publishJSON(r.Context(), contracts.SubjectUploadLanded, landed); err != nil {
		log.Error("landed event publish failed", "uploadId", sess.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "event_publish_failed", "could not emit landed event; retry complete")
		return
	}
	uid := Identity{WorkspaceID: sess.WorkspaceID, UserID: sess.UserID}
	if err := s.publishMetering(r.Context(), uid, contracts.MeteringUploadCompleted, &sess.SizeBytes, ""); err != nil {
		log.Error("metering publish failed", "uploadId", sess.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "event_publish_failed", "could not emit metering event; retry complete")
		return
	}
	if err := s.store.SetSessionState(r.Context(), sess.ID, SessionCompleted); err != nil {
		log.Error("set completed failed", "uploadId", sess.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not finalize session")
		return
	}
	sess.State = SessionCompleted
	log.Info("upload landed", "uploadId", sess.ID, "objectKey", sess.ObjectKey, "sha256", sess.SHA256)
	writeJSON(w, http.StatusOK, completeResponse(sess))
}

// assemble finalizes the multipart staging object, streams it back to
// mint the whole object sha256 (the S4 content hash), server side
// copies it to the content addressed key, and persists the result.
func (s *Server) assemble(ctx context.Context, log *slog.Logger, sess *Session, parts []CompletedPart) error {
	if err := s.blobs.CompleteMultipart(ctx, sess.StagingKey, sess.S3UploadID, parts); err != nil {
		// Crash tolerance: a previous attempt may have completed the
		// multipart upload before persisting ASSEMBLED. If the staging
		// object exists at full size, continue.
		size, exists, headErr := s.blobs.HeadObject(ctx, sess.StagingKey)
		if headErr != nil || !exists || size != sess.SizeBytes {
			return fmt.Errorf("complete multipart: %w", err)
		}
		log.Warn("multipart already completed; continuing", "uploadId", sess.ID)
	}

	body, err := s.blobs.GetObject(ctx, sess.StagingKey)
	if err != nil {
		return fmt.Errorf("read staging object: %w", err)
	}
	defer body.Close()
	hasher := sha256.New()
	copied, err := io.Copy(hasher, body)
	if err != nil {
		return fmt.Errorf("hash staging object: %w", err)
	}
	if copied != sess.SizeBytes {
		return fmt.Errorf("assembled size %d does not match declared %d", copied, sess.SizeBytes)
	}
	shaHex := hex.EncodeToString(hasher.Sum(nil))
	objectKey := fmt.Sprintf("assets/%s/sha256/%s", sess.WorkspaceID, shaHex)

	if err := s.blobs.Copy(ctx, sess.StagingKey, objectKey, sess.SizeBytes); err != nil {
		return fmt.Errorf("copy to final key: %w", err)
	}
	if err := s.blobs.Delete(ctx, sess.StagingKey); err != nil {
		// The final object landed; a leftover staging object is not
		// fatal. Log and continue.
		log.Warn("staging cleanup failed", "uploadId", sess.ID, "err", err)
	}
	if err := s.store.SetSessionAssembled(ctx, sess.ID, shaHex, objectKey); err != nil {
		return fmt.Errorf("persist assembled state: %w", err)
	}
	sess.State = SessionAssembled
	sess.SHA256 = shaHex
	sess.ObjectKey = objectKey
	return nil
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	sess, id, ok := s.loadOwnedSession(w, r)
	if !ok {
		return
	}
	log := s.reqLog(r, id)

	switch sess.State {
	case SessionCompleted:
		writeError(w, http.StatusConflict, "session_completed", "completed sessions cannot be cancelled")
		return
	case SessionCancelled:
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := s.blobs.AbortMultipart(r.Context(), sess.StagingKey, sess.S3UploadID); err != nil {
		log.Error("abort multipart failed", "uploadId", sess.ID, "err", err)
		writeError(w, http.StatusBadGateway, "storage_error", "could not garbage collect parts; retry cancel")
		return
	}
	if _, exists, err := s.blobs.HeadObject(r.Context(), sess.StagingKey); err == nil && exists {
		if err := s.blobs.Delete(r.Context(), sess.StagingKey); err != nil {
			log.Error("staging delete failed", "uploadId", sess.ID, "err", err)
			writeError(w, http.StatusBadGateway, "storage_error", "could not garbage collect staging object; retry cancel")
			return
		}
	}
	if err := s.store.SetSessionState(r.Context(), sess.ID, SessionCancelled); err != nil {
		log.Error("set cancelled failed", "uploadId", sess.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "store_error", "could not record cancellation")
		return
	}
	log.Info("session cancelled", "uploadId", sess.ID)
	w.WriteHeader(http.StatusNoContent)
}

func completeResponse(sess *Session) map[string]any {
	return map[string]any{
		"uploadId":  sess.ID.String(),
		"objectKey": sess.ObjectKey,
		"sha256":    sess.SHA256,
		"sizeBytes": sess.SizeBytes,
		"state":     sess.State,
	}
}

// publishJSON marshals v and publishes it with bounded retries so the
// JetStream delivery is at least once from the caller's point of view.
func (s *Server) publishJSON(ctx context.Context, subject string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", subject, err)
	}
	var last error
	for attempt := 0; attempt < s.publishRetries; attempt++ {
		if last = s.pub.Publish(ctx, subject, data); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	return last
}

func (s *Server) publishMetering(ctx context.Context, id Identity, kind contracts.MeteringKind, bytes *int64, jobID string) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return s.publishJSON(ctx, contracts.SubjectMetering, contracts.MeteringEvent{
		EventID:     eventID.String(),
		WorkspaceID: id.WorkspaceID,
		UserID:      id.UserID,
		Kind:        kind,
		Bytes:       bytes,
		JobID:       jobID,
		At:          s.now().UTC(),
	})
}

func (s *Server) reqLog(r *http.Request, id Identity) *slog.Logger {
	requestID, _ := r.Context().Value(requestIDKey{}).(string)
	return s.log.With(
		"requestId", requestID,
		"workspaceId", id.WorkspaceID,
		"userId", id.UserID,
	)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
