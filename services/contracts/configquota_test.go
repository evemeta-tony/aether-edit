// services/contracts/configquota_test.go

package contracts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func mustLoad(t *testing.T, path string) *ConfigQuota {
	t.Helper()
	q, err := LoadConfigQuota(path)
	if err != nil {
		t.Fatalf("LoadConfigQuota(%s): %v", path, err)
	}
	return q
}

func TestConfigQuotaYAMLUploadDecisions(t *testing.T) {
	q := mustLoad(t, filepath.Join("testdata", "quota.yaml"))
	ctx := context.Background()

	cases := []struct {
		name        string
		workspace   string
		size        int64
		wantAllowed bool
		wantReason  string
	}{
		{"default allows under limit", "ws-anything", 1 << 20, true, ""},
		{"default denies over limit", "ws-anything", 2 << 30, false, ReasonUploadSizeExceeded},
		{"workspace override denies over its own limit", "ws-small", 4096, false, ReasonUploadSizeExceeded},
		{"workspace override allows under its own limit", "ws-small", 512, true, ""},
		{"uploads disabled flag", "ws-nouploads", 1, false, ReasonUploadsDisabled},
		{"zero limit means disabled", "ws-zero", 1, false, ReasonUploadsDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := q.CheckUploadSession(ctx, tc.workspace, tc.size)
			if err != nil {
				t.Fatalf("CheckUploadSession: %v", err)
			}
			if d.Allowed != tc.wantAllowed || d.Reason != tc.wantReason {
				t.Fatalf("got %+v, want allowed=%v reason=%q", d, tc.wantAllowed, tc.wantReason)
			}
		})
	}
}

func TestConfigQuotaYAMLJobDecisions(t *testing.T) {
	q := mustLoad(t, filepath.Join("testdata", "quota.yaml"))
	ctx := context.Background()

	d, err := q.CheckJobAdmission(ctx, "ws-nojobs")
	if err != nil {
		t.Fatalf("CheckJobAdmission: %v", err)
	}
	if d.Allowed || d.Reason != ReasonJobsDisabled {
		t.Fatalf("got %+v, want denied with %q", d, ReasonJobsDisabled)
	}

	d, err = q.CheckJobAdmission(ctx, "ws-anything")
	if err != nil {
		t.Fatalf("CheckJobAdmission: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("got %+v, want allowed", d)
	}
}

func TestConfigQuotaJSONAndUnknownWorkspacePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.json")
	body := `{
  "denyUnknownWorkspaces": true,
  "defaults": {"maxUploadBytes": 2048, "allowUploads": true, "allowJobs": true},
  "workspaces": {"ws-known": {"maxUploadBytes": 4096}}
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	q := mustLoad(t, path)
	ctx := context.Background()

	d, err := q.CheckUploadSession(ctx, "ws-unknown", 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || d.Reason != ReasonWorkspaceUnknown {
		t.Fatalf("unknown workspace: got %+v, want denied with %q", d, ReasonWorkspaceUnknown)
	}

	d, err = q.CheckJobAdmission(ctx, "ws-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || d.Reason != ReasonWorkspaceUnknown {
		t.Fatalf("unknown workspace job: got %+v, want denied with %q", d, ReasonWorkspaceUnknown)
	}

	d, err = q.CheckUploadSession(ctx, "ws-known", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Fatalf("known workspace under limit: got %+v, want allowed", d)
	}
	d, err = q.CheckUploadSession(ctx, "ws-known", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || d.Reason != ReasonUploadSizeExceeded {
		t.Fatalf("known workspace over limit: got %+v, want %q", d, ReasonUploadSizeExceeded)
	}
}

func TestConfigQuotaFailClosedByDefault(t *testing.T) {
	// Janus V-5: an empty or silent policy denies. Admission requires an
	// explicit allow after layering the workspace entry over defaults.
	q, err := NewConfigQuota(QuotaConfigFile{})
	if err != nil {
		t.Fatalf("NewConfigQuota(empty): %v", err)
	}
	ctx := context.Background()

	d, err := q.CheckUploadSession(ctx, "ws-any", 1)
	if err != nil {
		t.Fatalf("CheckUploadSession: %v", err)
	}
	if d.Allowed || d.Reason != ReasonUploadsDisabled {
		t.Fatalf("empty config upload: got %+v, want denied with %q", d, ReasonUploadsDisabled)
	}

	d, err = q.CheckJobAdmission(ctx, "ws-any")
	if err != nil {
		t.Fatalf("CheckJobAdmission: %v", err)
	}
	if d.Allowed || d.Reason != ReasonJobsDisabled {
		t.Fatalf("empty config job: got %+v, want denied with %q", d, ReasonJobsDisabled)
	}

	// A size limit alone is not an allow; the explicit flag is required.
	limit := int64(1 << 20)
	q, err = NewConfigQuota(QuotaConfigFile{Defaults: WorkspaceQuota{MaxUploadBytes: &limit}})
	if err != nil {
		t.Fatalf("NewConfigQuota(limit only): %v", err)
	}
	d, err = q.CheckUploadSession(ctx, "ws-any", 1)
	if err != nil {
		t.Fatalf("CheckUploadSession: %v", err)
	}
	if d.Allowed || d.Reason != ReasonUploadsDisabled {
		t.Fatalf("limit only upload: got %+v, want denied with %q", d, ReasonUploadsDisabled)
	}
}

func TestConfigQuotaRejectsBadConfig(t *testing.T) {
	dir := t.TempDir()

	unknownField := filepath.Join(dir, "unknown.yaml")
	if err := os.WriteFile(unknownField, []byte("defaults:\n  maxUploadByte: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigQuota(unknownField); err == nil {
		t.Fatal("want error for unknown field, got nil")
	}

	negative := filepath.Join(dir, "negative.json")
	if err := os.WriteFile(negative, []byte(`{"defaults":{"maxUploadBytes":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigQuota(negative); err == nil {
		t.Fatal("want error for negative limit, got nil")
	}

	badExt := filepath.Join(dir, "quota.toml")
	if err := os.WriteFile(badExt, []byte("x = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigQuota(badExt); err == nil {
		t.Fatal("want error for unsupported extension, got nil")
	}
}
