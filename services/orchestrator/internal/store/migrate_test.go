// services/orchestrator/internal/store/migrate_test.go
//
// Migration loading tests. Applying against a live Postgres is not run
// locally: the only reachable cluster is shared production infrastructure,
// and this project's guardrails forbid creating test databases there. The
// end-to-end migration run is part of the OVH box bring-up checklist.
package store

import (
	"strings"
	"testing"
)

func TestLoadMigrationsOrderedAndComplete(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations embedded")
	}
	last := int64(0)
	for _, m := range ms {
		if m.Version <= last {
			t.Errorf("migration %s out of order (version %d after %d)", m.Name, m.Version, last)
		}
		last = m.Version
		if strings.TrimSpace(m.SQL) == "" {
			t.Errorf("migration %s is empty", m.Name)
		}
	}
	if ms[0].Version != 1 || !strings.Contains(ms[0].SQL, "CREATE TABLE jobs") {
		t.Errorf("first migration unexpected: %s", ms[0].Name)
	}
}
