// services/tenancy/store.go

package main

import (
	"context"
	"errors"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// Workspace roles. Order of privilege: owner > admin > member.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// validRole reports whether r is a known workspace role.
func validRole(r string) bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleMember
}

// Sentinel store errors. Handlers map these to HTTP statuses.
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

// User is an authenticated human, keyed by the Google OIDC subject.
type User struct {
	ID                string
	GoogleSub         string
	Email             string
	Name              string
	ActiveWorkspaceID string // empty until the first workspace exists
	CreatedAt         time.Time
	LastLoginAt       time.Time
}

// Workspace is the tenancy unit every other service scopes by.
type Workspace struct {
	ID        string
	Name      string
	PlanTier  string
	CreatedBy string
	CreatedAt time.Time
}

// Membership binds a user to a workspace with a role.
type Membership struct {
	WorkspaceID string
	UserID      string
	Role        string
	CreatedAt   time.Time
}

// WorkspaceWithRole is a workspace joined with the caller's role.
type WorkspaceWithRole struct {
	Workspace
	Role string
}

// MemberWithUser is a membership joined with the member's identity.
type MemberWithUser struct {
	Membership
	Email string
	Name  string
}

// RefreshToken is one member of a rotation family. The raw token never
// touches the database: TokenHash is its SHA-256, hex encoded.
type RefreshToken struct {
	ID        string
	UserID    string
	FamilyID  string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// LoginState is the single-use state saved across the OIDC redirect:
// CSRF state key, ID token nonce, and the PKCE verifier.
type LoginState struct {
	State        string
	Nonce        string
	PKCEVerifier string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// APIKey is a per-workspace scoped key. The secret never touches the
// database: SecretHash is an argon2id PHC string.
type APIKey struct {
	ID          string
	WorkspaceID string
	Name        string
	SecretHash  string
	CreatedBy   string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

// UsageRollup is the per-workspace, per-UTC-month aggregate built from
// the metering stream. BytesUploaded sums upload_completed bytes;
// EncodeSeconds sums job_completed and job_failed encodeSeconds.
type UsageRollup struct {
	WorkspaceID      string
	Month            string // "YYYY-MM", UTC
	BytesUploaded    int64
	EncodeSeconds    float64
	UploadSessions   int64
	UploadsCompleted int64
	JobsQueued       int64
	JobsStarted      int64
	JobsCompleted    int64
	JobsFailed       int64
	UpdatedAt        time.Time
}

// Store is the persistence boundary. PostgresStore is the production
// implementation; tests run against the in-memory memStore double.
type Store interface {
	// Login state (single use across the OIDC redirect).
	SaveLoginState(ctx context.Context, ls LoginState) error
	// TakeLoginState returns and deletes the state; ErrNotFound if it
	// is absent, already consumed, or expired at now.
	TakeLoginState(ctx context.Context, state string, now time.Time) (LoginState, error)

	// Users.
	// UpsertUserByGoogleSub creates the user on first login and
	// refreshes email, name, and last login on later ones.
	UpsertUserByGoogleSub(ctx context.Context, googleSub, email, name string, now time.Time) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	SetActiveWorkspace(ctx context.Context, userID, workspaceID string) error

	// Workspaces and membership.
	// CreateWorkspace inserts ws and an owner membership atomically.
	CreateWorkspace(ctx context.Context, ws Workspace, ownerUserID string) error
	GetWorkspace(ctx context.Context, id string) (Workspace, error)
	ListWorkspacesForUser(ctx context.Context, userID string) ([]WorkspaceWithRole, error)
	GetMembership(ctx context.Context, workspaceID, userID string) (Membership, error)
	ListMembers(ctx context.Context, workspaceID string) ([]MemberWithUser, error)
	AddMember(ctx context.Context, m Membership) error
	UpdateMemberRole(ctx context.Context, workspaceID, userID, role string) error
	RemoveMember(ctx context.Context, workspaceID, userID string) error
	CountOwners(ctx context.Context, workspaceID string) (int, error)

	// Refresh tokens (rotation with server-side revocation).
	CreateRefreshToken(ctx context.Context, rt RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (RefreshToken, error)
	MarkRefreshTokenUsed(ctx context.Context, id string, at time.Time) error
	RevokeRefreshFamily(ctx context.Context, familyID string, at time.Time) error

	// API keys.
	CreateAPIKey(ctx context.Context, k APIKey) error
	GetAPIKey(ctx context.Context, id string) (APIKey, error)
	ListAPIKeys(ctx context.Context, workspaceID string) ([]APIKey, error)
	// RevokeAPIKey revokes the key only if it belongs to workspaceID.
	RevokeAPIKey(ctx context.Context, workspaceID, id string, at time.Time) error
	TouchAPIKey(ctx context.Context, id string, at time.Time) error

	// Metering. ApplyMetering records ev and applies delta to the
	// (workspace, month) rollup in one transaction. It returns false
	// without applying when ev.EventID was already recorded, which
	// makes consumption idempotent under JetStream redelivery.
	ApplyMetering(ctx context.Context, ev contracts.MeteringEvent, month string, delta UsageRollup) (bool, error)
	GetRollup(ctx context.Context, workspaceID, month string) (UsageRollup, error)
	// SumStorageBytes sums BytesUploaded across all months for the
	// workspace (cumulative landed bytes; see README for the honest
	// limits of this as a storage figure).
	SumStorageBytes(ctx context.Context, workspaceID string) (int64, error)
}
