// services/tenancy/memstore_test.go

package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// memStore is the in-memory Store test double (test-only, mirroring
// the FT-2 lane's pattern). PostgresStore is the production
// implementation; postgres_test.go exercises it against a real
// database when TENANCY_TEST_DATABASE_URL is set.
type memStore struct {
	mu sync.Mutex

	loginStates map[string]LoginState
	users       map[string]User
	workspaces  map[string]Workspace
	memberships map[string]map[string]Membership // workspaceID -> userID
	refresh     map[string]RefreshToken          // by id
	apiKeys     map[string]APIKey
	events      map[string]contracts.MeteringEvent
	rollups     map[string]UsageRollup // workspaceID + "|" + month
}

func newMemStore() *memStore {
	return &memStore{
		loginStates: map[string]LoginState{},
		users:       map[string]User{},
		workspaces:  map[string]Workspace{},
		memberships: map[string]map[string]Membership{},
		refresh:     map[string]RefreshToken{},
		apiKeys:     map[string]APIKey{},
		events:      map[string]contracts.MeteringEvent{},
		rollups:     map[string]UsageRollup{},
	}
}

var _ Store = (*memStore)(nil)

func (m *memStore) SaveLoginState(_ context.Context, ls LoginState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loginStates[ls.State] = ls
	return nil
}

func (m *memStore) TakeLoginState(_ context.Context, state string, now time.Time) (LoginState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.loginStates[state]
	if !ok {
		return LoginState{}, ErrNotFound
	}
	delete(m.loginStates, state)
	if now.After(ls.ExpiresAt) {
		return LoginState{}, ErrNotFound
	}
	return ls, nil
}

func (m *memStore) UpsertUserByGoogleSub(_ context.Context, googleSub, email, name string, now time.Time) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, u := range m.users {
		if u.GoogleSub == googleSub {
			u.Email = strings.ToLower(email)
			u.Name = name
			u.LastLoginAt = now
			m.users[id] = u
			return u, nil
		}
	}
	u := User{
		ID:          newUserID(),
		GoogleSub:   googleSub,
		Email:       strings.ToLower(email),
		Name:        name,
		CreatedAt:   now,
		LastLoginAt: now,
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) GetUser(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *memStore) GetUserByEmail(_ context.Context, email string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == strings.ToLower(email) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *memStore) SetActiveWorkspace(_ context.Context, userID, workspaceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.ActiveWorkspaceID = workspaceID
	m.users[userID] = u
	return nil
}

func (m *memStore) CreateWorkspace(_ context.Context, ws Workspace, ownerUserID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workspaces[ws.ID]; ok {
		return ErrDuplicate
	}
	m.workspaces[ws.ID] = ws
	m.memberships[ws.ID] = map[string]Membership{
		ownerUserID: {WorkspaceID: ws.ID, UserID: ownerUserID, Role: RoleOwner, CreatedAt: ws.CreatedAt},
	}
	return nil
}

func (m *memStore) GetWorkspace(_ context.Context, id string) (Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws, ok := m.workspaces[id]
	if !ok {
		return Workspace{}, ErrNotFound
	}
	return ws, nil
}

