// services/orchestrator/internal/events/events_test.go
package events

import (
	"strings"
	"testing"
)

const validLanded = `{
	"uploadId": "0190e3a0-1111-7abc-8def-0123456789ab",
	"workspaceId": "ws1",
	"userId": "user-7",
	"objectKey": "assets/ws1/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"sizeBytes": 714000000,
	"mime": "video/mp4",
	"landedAt": "2026-08-06T10:00:00Z"
}`

func TestParseUploadLandedValid(t *testing.T) {
	ev, err := ParseUploadLanded([]byte(validLanded))
	if err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if ev.WorkspaceID != "ws1" || ev.SizeBytes != 714000000 {
		t.Errorf("fields wrong: %+v", ev)
	}
}

func TestParseUploadLandedRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(string) string
		wantSub string
	}{
		{"unknown field", func(s string) string {
			return strings.Replace(s, `"mime"`, `"surprise": 1, "mime"`, 1)
		}, "decode"},
		{"bad uuid", func(s string) string {
			return strings.Replace(s, "0190e3a0-1111-7abc-8def-0123456789ab", "not-a-uuid", 1)
		}, "uuid"},
		{"workspace mismatch", func(s string) string {
			return strings.Replace(s, `"workspaceId": "ws1"`, `"workspaceId": "ws2"`, 1)
		}, "workspace"},
		{"sha mismatch", func(s string) string {
			return strings.Replace(s, `"sha256": "aaaa`, `"sha256": "bbbb`, 1)
		}, "sha256"},
		{"bad key shape", func(s string) string {
			return strings.Replace(s, "assets/ws1/sha256/", "assets/ws1/md5/", 1)
		}, "objectKey"},
		{"zero size", func(s string) string {
			return strings.Replace(s, "714000000", "0", 1)
		}, "sizeBytes"},
		{"empty mime", func(s string) string {
			return strings.Replace(s, "video/mp4", "", 1)
		}, "mime"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseUploadLanded([]byte(tc.mutate(validLanded)))
			if err == nil {
				t.Fatal("expected rejection, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestParseUploadLandedRejectsTraversalKey(t *testing.T) {
	bad := strings.Replace(validLanded, "assets/ws1/sha256/", "assets/../sha256/", 1)
	if _, err := ParseUploadLanded([]byte(bad)); err == nil {
		t.Fatal("traversal-shaped key accepted")
	}
}
