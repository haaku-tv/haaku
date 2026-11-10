package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/secret"

	"gorm.io/gorm"
)

// Local API keys (issue #92).
//
// In clerk mode a `tk_` credential lives in Clerk's `unsafe_metadata.api_keys`
// and is only ever reached through the Clerk Backend API. A self-hosted
// instance has no Clerk tenant, so with the Clerk path alone an instance in the
// primary deployment shape could not mint a credential for the Jellyfin plugin
// (or anything else that authenticates with a key rather than a session). This
// file is the local half: the same key format, issued and resolved against
// `api_keys`.
//
// The raw key is never stored. What the row carries is an HMAC-SHA256 blind
// index under a key HKDF-derived from the same HAAKU_SECRET_KEY root that
// protects sessions and at-rest ciphertext, under its own versioned purpose
// label — so the two derived keys can never be cross-used, and a database dump
// without the root key is neither a set of usable credentials nor a set of
// hashes an attacker can grind offline against candidate keys.
//
// There is deliberately no Redis cache on this path. The lookup is one
// equality query on a unique index, the prefix it would have to be cached under
// is truncated (so two keys can share it) and a cache would make a revocation
// lag by its TTL — the opposite of what a revoke endpoint promises.

// apiKeyTokenPurpose domain-separates the api-key blind-index key from the
// session blind index and the at-rest data-encryption key derived from the same
// root. Never reuse it.
const apiKeyTokenPurpose = "haaku/api-key-token/v1"

// apiKeyDisplayLen is how much of the raw key the stored prefix keeps:
// "tk_" plus the first six hex characters.
const apiKeyDisplayLen = 10

// apiKeyTouchInterval throttles the last_used_at write. The authentication path
// runs on every request and a row write per request is not worth a display
// column; once every few minutes is enough for a settings page.
const apiKeyTouchInterval = 5 * time.Minute

// ErrAPIKeyNotFound is the single error for "no active key matches": unknown,
// revoked, or owned by an account that is no longer active. Callers must not
// distinguish them to the client.
var ErrAPIKeyNotFound = errors.New("auth: api key not found")

// localAPIKeyHash returns the hex HMAC-SHA256 blind index of a raw key under
// the HKDF-derived lookup key. It is exported to nobody: every caller in this
// package derives it the same way.
func localAPIKeyHash(key string) (string, error) {
	root, err := secret.RootKeyFromEnv()
	if err != nil {
		return "", err
	}
	lookupKey, err := secret.DeriveKey(root, apiKeyTokenPurpose, 32)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, lookupKey)
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// createLocalAPIKey mints a `tk_` key for userID and stores only its blind
// index. The raw key is the return value and exists nowhere else — there is no
// read path that can reproduce it.
func createLocalAPIKey(db *gorm.DB, userID, name string) (string, *database.APIKey, error) {
	if db == nil {
		return "", nil, errors.New("auth: no database handle")
	}
	if userID == "" {
		return "", nil, errors.New("auth: no user id")
	}

	randomBytes, err := generateAPIKeyBytes()
	if err != nil {
		return "", nil, fmt.Errorf("auth: generate api key: %w", err)
	}
	fullKey := apiKeyPrefix + hex.EncodeToString(randomBytes)

	hash, err := localAPIKeyHash(fullKey)
	if err != nil {
		return "", nil, err
	}
	id, err := newRandomToken("", 8)
	if err != nil {
		return "", nil, err
	}

	row := &database.APIKey{
		ID:        id,
		UserID:    userID,
		Prefix:    fullKey[:apiKeyDisplayLen],
		KeyHash:   hash,
		Name:      strings.TrimSpace(name),
		CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(row).Error; err != nil {
		return "", nil, fmt.Errorf("auth: store api key: %w", err)
	}
	return fullKey, row, nil
}

// listLocalAPIKeys returns the caller's keys, newest first. Only the blind
// index is withheld from the caller — every other column is display metadata.
func listLocalAPIKeys(db *gorm.DB, userID string) ([]database.APIKey, error) {
	if db == nil {
		return nil, errors.New("auth: no database handle")
	}
	var rows []database.APIKey
	if err := db.Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("auth: list api keys: %w", err)
	}
	return rows, nil
}

// revokeLocalAPIKey deletes one key **owned by userID**. The ownership is part
// of the WHERE clause rather than a separate read, so somebody else's key is
// indistinguishable from a key that does not exist (404 either way, never a
// 500 and never a cross-account delete).
func revokeLocalAPIKey(db *gorm.DB, userID, keyID string) error {
	if db == nil {
		return errors.New("auth: no database handle")
	}
	res := db.Where("id = ? AND user_id = ?", keyID, userID).Delete(&database.APIKey{})
	if res.Error != nil {
		return fmt.Errorf("auth: revoke api key: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// localLookupUserByAPIKey resolves a raw `tk_` key to its owning user id.
//
// The user row is joined into the same query so a key belonging to an account
// that is no longer active stops authenticating. That mirrors what
// ResolveSessionToken does for sessions and is the reason a ban does not leave
// a live bearer credential behind; a missing row and an inactive account
// collapse to the same ErrAPIKeyNotFound, so the response never says which.
func localLookupUserByAPIKey(key string) (string, error) {
	db, err := database.GetDB()
	if err != nil {
		return "", err
	}
	hash, err := localAPIKeyHash(key)
	if err != nil {
		return "", err
	}

	var row database.APIKey
	err = db.Table("api_keys").
		Select("api_keys.id, api_keys.user_id, api_keys.last_used_at").
		Joins("JOIN users ON users.id = api_keys.user_id").
		Where("api_keys.key_hash = ? AND users.status = ?", hash, database.UserStatusActive).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrAPIKeyNotFound
	}
	if err != nil {
		return "", fmt.Errorf("auth: lookup api key: %w", err)
	}

	touchAPIKey(db, &row)
	return row.UserID, nil
}

// touchAPIKey refreshes last_used_at (throttled, and conditional in SQL so two
// concurrent requests cannot both win). Failures are ignored: a stale timestamp
// must never fail a request.
func touchAPIKey(db *gorm.DB, row *database.APIKey) {
	now := time.Now().UTC()
	if row.LastUsedAt != nil && now.Sub(*row.LastUsedAt) < apiKeyTouchInterval {
		return
	}
	_ = db.Model(&database.APIKey{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", row.ID, now.Add(-apiKeyTouchInterval)).
		Update("last_used_at", now).Error
}