func (m *memStore) ListWorkspacesForUser(_ context.Context, userID string) ([]WorkspaceWithRole, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []WorkspaceWithRole
	for wsID, members := range m.memberships {
		if mem, ok := members[userID]; ok {
			out = append(out, WorkspaceWithRole{Workspace: m.workspaces[wsID], Role: mem.Role})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *memStore) GetMembership(_ context.Context, workspaceID, userID string) (Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memberships[workspaceID][userID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	return mem, nil
}

func (m *memStore) ListMembers(_ context.Context, workspaceID string) ([]MemberWithUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MemberWithUser
	for _, mem := range m.memberships[workspaceID] {
		u := m.users[mem.UserID]
		out = append(out, MemberWithUser{Membership: mem, Email: u.Email, Name: u.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *memStore) AddMember(_ context.Context, mem Membership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	members, ok := m.memberships[mem.WorkspaceID]
	if !ok {
		return ErrNotFound
	}
	if _, dup := members[mem.UserID]; dup {
		return ErrDuplicate
	}
	members[mem.UserID] = mem
	return nil
}

func (m *memStore) UpdateMemberRole(_ context.Context, workspaceID, userID, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memberships[workspaceID][userID]
	if !ok {
		return ErrNotFound
	}
	mem.Role = role
	m.memberships[workspaceID][userID] = mem
	return nil
}

func (m *memStore) RemoveMember(_ context.Context, workspaceID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.memberships[workspaceID][userID]; !ok {
		return ErrNotFound
	}
	delete(m.memberships[workspaceID], userID)
	return nil
}

func (m *memStore) CountOwners(_ context.Context, workspaceID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, mem := range m.memberships[workspaceID] {
		if mem.Role == RoleOwner {
			n++
		}
	}
	return n, nil
}

func (m *memStore) CreateRefreshToken(_ context.Context, rt RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[rt.ID] = rt
	return nil
}

func (m *memStore) GetRefreshTokenByHash(_ context.Context, tokenHash string) (RefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rt := range m.refresh {
		if rt.TokenHash == tokenHash {
			return rt, nil
		}
	}
	return RefreshToken{}, ErrNotFound
}

func (m *memStore) MarkRefreshTokenUsed(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.refresh[id]
	if !ok || rt.UsedAt != nil {
		return ErrNotFound
	}
	rt.UsedAt = &at
	m.refresh[id] = rt
	return nil
}

func (m *memStore) RevokeRefreshFamily(_ context.Context, familyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rt := range m.refresh {
		if rt.FamilyID == familyID && rt.RevokedAt == nil {
			rt.RevokedAt = &at
			m.refresh[id] = rt
		}
	}
	return nil
}

func (m *memStore) CreateAPIKey(_ context.Context, k APIKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.apiKeys[k.ID]; dup {
		return ErrDuplicate
	}
	m.apiKeys[k.ID] = k
	return nil
}

func (m *memStore) GetAPIKey(_ context.Context, id string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[id]
	if !ok {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

func (m *memStore) ListAPIKeys(_ context.Context, workspaceID string) ([]APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIKey
	for _, k := range m.apiKeys {
		if k.WorkspaceID == workspaceID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *memStore) RevokeAPIKey(_ context.Context, workspaceID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[id]
	if !ok || k.WorkspaceID != workspaceID || k.RevokedAt != nil {
		return ErrNotFound
	}
	k.RevokedAt = &at
	m.apiKeys[id] = k
	return nil
}

func (m *memStore) TouchAPIKey(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[id]
	if !ok {
		return ErrNotFound
	}
	k.LastUsedAt = &at
	m.apiKeys[id] = k
	return nil
}

func rollupKey(workspaceID, month string) string { return workspaceID + "|" + month }

func (m *memStore) ApplyMetering(_ context.Context, ev contracts.MeteringEvent, month string, delta UsageRollup) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.events[ev.EventID]; dup {
		return false, nil
	}
	m.events[ev.EventID] = ev
	key := rollupKey(delta.WorkspaceID, month)
	r := m.rollups[key]
	r.WorkspaceID = delta.WorkspaceID
	r.Month = month
	r.BytesUploaded += delta.BytesUploaded
	r.EncodeSeconds += delta.EncodeSeconds
	r.UploadSessions += delta.UploadSessions
	r.UploadsCompleted += delta.UploadsCompleted
	r.JobsQueued += delta.JobsQueued
	r.JobsStarted += delta.JobsStarted
	r.JobsCompleted += delta.JobsCompleted
	r.JobsFailed += delta.JobsFailed
	r.UpdatedAt = time.Now()
	m.rollups[key] = r
	return true, nil
}

func (m *memStore) GetRollup(_ context.Context, workspaceID, month string) (UsageRollup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rollups[rollupKey(workspaceID, month)]
	if !ok {
		return UsageRollup{}, ErrNotFound
	}
	return r, nil
}

func (m *memStore) SumStorageBytes(_ context.Context, workspaceID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.rollups {
		if r.WorkspaceID == workspaceID {
			n += r.BytesUploaded
		}
	}
	return n, nil
}
