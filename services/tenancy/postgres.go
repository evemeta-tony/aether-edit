// services/tenancy/postgres.go

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// PostgresStore is the production Store over a pgx pool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PostgresStore)(nil)

// NewPostgresStore connects, runs pending migrations, and returns the
// store.
func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	migConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := runMigrations(ctx, migConn); err != nil {
		_ = migConn.Close(ctx)
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := migConn.Close(ctx); err != nil {
		return nil, fmt.Errorf("close migration conn: %w", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// Close releases the pool.
func (p *PostgresStore) Close() {
	p.pool.Close()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ---- login states ----

func (p *PostgresStore) SaveLoginState(ctx context.Context, ls LoginState) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO tenancy_login_states
		(state, nonce, pkce_verifier, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		ls.State, ls.Nonce, ls.PKCEVerifier, ls.CreatedAt, ls.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save login state: %w", err)
	}
	return nil
}

func (p *PostgresStore) TakeLoginState(ctx context.Context, state string, now time.Time) (LoginState, error) {
	var ls LoginState
	err := p.pool.QueryRow(ctx, `DELETE FROM tenancy_login_states WHERE state = $1
		RETURNING state, nonce, pkce_verifier, created_at, expires_at`, state).
		Scan(&ls.State, &ls.Nonce, &ls.PKCEVerifier, &ls.CreatedAt, &ls.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginState{}, ErrNotFound
	}
	if err != nil {
		return LoginState{}, fmt.Errorf("take login state: %w", err)
	}
	if now.After(ls.ExpiresAt) {
		return LoginState{}, ErrNotFound
	}
	return ls, nil
}

// ---- users ----

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.ActiveWorkspaceID, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

const userCols = "id, google_sub, email, name, active_workspace_id, created_at, last_login_at"

func (p *PostgresStore) UpsertUserByGoogleSub(ctx context.Context, googleSub, email, name string, now time.Time) (User, error) {
	newID := newUserID()
	row := p.pool.QueryRow(ctx, `INSERT INTO tenancy_users
		(id, google_sub, email, name, active_workspace_id, created_at, last_login_at)
		VALUES ($1, $2, $3, $4, '', $5, $5)
		ON CONFLICT (google_sub) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, last_login_at = EXCLUDED.last_login_at
		RETURNING `+userCols, newID, googleSub, strings.ToLower(email), name, now)
	return scanUser(row)
}

func (p *PostgresStore) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx,
		"SELECT "+userCols+" FROM tenancy_users WHERE id = $1", id))
}

func (p *PostgresStore) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx,
		"SELECT "+userCols+" FROM tenancy_users WHERE lower(email) = lower($1)", email))
}

