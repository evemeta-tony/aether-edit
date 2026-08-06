// services/tenancy/workspaces_test.go

package main

import (
	"net/http"
	"testing"
	"time"
)

// TestWorkspaceLifecycle covers create, list, active, and the
// switcher minting a token scoped to the new workspace.
func TestWorkspaceLifecycle(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("ws-sub", "ws@example.com", "WS")

	// Create a second workspace.
	created := env.do(http.MethodPost, "/v1/workspaces", sess.AccessToken, map[string]string{"name": "Studio"})
	wantStatus(t, created, http.StatusCreated)
	var ws workspaceView
	decodeBody(t, created, &ws)
	if ws.Role != RoleOwner || ws.PlanTier != "demo" {
		t.Fatalf("created workspace %+v", ws)
	}

	// List shows both, with the bootstrap one active.
	list := env.do(http.MethodGet, "/v1/workspaces", sess.AccessToken, nil)
	wantStatus(t, list, http.StatusOK)
	var listResp struct {
		Workspaces []workspaceView `json:"workspaces"`
	}
	decodeBody(t, list, &listResp)
	if len(listResp.Workspaces) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(listResp.Workspaces))
	}
	activeCount := 0
	for _, w := range listResp.Workspaces {
		if w.Active {
			activeCount++
			if w.ID != sess.ActiveWorkspaceID {
				t.Fatalf("active flag on %s, want %s", w.ID, sess.ActiveWorkspaceID)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("want exactly one active workspace, got %d", activeCount)
	}

	// Switch: new token is scoped to the new workspace.
	sw := env.do(http.MethodPost, "/v1/workspaces/switch", sess.AccessToken, map[string]string{"workspaceId": ws.ID})
	wantStatus(t, sw, http.StatusOK)
	var swResp struct {
		AccessToken string        `json:"accessToken"`
		Workspace   workspaceView `json:"workspace"`
	}
	decodeBody(t, sw, &swResp)
	id, err := env.signer.Verify(swResp.AccessToken, time.Now())
	if err != nil {
		t.Fatalf("verify switched token: %v", err)
	}
	if id.WorkspaceID != ws.ID {
		t.Fatalf("switched token scoped to %s, want %s", id.WorkspaceID, ws.ID)
	}

	// Active endpoint follows the token scope.
	act := env.do(http.MethodGet, "/v1/workspaces/active", swResp.AccessToken, nil)
	wantStatus(t, act, http.StatusOK)
	var active workspaceView
	decodeBody(t, act, &active)
	if active.ID != ws.ID {
		t.Fatalf("active %s, want %s", active.ID, ws.ID)
	}

	// Switching to a workspace the user is not a member of is refused.
	other := env.login("other-sub", "other@example.com", "Other")
	deny := env.do(http.MethodPost, "/v1/workspaces/switch", sess.AccessToken, map[string]string{"workspaceId": other.ActiveWorkspaceID})
	wantStatus(t, deny, http.StatusForbidden)
}

// TestMembershipRoles covers add, role change, removal, role
// enforcement, and last-owner protection.
func TestMembershipRoles(t *testing.T) {
	env := newTestEnv(t)
	owner := env.login("owner-sub", "owner@example.com", "Owner")
	member := env.login("member-sub", "member@example.com", "Member")
	wsID := owner.ActiveWorkspaceID

	// Owner adds the member.
	add := env.do(http.MethodPost, "/v1/workspaces/"+wsID+"/members", owner.AccessToken,
		map[string]string{"email": "member@example.com", "role": RoleMember})
	wantStatus(t, add, http.StatusCreated)
	add.Body.Close()

	// Duplicate add conflicts.
	dup := env.do(http.MethodPost, "/v1/workspaces/"+wsID+"/members", owner.AccessToken,
		map[string]string{"email": "member@example.com", "role": RoleMember})
	wantStatus(t, dup, http.StatusConflict)

	// Unknown email is a 404, not an invite.
	ghost := env.do(http.MethodPost, "/v1/workspaces/"+wsID+"/members", owner.AccessToken,
		map[string]string{"email": "ghost@example.com", "role": RoleMember})
	wantStatus(t, ghost, http.StatusNotFound)

	// The member switches into the shared workspace to act there.
	sw := env.do(http.MethodPost, "/v1/workspaces/switch", member.AccessToken, map[string]string{"workspaceId": wsID})
	wantStatus(t, sw, http.StatusOK)
	var swResp struct {
		AccessToken string `json:"accessToken"`
	}
	decodeBody(t, sw, &swResp)
	memberTok := swResp.AccessToken

	// A member cannot add members (admin required).
	mAdd := env.do(http.MethodPost, "/v1/workspaces/"+wsID+"/members", memberTok,
		map[string]string{"email": "owner@example.com", "role": RoleMember})
	wantStatus(t, mAdd, http.StatusForbidden)

	// A member cannot change roles (owner required).
	mRole := env.do(http.MethodPatch, "/v1/workspaces/"+wsID+"/members/"+owner.User.ID, memberTok,
		map[string]string{"role": RoleMember})
	wantStatus(t, mRole, http.StatusForbidden)

	// Owner promotes the member to admin.
	promote := env.do(http.MethodPatch, "/v1/workspaces/"+wsID+"/members/"+member.User.ID, owner.AccessToken,
		map[string]string{"role": RoleAdmin})
	wantStatus(t, promote, http.StatusOK)
	promote.Body.Close()

	// The last owner cannot demote themselves.
	demote := env.do(http.MethodPatch, "/v1/workspaces/"+wsID+"/members/"+owner.User.ID, owner.AccessToken,
		map[string]string{"role": RoleMember})
	wantStatus(t, demote, http.StatusConflict)

	// Nor be removed.
	rmOwner := env.do(http.MethodDelete, "/v1/workspaces/"+wsID+"/members/"+owner.User.ID, owner.AccessToken, nil)
	wantStatus(t, rmOwner, http.StatusConflict)

	// Member list is visible to members.
	ml := env.do(http.MethodGet, "/v1/workspaces/"+wsID+"/members", memberTok, nil)
	wantStatus(t, ml, http.StatusOK)
	var mlResp struct {
		Members []memberView `json:"members"`
	}
	decodeBody(t, ml, &mlResp)
	if len(mlResp.Members) != 2 {
		t.Fatalf("want 2 members, got %d", len(mlResp.Members))
	}

	// Owner removes the (now admin) member.
	rm := env.do(http.MethodDelete, "/v1/workspaces/"+wsID+"/members/"+member.User.ID, owner.AccessToken, nil)
	wantStatus(t, rm, http.StatusNoContent)
	rm.Body.Close()

	// The removed member's old token no longer grants active access.
	gone := env.do(http.MethodGet, "/v1/workspaces/active", memberTok, nil)
	wantStatus(t, gone, http.StatusForbidden)
}
