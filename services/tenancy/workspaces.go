// services/tenancy/workspaces.go

package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type workspaceView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PlanTier  string    `json:"planTier"`
	CreatedAt time.Time `json:"createdAt"`
	Role      string    `json:"role,omitempty"`
	Active    bool      `json:"active"`
}

// handleMe returns the authenticated user's identity and active
// workspace: the UserMenu identity block (panel map U1).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	user, err := s.store.GetUser(r.Context(), id.UserID)
	if err != nil {
		s.internalError(w, r, "me lookup", err)
		return
	}
	out := map[string]any{
		"user": userView{ID: user.ID, Email: user.Email, Name: user.Name},
	}
	if m, err := s.store.GetMembership(r.Context(), id.WorkspaceID, id.UserID); err == nil {
		if ws, err := s.store.GetWorkspace(r.Context(), id.WorkspaceID); err == nil {
			out["workspace"] = workspaceView{
				ID: ws.ID, Name: ws.Name, PlanTier: ws.PlanTier,
				CreatedAt: ws.CreatedAt, Role: m.Role, Active: true,
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type createWorkspaceRequest struct {
	Name string `json:"name"`
}

// handleCreateWorkspace creates a workspace on the default plan tier
// with the caller as owner.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req createWorkspaceRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_name", "name is required and at most 128 characters")
		return
	}
	ws := Workspace{
		ID:        uuid.NewString(),
		Name:      req.Name,
		PlanTier:  s.tiers.DefaultTier,
		CreatedBy: id.UserID,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateWorkspace(r.Context(), ws, id.UserID); err != nil {
		s.internalError(w, r, "workspace create", err)
		return
	}
	s.log.Info("workspace created", "workspaceId", ws.ID, "by", id.UserID)
	writeJSON(w, http.StatusCreated, workspaceView{
		ID: ws.ID, Name: ws.Name, PlanTier: ws.PlanTier,
		CreatedAt: ws.CreatedAt, Role: RoleOwner, Active: false,
	})
}

// handleListWorkspaces lists the caller's workspaces with roles and
// the active flag: the UserMenu workspace switcher list.
func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	user, err := s.store.GetUser(r.Context(), id.UserID)
	if err != nil {
		s.internalError(w, r, "user lookup", err)
		return
	}
	list, err := s.store.ListWorkspacesForUser(r.Context(), id.UserID)
	if err != nil {
		s.internalError(w, r, "workspace list", err)
		return
	}
	views := make([]workspaceView, 0, len(list))
	for _, wr := range list {
		views = append(views, workspaceView{
			ID: wr.ID, Name: wr.Name, PlanTier: wr.PlanTier,
			CreatedAt: wr.CreatedAt, Role: wr.Role,
			Active: wr.ID == user.ActiveWorkspaceID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": views})
}

// handleActiveWorkspace returns the workspace the caller's token is
// scoped to.
func (s *Server) handleActiveWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetMembership(r.Context(), id.WorkspaceID, id.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusForbidden, "not_a_member", "caller is no longer a member of the token workspace")
			return
		}
		s.internalError(w, r, "membership lookup", err)
		return
	}
	ws, err := s.store.GetWorkspace(r.Context(), id.WorkspaceID)
	if err != nil {
		s.internalError(w, r, "workspace lookup", err)
		return
	}
	writeJSON(w, http.StatusOK, workspaceView{
		ID: ws.ID, Name: ws.Name, PlanTier: ws.PlanTier,
		CreatedAt: ws.CreatedAt, Role: m.Role, Active: true,
	})
}

type switchWorkspaceRequest struct {
	WorkspaceID string `json:"workspaceId"`
}

// handleSwitchWorkspace changes the caller's active workspace and
// mints a fresh access token scoped to it. Because workspaceId is a
// token claim, switching changes real scoping on every API that
// verifies the shared claims contract (FT-2, FT-3, FT-4).
func (s *Server) handleSwitchWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req switchWorkspaceRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_workspace_id", "workspaceId is required")
		return
	}
	m, err := s.store.GetMembership(r.Context(), req.WorkspaceID, id.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusForbidden, "not_a_member", "caller is not a member of that workspace")
			return
		}
		s.internalError(w, r, "membership lookup", err)
		return
	}
	if err := s.store.SetActiveWorkspace(r.Context(), id.UserID, req.WorkspaceID); err != nil {
		s.internalError(w, r, "active workspace set", err)
		return
	}
	access, err := s.signer.Mint(id.UserID, req.WorkspaceID, s.now(), s.accessTokenTTL)
	if err != nil {
		s.internalError(w, r, "switch mint", err)
		return
	}
	ws, err := s.store.GetWorkspace(r.Context(), req.WorkspaceID)
	if err != nil {
		s.internalError(w, r, "workspace lookup", err)
		return
	}
	s.log.Info("workspace switched", "userId", id.UserID, "workspaceId", req.WorkspaceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken": access,
		"tokenType":   "Bearer",
		"expiresIn":   int64(s.accessTokenTTL.Seconds()),
		"workspace": workspaceView{
			ID: ws.ID, Name: ws.Name, PlanTier: ws.PlanTier,
			CreatedAt: ws.CreatedAt, Role: m.Role, Active: true,
		},
	})
}

