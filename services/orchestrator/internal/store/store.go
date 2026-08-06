// services/orchestrator/internal/store/store.go
//
// Postgres persistence for jobs, presets, and probed sources, built on pgx
// with embedded SQL migrations (see migrate.go). State transitions are
// enforced in SQL WHERE clauses so concurrent writers cannot produce an
// illegal transition.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
)

// ErrNotFound is returned when a row does not exist (or is out of scope).
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a guarded transition matched no row (the job
// was not in the required state).
var ErrConflict = errors.New("conflict: entity not in required state")

// Source is a probed landed object.
type Source struct {
	ObjectKey   string           `json:"objectKey"`
	WorkspaceID string           `json:"workspaceId"`
	SHA256      string           `json:"sha256"`
	SizeBytes   int64            `json:"sizeBytes"`
	Mime        string           `json:"mime"`
	MediaInfo   engine.MediaInfo `json:"mediaInfo"`
	ProbedAt    time.Time        `json:"probedAt"`
}

// Postgres is the concrete store.
type Postgres struct {
	pool *pgxpool.Pool
}

// New connects a pool.
func New(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the pool.
func (p *Postgres) Close() { p.pool.Close() }

// ---- sources ----

// UpsertSource stores or refreshes a probed source.
func (p *Postgres) UpsertSource(ctx context.Context, s Source) error {
	mi, err := json.Marshal(s.MediaInfo)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO sources (object_key, workspace_id, sha256, size_bytes, mime, media_info, probed_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (object_key) DO UPDATE
		SET sha256 = EXCLUDED.sha256, size_bytes = EXCLUDED.size_bytes,
		    mime = EXCLUDED.mime, media_info = EXCLUDED.media_info, probed_at = now()`,
		s.ObjectKey, s.WorkspaceID, s.SHA256, s.SizeBytes, s.Mime, mi)
	return err
}

// GetSource loads a probed source scoped to a workspace.
func (p *Postgres) GetSource(ctx context.Context, workspaceID, objectKey string) (Source, error) {
	var s Source
	var mi []byte
	err := p.pool.QueryRow(ctx, `
		SELECT object_key, workspace_id, sha256, size_bytes, mime, media_info, probed_at
		FROM sources WHERE workspace_id = $1 AND object_key = $2`,
		workspaceID, objectKey).
		Scan(&s.ObjectKey, &s.WorkspaceID, &s.SHA256, &s.SizeBytes, &s.Mime, &mi, &s.ProbedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(mi, &s.MediaInfo); err != nil {
		return s, fmt.Errorf("media_info decode: %w", err)
	}
	return s, nil
}

// ---- presets ----

const presetCols = `id, workspace_id, name, container, video_codec, rate_control,
	crf, bitrate_kbps, max_bitrate_kbps, gop_length, speed_preset, ladder, created_at, updated_at`

func scanPreset(row pgx.Row) (jobs.Preset, error) {
	var pr jobs.Preset
	var ladder []byte
	err := row.Scan(&pr.ID, &pr.WorkspaceID, &pr.Name, &pr.Container, &pr.VideoCodec,
		&pr.RateControl, &pr.CRF, &pr.BitrateKbps, &pr.MaxBitrateKbps, &pr.GOPLength,
		&pr.SpeedPreset, &ladder, &pr.CreatedAt, &pr.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return pr, ErrNotFound
	}
	if err != nil {
		return pr, err
	}
	if err := json.Unmarshal(ladder, &pr.Ladder); err != nil {
		return pr, fmt.Errorf("ladder decode: %w", err)
	}
	return pr, nil
}

// CreatePreset inserts a validated preset and returns it with server fields.
func (p *Postgres) CreatePreset(ctx context.Context, pr jobs.Preset) (jobs.Preset, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return pr, err
	}
	ladder, err := json.Marshal(pr.Ladder)
	if err != nil {
		return pr, err
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO presets (id, workspace_id, name, container, video_codec, rate_control,
			crf, bitrate_kbps, max_bitrate_kbps, gop_length, speed_preset, ladder)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+presetCols,
		id.String(), pr.WorkspaceID, pr.Name, pr.Container, pr.VideoCodec, pr.RateControl,
		pr.CRF, pr.BitrateKbps, pr.MaxBitrateKbps, pr.GOPLength, pr.SpeedPreset, ladder)
	return scanPreset(row)
}

// GetPreset loads one preset scoped to a workspace.
func (p *Postgres) GetPreset(ctx context.Context, workspaceID, id string) (jobs.Preset, error) {
	row := p.pool.QueryRow(ctx,
		`SELECT `+presetCols+` FROM presets WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id)
	return scanPreset(row)
}

// ListPresets lists presets for a workspace, newest first.
func (p *Postgres) ListPresets(ctx context.Context, workspaceID string) ([]jobs.Preset, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+presetCols+` FROM presets WHERE workspace_id = $1 ORDER BY created_at DESC`,
		workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jobs.Preset{}
	for rows.Next() {
		pr, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// UpdatePreset replaces the mutable fields of a preset. The caller passes a
// fully validated preset (PATCH semantics are resolved by the API layer:
// load, apply changes, validate, then persist). The edit applies to jobs
// that start after the update; running jobs keep their snapshot.
func (p *Postgres) UpdatePreset(ctx context.Context, pr jobs.Preset) (jobs.Preset, error) {
	ladder, err := json.Marshal(pr.Ladder)
	if err != nil {
		return pr, err
	}
	row := p.pool.QueryRow(ctx, `
		UPDATE presets SET name = $3, container = $4, video_codec = $5, rate_control = $6,
			crf = $7, bitrate_kbps = $8, max_bitrate_kbps = $9, gop_length = $10,
			speed_preset = $11, ladder = $12, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+presetCols,
		pr.WorkspaceID, pr.ID, pr.Name, pr.Container, pr.VideoCodec, pr.RateControl,
		pr.CRF, pr.BitrateKbps, pr.MaxBitrateKbps, pr.GOPLength, pr.SpeedPreset, ladder)
	return scanPreset(row)
}

// ---- jobs ----

const jobCols = `id, workspace_id, user_id, preset_id, source_object_key, source_sha256,
	state, error_class, error_message, attempts, progress_pct, fps, speed_x, eta_seconds,
	outputs, created_at, queued_at, started_at, finished_at, updated_at`

func scanJob(row pgx.Row) (jobs.Job, error) {
	var j jobs.Job
	var errClass *string
	var outputs []byte
	err := row.Scan(&j.ID, &j.WorkspaceID, &j.UserID, &j.PresetID, &j.SourceObjectKey,
		&j.SourceSHA256, &j.State, &errClass, &j.ErrorMessage, &j.Attempts, &j.ProgressPct,
		&j.FPS, &j.SpeedX, &j.ETASeconds, &outputs, &j.CreatedAt, &j.QueuedAt,
		&j.StartedAt, &j.FinishedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if errClass != nil {
		j.ErrorClass = jobs.ErrorClass(*errClass)
	}
	if err := json.Unmarshal(outputs, &j.Outputs); err != nil {
		return j, fmt.Errorf("outputs decode: %w", err)
	}
	return j, nil
}

// CreateJob inserts a queued job.
func (p *Postgres) CreateJob(ctx context.Context, j jobs.Job) (jobs.Job, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return j, err
	}
	outputs, err := json.Marshal(j.Outputs)
	if err != nil {
		return j, err
	}
	if j.Outputs == nil {
		outputs = []byte("[]")
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO jobs (id, workspace_id, user_id, preset_id, source_object_key,
			source_sha256, state, outputs)
		VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7)
		RETURNING `+jobCols,
		id.String(), j.WorkspaceID, j.UserID, j.PresetID, j.SourceObjectKey, j.SourceSHA256, outputs)
	return scanJob(row)
}

// GetJob loads one job scoped to a workspace.
func (p *Postgres) GetJob(ctx context.Context, workspaceID, id string) (jobs.Job, error) {
	row := p.pool.QueryRow(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id)
	return scanJob(row)
}

// ListJobs lists jobs for a workspace, optionally filtered by state,
// newest first, capped at limit.
func (p *Postgres) ListJobs(ctx context.Context, workspaceID string, state *jobs.State, limit int) ([]jobs.Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE workspace_id = $1`
	args := []any{workspaceID}
	if state != nil {
		q += ` AND state = $2`
		args = append(args, string(*state))
	}
	// LIMIT is a bound parameter, never string-formatted SQL, so a future
	// user-supplied limit cannot become an injection surface (Argus PR#4
	// pass 2 finding N3).
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jobs.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountActiveJobs counts queued plus running jobs in a workspace (quota).
func (p *Postgres) CountActiveJobs(ctx context.Context, workspaceID string) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state IN ('queued', 'running')`,
		workspaceID).Scan(&n)
	return n, err
}

// ClaimNextQueued atomically moves the oldest queued job to running and
// returns it. It returns ErrNotFound when the queue is empty. SKIP LOCKED
// keeps concurrent claimers from double-claiming.
func (p *Postgres) ClaimNextQueued(ctx context.Context) (jobs.Job, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE jobs SET state = 'running', started_at = now(), updated_at = now(),
			error_class = NULL, error_message = '', attempts = attempts + 1
		WHERE id = (
			SELECT id FROM jobs WHERE state = 'queued'
			ORDER BY queued_at LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+jobCols)
	return scanJob(row)
}

// UpdateProgress persists live progress for a running job.
func (p *Postgres) UpdateProgress(ctx context.Context, id string, pct, fps, speedX, etaSeconds float64, outputs []jobs.OutputProgress) error {
	data, err := json.Marshal(outputs)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
		UPDATE jobs SET progress_pct = $2, fps = $3, speed_x = $4, eta_seconds = $5,
			outputs = $6, updated_at = now()
		WHERE id = $1 AND state = 'running'`,
		id, pct, fps, speedX, etaSeconds, data)
	return err
}

// MarkCompleted transitions running -> completed.
func (p *Postgres) MarkCompleted(ctx context.Context, id string, outputs []jobs.OutputProgress) (jobs.Job, error) {
	data, err := json.Marshal(outputs)
	if err != nil {
		return jobs.Job{}, err
	}
	row := p.pool.QueryRow(ctx, `
		UPDATE jobs SET state = 'completed', progress_pct = 100, eta_seconds = 0,
			outputs = $2, finished_at = now(), updated_at = now()
		WHERE id = $1 AND state = 'running'
		RETURNING `+jobCols, id, data)
	j, err := scanJob(row)
	if errors.Is(err, ErrNotFound) {
		return j, ErrConflict
	}
	return j, err
}

// MarkFailed transitions queued or running -> failed with taxonomy class.
func (p *Postgres) MarkFailed(ctx context.Context, id string, class jobs.ErrorClass, msg string) (jobs.Job, error) {
	if !jobs.ValidErrorClass(class) {
		return jobs.Job{}, fmt.Errorf("invalid error class %q", class)
	}
	row := p.pool.QueryRow(ctx, `
		UPDATE jobs SET state = 'failed', error_class = $2, error_message = $3,
			finished_at = now(), updated_at = now()
		WHERE id = $1 AND state IN ('queued', 'running')
		RETURNING `+jobCols, id, string(class), msg)
	j, err := scanJob(row)
	if errors.Is(err, ErrNotFound) {
		return j, ErrConflict
	}
	return j, err
}

// RetryJob transitions failed -> queued (workspace scoped; API entry point).
func (p *Postgres) RetryJob(ctx context.Context, workspaceID, id string) (jobs.Job, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE jobs SET state = 'queued', error_class = NULL, error_message = '',
			progress_pct = 0, fps = 0, speed_x = 0, eta_seconds = 0, outputs = '[]',
			queued_at = now(), started_at = NULL, finished_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND state = 'failed'
		RETURNING `+jobCols, workspaceID, id)
	j, err := scanJob(row)
	if errors.Is(err, ErrNotFound) {
		if _, gerr := p.GetJob(ctx, workspaceID, id); errors.Is(gerr, ErrNotFound) {
			return j, ErrNotFound
		}
		return j, ErrConflict
	}
	return j, err
}

// CancelQueued transitions queued -> failed for a user cancel. Running jobs
// are canceled through the scheduler (context cancellation) instead.
func (p *Postgres) CancelQueued(ctx context.Context, workspaceID, id string) (jobs.Job, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE jobs SET state = 'failed', error_class = 'internal',
			error_message = 'canceled by user', finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND state = 'queued'
		RETURNING `+jobCols, workspaceID, id)
	j, err := scanJob(row)
	if errors.Is(err, ErrNotFound) {
		if _, gerr := p.GetJob(ctx, workspaceID, id); errors.Is(gerr, ErrNotFound) {
			return j, ErrNotFound
		}
		return j, ErrConflict
	}
	return j, err
}
