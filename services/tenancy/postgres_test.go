// services/tenancy/postgres_test.go

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// TestPostgresStore exercises the production store against a real
// database. It runs only when TENANCY_TEST_DATABASE_URL points at a
// disposable Postgres (the deploy box proof runs it before the
// service goes live); otherwise it skips so the suite stays green on
// dev machines without a cluster.
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TENANCY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TENANCY_TEST_DATABASE_URL not set; postgres store covered at deploy proof")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("connect+migrate: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user, err := store.UpsertUserByGoogleSub(ctx, "pg-sub-"+now.Format("150405.000"), "pg@example.com", "PG", now)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	ws := Workspace{ID: "pg-ws-" + now.Format("150405.000"), Name: "PG", PlanTier: "demo", CreatedBy: user.ID, CreatedAt: now}
	if err := store.CreateWorkspace(ctx, ws, user.ID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	m, err := store.GetMembership(ctx, ws.ID, user.ID)
	if err != nil || m.Role != RoleOwner {
		t.Fatalf("owner membership: %+v err=%v", m, err)
	}

	sec := 12.5
	ev := contracts.MeteringEvent{
		EventID: "pg-ev-" + now.Format("150405.000"), WorkspaceID: ws.ID,
		UserID: user.ID, Kind: contracts.MeteringJobCompleted, EncodeSeconds: &sec, At: now,
	}
	delta, err := rollupDelta(ev)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	applied, err := store.ApplyMetering(ctx, ev, delta.Month, delta)
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v", applied, err)
	}
	applied, err = store.ApplyMetering(ctx, ev, delta.Month, delta)
	if err != nil || applied {
		t.Fatalf("duplicate apply: applied=%v err=%v", applied, err)
	}
	r, err := store.GetRollup(ctx, ws.ID, delta.Month)
	if err != nil || r.EncodeSeconds != 12.5 {
		t.Fatalf("rollup: %+v err=%v", r, err)
	}
}