func (p *PostgresStore) SetActiveWorkspace(ctx context.Context, userID, workspaceID string) error {
	tag, err := p.pool.Exec(ctx,
		"UPDATE tenancy_users SET active_workspace_id = $2 WHERE id = $1", userID, workspaceID)
	if err != nil {
		return fmt.Errorf("set active workspace: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- workspaces and membership ----

func (p *PostgresStore) CreateWorkspace(ctx context.Context, ws Workspace, ownerUserID string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("workspace tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO tenancy_workspaces
		(id, name, plan_tier, created_by, created_at) VALUES ($1, $2, $3, $4, $5)`,
		ws.ID, ws.Name, ws.PlanTier, ws.CreatedBy, ws.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert workspace: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenancy_memberships
		(workspace_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`,
		ws.ID, ownerUserID, RoleOwner, ws.CreatedAt); err != nil {
		return fmt.Errorf("insert owner membership: %w", err)
	}
	return tx.Commit(ctx)
}

func (p *PostgresStore) GetWorkspace(ctx context.Context, id string) (Workspace, error) {
	var ws Workspace
	err := p.pool.QueryRow(ctx, `SELECT id, name, plan_tier, created_by, created_at
		FROM tenancy_workspaces WHERE id = $1`, id).
		Scan(&ws.ID, &ws.Name, &ws.PlanTier, &ws.CreatedBy, &ws.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	return ws, nil
}

func (p *PostgresStore) ListWorkspacesForUser(ctx context.Context, userID string) ([]WorkspaceWithRole, error) {
	rows, err := p.pool.Query(ctx, `SELECT w.id, w.name, w.plan_tier, w.created_by, w.created_at, m.role
		FROM tenancy_memberships m JOIN tenancy_workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	var out []WorkspaceWithRole
	for rows.Next() {
		var wr WorkspaceWithRole
		if err := rows.Scan(&wr.ID, &wr.Name, &wr.PlanTier, &wr.CreatedBy, &wr.CreatedAt, &wr.Role); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		out = append(out, wr)
	}
	return out, rows.Err()
}

func (p *PostgresStore) GetMembership(ctx context.Context, workspaceID, userID string) (Membership, error) {
	var m Membership
	err := p.pool.QueryRow(ctx, `SELECT workspace_id, user_id, role, created_at
		FROM tenancy_memberships WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID).
		Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("get membership: %w", err)
	}
	return m, nil
}

func (p *PostgresStore) ListMembers(ctx context.Context, workspaceID string) ([]MemberWithUser, error) {
	rows, err := p.pool.Query(ctx, `SELECT m.workspace_id, m.user_id, m.role, m.created_at, u.email, u.name
		FROM tenancy_memberships m JOIN tenancy_users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 ORDER BY m.created_at`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []MemberWithUser
	for rows.Next() {
		var m MemberWithUser
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt, &m.Email, &m.Name); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *PostgresStore) AddMember(ctx context.Context, m Membership) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO tenancy_memberships
		(workspace_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`,
		m.WorkspaceID, m.UserID, m.Role, m.CreatedAt)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

func (p *PostgresStore) UpdateMemberRole(ctx context.Context, workspaceID, userID, role string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE tenancy_memberships SET role = $3
		WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID, role)
	if err != nil {
		return fmt.Errorf("update member role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresStore) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM tenancy_memberships
		WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresStore) CountOwners(ctx context.Context, workspaceID string) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, `SELECT count(*) FROM tenancy_memberships
		WHERE workspace_id = $1 AND role = 'owner'`, workspaceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}

// ---- refresh tokens ----

func (p *PostgresStore) CreateRefreshToken(ctx context.Context, rt RefreshToken) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO tenancy_refresh_tokens
		(id, user_id, family_id, token_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		rt.ID, rt.UserID, rt.FamilyID, rt.TokenHash, rt.CreatedAt, rt.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

func (p *PostgresStore) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (RefreshToken, error) {
	var rt RefreshToken
	err := p.pool.QueryRow(ctx, `SELECT id, user_id, family_id, token_hash, created_at, expires_at, used_at, revoked_at
		FROM tenancy_refresh_tokens WHERE token_hash = $1`, tokenHash).
		Scan(&rt.ID, &rt.UserID, &rt.FamilyID, &rt.TokenHash, &rt.CreatedAt, &rt.ExpiresAt, &rt.UsedAt, &rt.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshToken{}, ErrNotFound
	}
	if err != nil {
		return RefreshToken{}, fmt.Errorf("get refresh token: %w", err)
	}
	return rt, nil
}

func (p *PostgresStore) MarkRefreshTokenUsed(ctx context.Context, id string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `UPDATE tenancy_refresh_tokens SET used_at = $2
		WHERE id = $1 AND used_at IS NULL`, id, at)
	if err != nil {
		return fmt.Errorf("mark refresh used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresStore) RevokeRefreshFamily(ctx context.Context, familyID string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE tenancy_refresh_tokens SET revoked_at = $2
		WHERE family_id = $1 AND revoked_at IS NULL`, familyID, at)
	if err != nil {
		return fmt.Errorf("revoke refresh family: %w", err)
	}
	return nil
}

// ---- api keys ----

func (p *PostgresStore) CreateAPIKey(ctx context.Context, k APIKey) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO tenancy_api_keys
		(id, workspace_id, name, secret_hash, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		k.ID, k.WorkspaceID, k.Name, k.SecretHash, k.CreatedBy, k.CreatedAt)
	if err != nil {
		return fmt.Errorf("create api key: %w", err)
	}
	return nil
}

const apiKeyCols = "id, workspace_id, name, secret_hash, created_by, created_at, last_used_at, revoked_at"

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.WorkspaceID, &k.Name, &k.SecretHash, &k.CreatedBy, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("scan api key: %w", err)
	}
	return k, nil
}

func (p *PostgresStore) GetAPIKey(ctx context.Context, id string) (APIKey, error) {
	return scanAPIKey(p.pool.QueryRow(ctx,
		"SELECT "+apiKeyCols+" FROM tenancy_api_keys WHERE id = $1", id))
}

