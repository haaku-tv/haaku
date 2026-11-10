package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The local API-key store (issue #92). These tests are about the credential
// itself rather than the routes: what is persisted, what the blind index is
// keyed on, and which store the mode switch picks.

// localAPIKeyDB returns a migrated in-memory database in local mode with the
// caller seeded, installed as the process-wide handle.
func localAPIKeyDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	t.Setenv("HAAKU_SECRET_KEY", testRootKey)
	if err := InitAuthMode("", ""); err != nil {
		t.Fatalf("init auth mode: %v", err)
	}
	t.Cleanup(func() {
		if err := InitAuthMode("", ""); err != nil {
			t.Fatalf("restore auth mode: %v", err)
		}
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.User{}, &database.APIKey{}); err != nil {
		t.Fatalf("migrate test DB: %v", err)
	}
	database.SetDBForTest(t, db)

	userID := LocalIDPrefix + "apikeytest0000000000000000"
	if err := db.Create(&database.User{
		ID: userID, Username: "apikeytest", Email: "apikeytest@example.test",
		Status: database.UserStatusActive, Plan: "free",
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return db, userID
}

// TestLocalAPIKeyIsStoredAsAKeyedBlindIndex pins the three properties the at-rest
// protection rests on: the stored value is not the key, it is not a bare digest
// of the key, and it is keyed on the root key so a dump alone is useless.
func TestLocalAPIKeyIsStoredAsAKeyedBlindIndex(t *testing.T) {
	db, userID := localAPIKeyDB(t)

	rawKey, row, err := createLocalAPIKey(db, userID, "jellyfin")
	if err != nil {
		t.Fatalf("createLocalAPIKey: %v", err)
	}

	if row.KeyHash == rawKey {
		t.Fatal("the raw key is stored verbatim in key_hash")
	}
	bare := sha256.Sum256([]byte(rawKey))
	if row.KeyHash == hex.EncodeToString(bare[:]) {
		t.Fatal("key_hash is an unkeyed SHA-256 of the key, so a dump can be ground offline")
	}
	if !strings.HasPrefix(rawKey, apiKeyPrefix) {
		t.Fatalf("minted key %q does not carry the %q prefix", rawKey, apiKeyPrefix)
	}
	if row.Prefix != rawKey[:apiKeyDisplayLen] {
		t.Errorf("prefix = %q, want the key's first %d characters", row.Prefix, apiKeyDisplayLen)
	}

	// The same key hashes the same way, so it can be matched by equality...
	again, err := localAPIKeyHash(rawKey)
	if err != nil {
		t.Fatalf("localAPIKeyHash: %v", err)
	}
	if again != row.KeyHash {
		t.Fatal("the blind index is not deterministic, so no lookup could ever match")
	}
	// ...and it is not the session blind index of the same string: the two
	// purposes derive different keys from the same root.
	sessionStyle, err := localTokenHash(rawKey)
	if err != nil {
		t.Fatalf("localTokenHash: %v", err)
	}
	if sessionStyle == row.KeyHash {
		t.Fatal("the api-key and session blind indexes are the same value: the purpose labels are not domain-separating")
	}

	// A different root key gives a different index — this is what makes the
	// column useless without HAAKU_SECRET_KEY.
	t.Setenv("HAAKU_SECRET_KEY", strings.Repeat("other-test-key-", 4))
	other, err := localAPIKeyHash(rawKey)
	if err != nil {
		t.Fatalf("localAPIKeyHash under a rotated root key: %v", err)
	}
	if other == row.KeyHash {
		t.Fatal("the blind index does not depend on the root key")
	}
}

// TestLocalAPIKeyLookupRoundTrip covers create → resolve → revoke.
func TestLocalAPIKeyLookupRoundTrip(t *testing.T) {
	db, userID := localAPIKeyDB(t)

	rawKey, row, err := createLocalAPIKey(db, userID, "jellyfin")
	if err != nil {
		t.Fatalf("createLocalAPIKey: %v", err)
	}

	got, err := LookupUserByAPIKey(rawKey)
	if err != nil {
		t.Fatalf("LookupUserByAPIKey: %v", err)
	}
	if got != userID {
		t.Fatalf("LookupUserByAPIKey = %q, want %q", got, userID)
	}

	// The first lookup stamps last_used_at.
	var stored database.APIKey
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatalf("reload key row: %v", err)
	}
	if stored.LastUsedAt == nil {
		t.Error("last_used_at was not written by the lookup")
	}

	if err := revokeLocalAPIKey(db, userID, row.ID); err != nil {
		t.Fatalf("revokeLocalAPIKey: %v", err)
	}
	if _, err := LookupUserByAPIKey(rawKey); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("a revoked key still resolved: %v", err)
	}
	// Revoking twice is a not-found, not a silent success.
	if err := revokeLocalAPIKey(db, userID, row.ID); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("second revoke = %v, want ErrAPIKeyNotFound", err)
	}
}

