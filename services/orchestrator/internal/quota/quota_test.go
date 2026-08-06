// services/orchestrator/internal/quota/quota_test.go
package quota

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newChecker(t *testing.T, active map[string]int) *Checker {
	t.Helper()
	c, err := NewFromFile(filepath.Join("testdata", "quota.json"), func(_ context.Context, ws string) (int, error) {
		return active[ws], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestJobAdmissionDeniedOverDefaultLimit(t *testing.T) {
	c := newChecker(t, map[string]int{"ws-basic": 3})
	d, err := c.CheckJobAdmission(context.Background(), "ws-basic")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("admission must be denied at the limit")
	}
	if !strings.HasPrefix(d.Reason, "quota_exceeded:max_active_jobs") {
		t.Errorf("reason = %q, want typed quota_exceeded reason", d.Reason)
	}
}

func TestJobAdmissionAllowedUnderLimit(t *testing.T) {
	c := newChecker(t, map[string]int{"ws-basic": 2})
	d, err := c.CheckJobAdmission(context.Background(), "ws-basic")
	if err != nil || !d.Allowed {
		t.Fatalf("admission under limit must pass: %+v err=%v", d, err)
	}
}

func TestJobAdmissionWorkspaceOverride(t *testing.T) {
	c := newChecker(t, map[string]int{"ws-premium": 9, "ws-frozen": 1000})
	if d, _ := c.CheckJobAdmission(context.Background(), "ws-premium"); !d.Allowed {
		t.Errorf("premium at 9 of 10 must pass: %+v", d)
	}
	if d, _ := c.CheckJobAdmission(context.Background(), "ws-frozen"); !d.Allowed {
		t.Errorf("unlimited workspace must pass: %+v", d)
	}
}

func TestJobAdmissionCounterError(t *testing.T) {
	c, err := NewFromFile(filepath.Join("testdata", "quota.json"), func(context.Context, string) (int, error) {
		return 0, fmt.Errorf("db down")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CheckJobAdmission(context.Background(), "ws"); err == nil {
		t.Fatal("counter error must propagate, not silently allow")
	}
}

func TestUploadSessionCheck(t *testing.T) {
	c := newChecker(t, nil)
	if d, _ := c.CheckUploadSession(context.Background(), "ws-basic", 6<<30); d.Allowed {
		t.Error("oversized upload must be denied")
	}
	if d, _ := c.CheckUploadSession(context.Background(), "ws-basic", 1<<30); !d.Allowed {
		t.Error("in-limit upload must pass")
	}
	if d, _ := c.CheckUploadSession(context.Background(), "ws-premium", 100<<30); !d.Allowed {
		t.Error("unlimited workspace upload must pass")
	}
}

func TestConfigRejectsUnknownFieldsAndMissingDefaults(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"defaults":{"maxActiveJobs":1,"maxUploadBytes":1},"surprise":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromFile(bad, func(context.Context, string) (int, error) { return 0, nil }); err == nil {
		t.Error("unknown field must be rejected")
	}
	missing := filepath.Join(dir, "missing.json")
	if err := os.WriteFile(missing, []byte(`{"defaults":{"maxUploadBytes":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromFile(missing, func(context.Context, string) (int, error) { return 0, nil }); err == nil {
		t.Error("missing defaults.maxActiveJobs must be rejected")
	}
}