func (p *PostgresStore) ListAPIKeys(ctx context.Context, workspaceID string) ([]APIKey, error) {
	rows, err := p.pool.Query(ctx,
		"SELECT "+apiKeyCols+" FROM tenancy_api_keys WHERE workspace_id = $1 ORDER BY created_at", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (p *PostgresStore) RevokeAPIKey(ctx context.Context, workspaceID, id string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `UPDATE tenancy_api_keys SET revoked_at = $3
		WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL`, id, workspaceID, at)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresStore) TouchAPIKey(ctx context.Context, id string, at time.Time) error {
	_, err := p.pool.Exec(ctx,
		"UPDATE tenancy_api_keys SET last_used_at = $2 WHERE id = $1", id, at)
	if err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

// ---- metering ----

func (p *PostgresStore) ApplyMetering(ctx context.Context, ev contracts.MeteringEvent, month string, delta UsageRollup) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("metering tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO tenancy_metering_events
		(event_id, workspace_id, user_id, kind, bytes, encode_seconds, job_id, at, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (event_id) DO NOTHING`,
		ev.EventID, ev.WorkspaceID, ev.UserID, string(ev.Kind), ev.Bytes, ev.EncodeSeconds, ev.JobID, ev.At)
	if err != nil {
		return false, fmt.Errorf("insert metering event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil // duplicate eventId: already applied
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenancy_usage_rollups
		(workspace_id, month, bytes_uploaded, encode_seconds, upload_sessions,
		 uploads_completed, jobs_queued, jobs_started, jobs_completed, jobs_failed, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (workspace_id, month) DO UPDATE SET
		 bytes_uploaded = tenancy_usage_rollups.bytes_uploaded + EXCLUDED.bytes_uploaded,
		 encode_seconds = tenancy_usage_rollups.encode_seconds + EXCLUDED.encode_seconds,
		 upload_sessions = tenancy_usage_rollups.upload_sessions + EXCLUDED.upload_sessions,
		 uploads_completed = tenancy_usage_rollups.uploads_completed + EXCLUDED.uploads_completed,
		 jobs_queued = tenancy_usage_rollups.jobs_queued + EXCLUDED.jobs_queued,
		 jobs_started = tenancy_usage_rollups.jobs_started + EXCLUDED.jobs_started,
		 jobs_completed = tenancy_usage_rollups.jobs_completed + EXCLUDED.jobs_completed,
		 jobs_failed = tenancy_usage_rollups.jobs_failed + EXCLUDED.jobs_failed,
		 updated_at = now()`,
		delta.WorkspaceID, month, delta.BytesUploaded, delta.EncodeSeconds, delta.UploadSessions,
		delta.UploadsCompleted, delta.JobsQueued, delta.JobsStarted, delta.JobsCompleted, delta.JobsFailed); err != nil {
		return false, fmt.Errorf("upsert rollup: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("metering commit: %w", err)
	}
	return true, nil
}

func (p *PostgresStore) GetRollup(ctx context.Context, workspaceID, month string) (UsageRollup, error) {
	var r UsageRollup
	err := p.pool.QueryRow(ctx, `SELECT workspace_id, month, bytes_uploaded, encode_seconds,
		upload_sessions, uploads_completed, jobs_queued, jobs_started, jobs_completed, jobs_failed, updated_at
		FROM tenancy_usage_rollups WHERE workspace_id = $1 AND month = $2`, workspaceID, month).
		Scan(&r.WorkspaceID, &r.Month, &r.BytesUploaded, &r.EncodeSeconds, &r.UploadSessions,
			&r.UploadsCompleted, &r.JobsQueued, &r.JobsStarted, &r.JobsCompleted, &r.JobsFailed, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UsageRollup{}, ErrNotFound
	}
	if err != nil {
		return UsageRollup{}, fmt.Errorf("get rollup: %w", err)
	}
	return r, nil
}

func (p *PostgresStore) SumStorageBytes(ctx context.Context, workspaceID string) (int64, error) {
	var n int64
	err := p.pool.QueryRow(ctx, `SELECT COALESCE(sum(bytes_uploaded), 0)
		FROM tenancy_usage_rollups WHERE workspace_id = $1`, workspaceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sum storage bytes: %w", err)
	}
	return n, nil
}
