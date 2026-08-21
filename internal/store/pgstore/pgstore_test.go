package pgstore_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/KinonNeko/openorder/internal/store"
	"github.com/KinonNeko/openorder/internal/store/pgstore"
	"github.com/KinonNeko/openorder/internal/store/storetest"
)

// pgstore runs the same suite as memstore: the two are meant to be
// interchangeable, and only a shared suite keeps that honest.
//
//	OO_TEST_POSTGRES_DSN='postgres://…?sslmode=disable' go test ./internal/store/pgstore
//
// Without the DSN this skips, so `go test ./...` stays green without a database.
func TestPgstoreConformance(t *testing.T) {
	dsn := os.Getenv("OO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OO_TEST_POSTGRES_DSN not set; skipping pgstore conformance test")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		t.Helper()
		ctx := context.Background()
		pg, err := pgstore.Open(ctx, dsn)
		if err != nil {
			t.Fatalf("open postgres: %v", err)
		}
		t.Cleanup(func() { pg.Close() })
		truncateAll(t, dsn)
		return pg
	})
}

// truncateAll gives each subtest an empty database. It runs after Open so the
// schema exists, and cascades because messages and members reference the rest.
func truncateAll(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open for truncate: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`TRUNCATE messages, guild_members, channels, guilds, tokens, users CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
