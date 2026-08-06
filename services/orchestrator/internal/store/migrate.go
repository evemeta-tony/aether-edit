// services/orchestrator/internal/store/migrate.go
//
// Embedded SQL migration runner over pgx. golang-migrate was the planned
// tool but its module graph carries hashicorp/go-multierror and errwrap
// (MPL-2.0), which are outside the project license allowlist
// (MIT/BSD/Apache-2.0/ISC, rule S7), so migrations run through this small
// complete runner instead: versioned .up.sql files embedded in the binary,
// applied in order inside transactions, tracked in an
// orchestrator_schema_migrations table, serialized across concurrent
// starters with a Postgres advisory lock.
package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// advisoryLockKey serializes migration runs cluster-wide for this service.
// The value is arbitrary but fixed; it must not collide with other services
// on a shared cluster, so it embeds the service identity.
const advisoryLockKey int64 = 0x61657468_66743301 // "aeth" + ft3 + v1

// migrationFile is one ordered migration.
type migrationFile struct {
	Version int64
	Name    string
	SQL     string
}

// loadMigrations reads and orders the embedded *.up.sql files.
func loadMigrations() ([]migrationFile, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []migrationFile
	seen := map[int64]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: name must be <version>_<label>.up.sql", name)
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %s: version prefix is not numeric", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("duplicate migration version %d (%s and %s)", v, prev, name)
		}
		seen[v] = name
		data, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out = append(out, migrationFile{Version: v, Name: name, SQL: string(data)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no embedded migrations found")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies all pending embedded migrations to the database at
// databaseURL.
func Migrate(ctx context.Context, databaseURL string) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate connect: %w", err)
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("migrate advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS orchestrator_schema_migrations (
			version    bigint PRIMARY KEY,
			name       text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("migrate bookkeeping table: %w", err)
	}

	applied := map[int64]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM orchestrator_schema_migrations")
	if err != nil {
		return fmt.Errorf("migrate read versions: %w", err)
	}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migration %s begin: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s apply: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO orchestrator_schema_migrations (version, name) VALUES ($1, $2)",
			m.Version, m.Name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s record: %w", m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration %s commit: %w", m.Name, err)
		}
	}
	return nil
}
