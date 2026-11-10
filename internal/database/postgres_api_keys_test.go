//go:build postgres

// Real-PostgreSQL proof for the local API-key schema (issue #92).
//
// The three properties the store depends on are DDL: the blind index is
// UNIQUE (so two rows can never claim the same key), the user reference is a
// foreign key (so a key cannot exist for an account that does not), and the
// cascade removes a deleted account's keys. sqlite has no partial indexes and
// foreign keys are off by default there, so a sqlite test would prove nothing
// about the DDL a deployment actually gets.

package database

import (
	"context"
	"strings"
	"testing"
)

// TestAPIKeySchemaOnPostgres applies every migration to a fresh PostgreSQL and
// then exercises the api_keys constraints directly.
func TestAPIKeySchemaOnPostgres(t *testing.T) {
	dsn := newTestDatabase(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("HAAKU_MIGRATION_BACKUP_DIR", t.TempDir())
	g := openGorm(t, dsn)
	SetDBForTest(t, g)

	if err := Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if !relationExists(t, g, "api_keys") {
		t.Fatal(`table "api_keys" missing after migrations`)
	}
	if !relationExists(t, g, "idx_api_keys_user_id") {
		t.Error(`index "idx_api_keys_user_id" missing after migrations`)
	}

	if err := g.Exec(`INSERT INTO users (id, username, email) VALUES ('local_k1', 'k1', 'k1@example.test')`).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	insertKey := func(id, userID, hash string) error {
		return g.Exec(
			`INSERT INTO api_keys (id, user_id, prefix, key_hash, name) VALUES (?, ?, 'tk_abcdef', ?, 'k')`,
			id, userID, hash).Error
	}

	if err := insertKey("key_1", "local_k1", "hash_one"); err != nil {
		t.Fatalf("first key: %v", err)
	}
	// The blind index is unique: a second row cannot claim the same key hash.
	err := insertKey("key_2", "local_k1", "hash_one")
	if err == nil {
		t.Fatal("the unique index accepted two rows with the same key_hash")
	}
	if !strings.Contains(err.Error(), "duplicate key") && !strings.Contains(err.Error(), "api_keys_key_hash") {
		t.Fatalf("duplicate key_hash failed for the wrong reason: %v", err)
	}
	// A key cannot exist for an account that does not.
	if err := insertKey("key_3", "local_missing", "hash_three"); err == nil {
		t.Fatal("a key row was created for a user that does not exist")
	}

	// Deleting the account takes its keys with it.
	if err := g.Exec(`DELETE FROM users WHERE id = 'local_k1'`).Error; err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var remaining int64
	if err := g.Table("api_keys").Where("id = ?", "key_1").Count(&remaining).Error; err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("deleting a user left %d api_keys row(s) behind", remaining)
	}
}