type memberView struct {
	UserID    string    `json:"userId"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// handleListMembers lists members of a workspace the caller belongs to.
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	wsID := r.PathValue("id")
	if !s.requireRole(w, r, wsID, id.UserID, RoleMember) {
		return
	}
	members, err := s.store.ListMembers(r.Context(), wsID)
	if err != nil {
		s.internalError(w, r, "member list", err)
		return
	}
	views := make([]memberView, 0, len(members))
	for _, m := range members {
		views = append(views, memberView{
			UserID: m.UserID, Email: m.Email, Name: m.Name,
			Role: m.Role, CreatedAt: m.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": views})
}

type addMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// handleAddMember adds an existing user (they must have signed in at
// least once; invitations are out of scope, see README) to the
// workspace. Admins may add members; granting admin or owner requires
// the owner role.
func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	wsID := r.PathValue("id")
	var req addMemberRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "invalid_email", "a valid email is required")
		return
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "invalid_role", "role must be owner, admin, or member")
		return
	}
	minRole := RoleAdmin
	if req.Role != RoleMember {
		minRole = RoleOwner
	}
	if !s.requireRole(w, r, wsID, id.UserID, minRole) {
		return
	}
	target, err := s.store.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "user_not_found", "no user with that email has signed in yet")
			return
		}
		s.internalError(w, r, "member user lookup", err)
		return
	}
	m := Membership{WorkspaceID: wsID, UserID: target.ID, Role: req.Role, CreatedAt: s.now()}
	if err := s.store.AddMember(r.Context(), m); err != nil {
		if errors.Is(err, ErrDuplicate) {
			writeError(w, http.StatusConflict, "already_member", "user is already a member of this workspace")
			return
		}
		s.internalError(w, r, "member add", err)
		return
	}
	s.log.Info("member added", "workspaceId", wsID, "userId", target.ID, "role", req.Role, "by", id.UserID)
	writeJSON(w, http.StatusCreated, memberView{
		UserID: target.ID, Email: target.Email, Name: target.Name,
		Role: m.Role, CreatedAt: m.CreatedAt,
	})
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

// handleUpdateMemberRole changes a member's role. Owner only. The last
// owner cannot be demoted.
func (s *Server) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	wsID := r.PathValue("id")
	targetID := r.PathValue("userId")
	if !s.requireRole(w, r, wsID, id.UserID, RoleOwner) {
		return
	}
	var req updateMemberRoleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "invalid_role", "role must be owner, admin, or member")
		return
	}
	current, err := s.store.GetMembership(r.Context(), wsID, targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "member_not_found", "no such member in this workspace")
			return
		}
		s.internalError(w, r, "member lookup", err)
		return
	}
	if current.Role == RoleOwner && req.Role != RoleOwner {
		owners, err := s.store.CountOwners(r.Context(), wsID)
		if err != nil {
			s.internalError(w, r, "owner count", err)
			return
		}
		if owners <= 1 {
			writeError(w, http.StatusConflict, "last_owner", "cannot demote the last owner")
			return
		}
	}
	if err := s.store.UpdateMemberRole(r.Context(), wsID, targetID, req.Role); err != nil {
		s.internalError(w, r, "member role update", err)
		return
	}
	s.log.Info("member role updated", "workspaceId", wsID, "userId", targetID, "role", req.Role, "by", id.UserID)
	writeJSON(w, http.StatusOK, map[string]string{"userId": targetID, "role": req.Role})
}

// handleRemoveMember removes a member. Admins may remove members;
// removing an admin or owner requires the owner role; anyone may
// remove themselves (leave). The last owner cannot be removed.
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	wsID := r.PathValue("id")
	targetID := r.PathValue("userId")
	current, err := s.store.GetMembership(r.Context(), wsID, targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "member_not_found", "no such member in this workspace")
			return
		}
		s.internalError(w, r, "member lookup", err)
		return
	}
	if targetID != id.UserID {
		minRole := RoleAdmin
		if current.Role != RoleMember {
			minRole = RoleOwner
		}
		if !s.requireRole(w, r, wsID, id.UserID, minRole) {
			return
		}
	} else if !s.requireRole(w, r, wsID, id.UserID, RoleMember) {
		return
	}
	if current.Role == RoleOwner {
		owners, err := s.store.CountOwners(r.Context(), wsID)
		if err != nil {
			s.internalError(w, r, "owner count", err)
			return
		}
		if owners <= 1 {
			writeError(w, http.StatusConflict, "last_owner", "cannot remove the last owner")
			return
		}
	}
	if err := s.store.RemoveMember(r.Context(), wsID, targetID); err != nil {
		s.internalError(w, r, "member remove", err)
		return
	}
	s.log.Info("member removed", "workspaceId", wsID, "userId", targetID, "by", id.UserID)
	w.WriteHeader(http.StatusNoContent)
}