// TestLocalAPIKeyLookupRequiresAnActiveAccount is the ban half of "the same
// door as sessions": a banned account's key must stop authenticating even
// though the key row is untouched.
func TestLocalAPIKeyLookupRequiresAnActiveAccount(t *testing.T) {
	db, userID := localAPIKeyDB(t)

	rawKey, _, err := createLocalAPIKey(db, userID, "jellyfin")
	if err != nil {
		t.Fatalf("createLocalAPIKey: %v", err)
	}
	if _, err := LookupUserByAPIKey(rawKey); err != nil {
		t.Fatalf("the key did not work before the ban: %v", err)
	}

	if err := db.Model(&database.User{}).Where("id = ?", userID).
		Update("status", database.UserStatusBanned).Error; err != nil {
		t.Fatalf("ban user: %v", err)
	}
	if _, err := LookupUserByAPIKey(rawKey); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("a banned account's key still resolved: %v", err)
	}
}

// TestLocalAPIKeyLookupIsUnusableWithoutTheRootKey pins the failure direction:
// no root key is an error, never a lookup that silently matches nothing or a
// pass-through.
func TestLocalAPIKeyLookupIsUnusableWithoutTheRootKey(t *testing.T) {
	db, userID := localAPIKeyDB(t)

	rawKey, _, err := createLocalAPIKey(db, userID, "jellyfin")
	if err != nil {
		t.Fatalf("createLocalAPIKey: %v", err)
	}

	t.Setenv("HAAKU_SECRET_KEY", "")
	if _, err := LookupUserByAPIKey(rawKey); err == nil {
		t.Fatal("a lookup succeeded with no root key set")
	}
}

// TestModeSwitchPicksTheStore is the "respected in both directions" half of the
// acceptance bar: local mode resolves from the local table without any Clerk
// configuration at all, and clerk mode does not authenticate a row it never
// wrote.
func TestModeSwitchPicksTheStore(t *testing.T) {
	db, userID := localAPIKeyDB(t)

	rawKey, _, err := createLocalAPIKey(db, userID, "jellyfin")
	if err != nil {
		t.Fatalf("createLocalAPIKey: %v", err)
	}

	t.Run("local mode needs no Clerk", func(t *testing.T) {
		t.Setenv("CLERK_API_BASE_URL", "")
		t.Setenv("CLERK_SECRET_KEY", "")
		if err := InitAuthMode("local", "approval"); err != nil {
			t.Fatalf("switch to local: %v", err)
		}
		if got, err := LookupUserByAPIKey(rawKey); err != nil || got != userID {
			t.Fatalf("local lookup = (%q, %v), want (%q, nil)", got, err, userID)
		}
	})

	t.Run("clerk mode ignores the local store", func(t *testing.T) {
		// A Clerk that knows about no keys at all: if the local row leaked into
		// this path, the lookup would succeed.
		clerk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v1/users" {
				_, _ = w.Write([]byte("[]"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"message":"not found"}]}`))
		}))
		defer clerk.Close()

		t.Setenv("CLERK_API_BASE_URL", clerk.URL)
		t.Setenv("CLERK_SECRET_KEY", strings.Repeat("sk-test-", 6))
		if err := InitAuthMode("clerk", "approval"); err != nil {
			t.Fatalf("switch to clerk: %v", err)
		}
		if got, err := LookupUserByAPIKey(rawKey); err == nil {
			t.Fatalf("a locally stored key authenticated in clerk mode as %q", got)
		}
	})
}
