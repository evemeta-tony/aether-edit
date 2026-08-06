// services/tenancy/migrate.go

// Embedded SQL migration runner over pgx, following the FT-3 lane's
// precedent: golang-migrate's FILE LAYOUT (NNNNNN_label.up.sql /
// .down.sql) with a small in-binary runner, because golang-migrate's
// module graph carries MPL-2.0 dependencies outside the project
// license allowlist. Up migrations apply in order inside transactions,
// tracked in tenancy_schema_migrations, serialized by a Postgres
// advisory lock so concurrent starters on the shared cluster do not
// race.
package main

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

// advisoryLockKey serializes migration runs cluster-wide for this
// service. Distinct from the FT-3 orchestrator key.
const advisoryLockKey int64 = 0x61657468_66743661 // "aeth" + ft6a

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
			return nil, fmt.Errorf("migration %s: duplicate version with %s", name, prev)
		}
		seen[v] = name
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out = append(out, migrationFile{Version: v, Name: name, SQL: string(raw)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no migrations embedded")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// runMigrations applies pending up migrations on conn.
func runMigrations(ctx context.Context, conn *pgx.Conn) error {
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS tenancy_schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrations table: %w", err)
	}
	applied := map[int64]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM tenancy_schema_migrations")
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("applied migrations: %w", err)
	}
	for _, m := range migs {
		if applied[m.Version] {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO tenancy_schema_migrations (version, name) VALUES ($1, $2)",
			m.Version, m.Name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.Name, err)
		}
	}
	return nil
}
