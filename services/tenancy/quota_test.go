// services/tenancy/quota_test.go

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
	"github.com/evemeta-tony/aether-edit/services/tenancy/quotaclient"
)

// quotaFixture seeds a workspace on the demo tier with a controlled
// clock and returns the checker.
func quotaFixture(t *testing.T, tier string) (*memStore, *MeteredQuota, string) {
	t.Helper()
	store := newMemStore()
	tiers := testTiers(t)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	user, err := store.UpsertUserByGoogleSub(context.Background(), "q-sub", "q@example.com", "Q", now)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	ws := Workspace{ID: "ws-quota", Name: "Q", PlanTier: tier, CreatedBy: user.ID, CreatedAt: now}
	if err := store.CreateWorkspace(context.Background(), ws, user.ID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	q := NewMeteredQuota(store, tiers)
	q.now = func() time.Time { return now }
	return store, q, ws.ID
}

// seedEncodeSeconds pushes encode usage into the current month.
func seedEncodeSeconds(t *testing.T, store *memStore, wsID string, seconds float64, at time.Time) {
	t.Helper()
	ev := mkEvent("seed-"+at.String(), wsID, contracts.MeteringJobCompleted, nil, &seconds, "j", at)
	delta, err := rollupDelta(ev)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if _, err := store.ApplyMetering(context.Background(), ev, delta.Month, delta); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// TestMeteredQuotaUploadChecks covers allow, per-session size cap,
// storage exhaustion, unknown workspace, and the disabled gate.
func TestMeteredQuotaUploadChecks(t *testing.T) {
	ctx := context.Background()
	store, q, wsID := quotaFixture(t, "demo")

	// Within limits: allowed.
	d, err := q.CheckUploadSession(ctx, wsID, 1<<10)
	if err != nil || !d.Allowed {
		t.Fatalf("allow: %+v err=%v", d, err)
	}

	// Session over the tier's per-upload cap (256 KiB).
	d, err = q.CheckUploadSession(ctx, wsID, (1<<18)+1)
	if err != nil || d.Allowed || d.Reason != contracts.ReasonUploadSizeExceeded {
		t.Fatalf("size cap: %+v err=%v", d, err)
	}

	// Storage cap (1 MiB): fill nearly full via rollups; the next
	// session that would cross the cap is denied with the typed
	// reason.
	ev := mkEvent("fill", wsID, contracts.MeteringUploadCompleted, i64((1<<20)-100), nil, "", q.now())
	delta, err := rollupDelta(ev)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if _, err := store.ApplyMetering(ctx, ev, delta.Month, delta); err != nil {
		t.Fatalf("apply: %v", err)
	}
	d, err = q.CheckUploadSession(ctx, wsID, 200)
	if err != nil || d.Allowed || d.Reason != ReasonStorageExceeded {
		t.Fatalf("storage cap: %+v err=%v", d, err)
	}
	// Exactly at the cap still fits.
	d, err = q.CheckUploadSession(ctx, wsID, 100)
	if err != nil || !d.Allowed {
		t.Fatalf("storage fit: %+v err=%v", d, err)
	}

	// Unknown workspace.
	d, err = q.CheckUploadSession(ctx, "no-such-ws", 1)
	if err != nil || d.Allowed || d.Reason != contracts.ReasonWorkspaceUnknown {
		t.Fatalf("unknown ws: %+v err=%v", d, err)
	}

	// Negative size hint is a caller error, not a decision.
	if _, err := q.CheckUploadSession(ctx, wsID, -1); err == nil {
		t.Fatal("negative size hint accepted")
	}

	// Tier with uploads disabled.
	_, q2, ws2 := quotaFixture(t, "locked")
	d, err = q2.CheckUploadSession(ctx, ws2, 1)
	if err != nil || d.Allowed || d.Reason != contracts.ReasonUploadsDisabled {
		t.Fatalf("disabled: %+v err=%v", d, err)
	}

	// Workspace on an undefined tier is denied, typed.
	_, q3, ws3 := quotaFixture(t, "ghost-tier")
	d, err = q3.CheckUploadSession(ctx, ws3, 1)
	if err != nil || d.Allowed || d.Reason != ReasonTierUnknown {
		t.Fatalf("ghost tier: %+v err=%v", d, err)
	}
}

// TestMeteredQuotaJobChecks covers the monthly encode-hours math: the
// current month's rollup gates admission and prior months do not
// count.
func TestMeteredQuotaJobChecks(t *testing.T) {
	ctx := context.Background()
	store, q, wsID := quotaFixture(t, "demo")
	now := q.now()

	// Fresh month: allowed.
	d, err := q.CheckJobAdmission(ctx, wsID)
	if err != nil || !d.Allowed {
		t.Fatalf("allow: %+v err=%v", d, err)
	}

	// Last month's heavy usage does not gate this month.
	seedEncodeSeconds(t, store, wsID, 7200, now.AddDate(0, -1, 0))
	d, err = q.CheckJobAdmission(ctx, wsID)
	if err != nil || !d.Allowed {
		t.Fatalf("prior month gated: %+v err=%v", d, err)
	}

	// Just under the 2-hour tier limit: still allowed.
	seedEncodeSeconds(t, store, wsID, 7199, now)
	d, err = q.CheckJobAdmission(ctx, wsID)
	if err != nil || !d.Allowed {
		t.Fatalf("under limit: %+v err=%v", d, err)
	}

	// Crossing the limit denies with the typed reason.
	seedEncodeSeconds(t, store, wsID, 1, now.Add(time.Minute))
	d, err = q.CheckJobAdmission(ctx, wsID)
	if err != nil || d.Allowed || d.Reason != ReasonEncodeHoursExhausted {
		t.Fatalf("over limit: %+v err=%v", d, err)
	}

	// Jobs disabled tier.
	_, q2, ws2 := quotaFixture(t, "locked")
	d, err = q2.CheckJobAdmission(ctx, ws2)
	if err != nil || d.Allowed || d.Reason != contracts.ReasonJobsDisabled {
		t.Fatalf("disabled: %+v err=%v", d, err)
	}
}

// TestQuotaHTTPAndClient exercises the internal HTTP quota API through
// the quotaclient package (the deploy-time QuotaChecker for FT-2 and
// FT-3), including auth on the internal surface and fail-closed
// error behavior.
func TestQuotaHTTPAndClient(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("qc-sub", "qc@example.com", "QC")
	ctx := context.Background()

	client, err := quotaclient.New(env.http.URL, testInternalToken, 5*time.Second)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// Allowed upload within the demo tier.
	d, err := client.CheckUploadSession(ctx, sess.ActiveWorkspaceID, 1<<10)
	if err != nil || !d.Allowed {
		t.Fatalf("upload allow: %+v err=%v", d, err)
	}
	// Typed denial crosses the HTTP boundary intact.
	d, err = client.CheckUploadSession(ctx, sess.ActiveWorkspaceID, (1<<18)+1)
	if err != nil || d.Allowed || d.Reason != contracts.ReasonUploadSizeExceeded {
		t.Fatalf("upload deny: %+v err=%v", d, err)
	}
	d, err = client.CheckJobAdmission(ctx, sess.ActiveWorkspaceID)
	if err != nil || !d.Allowed {
		t.Fatalf("job allow: %+v err=%v", d, err)
	}
	d, err = client.CheckUploadSession(ctx, "no-such-ws", 1)
	if err != nil || d.Allowed || d.Reason != contracts.ReasonWorkspaceUnknown {
		t.Fatalf("unknown ws: %+v err=%v", d, err)
	}

	// Wrong internal token: the client surfaces an error, and the
	// V-5 fail-closed posture at FT-2/FT-3 call sites denies.
	badClient, err := quotaclient.New(env.http.URL, "wrong-token-wrong-token", 5*time.Second)
	if err != nil {
		t.Fatalf("bad client: %v", err)
	}
	if _, err := badClient.CheckJobAdmission(ctx, sess.ActiveWorkspaceID); err == nil {
		t.Fatal("wrong internal token did not error")
	}

	// Unreachable service: error, never a silent allow.
	downClient, err := quotaclient.New("http://127.0.0.1:1", testInternalToken, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("down client: %v", err)
	}
	if _, err := downClient.CheckJobAdmission(ctx, sess.ActiveWorkspaceID); err == nil {
		t.Fatal("unreachable service did not error")
	}

	// Direct HTTP checks: bad request shapes are rejected.
	resp := env.doInternal(t, "/internal/v1/quota/check-upload-session", `{"workspaceId":"","sizeHintBytes":1}`)
	wantStatus(t, resp, http.StatusBadRequest)
	resp = env.doInternal(t, "/internal/v1/quota/check-upload-session", `{"workspaceId":"w","sizeHintBytes":-5}`)
	wantStatus(t, resp, http.StatusBadRequest)
	resp = env.doInternal(t, "/internal/v1/quota/check-upload-session", `{"workspaceId":"w","unknown":true}`)
	wantStatus(t, resp, http.StatusBadRequest)
}

// TestTierConfigValidation pins the tier file validation rules.
func TestTierConfigValidation(t *testing.T) {
	bad := []TierConfig{
		{},
		{DefaultTier: "demo"},
		{DefaultTier: "missing", Tiers: map[string]Tier{"demo": {}}},
		{DefaultTier: "demo", Tiers: map[string]Tier{"demo": {EncodeHoursPerMonth: -1}}},
		{DefaultTier: "demo", Tiers: map[string]Tier{"demo": {StorageBytes: -1}}},
	}
	for i, cfg := range bad {
		if err := cfg.validate(); err == nil {
			t.Fatalf("case %d: invalid tier config accepted", i)
		}
	}
	good := TierConfig{DefaultTier: "demo", Tiers: map[string]Tier{"demo": {EncodeHoursPerMonth: 1, AllowUploads: true, AllowJobs: true}}}
	if err := good.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
