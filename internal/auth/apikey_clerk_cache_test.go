package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	projredis "github.com/haaku-tv/haaku/redis"

	goredis "github.com/redis/go-redis/v9"
)

// Clerk-mode API keys (issue #148).
//
// Nothing in this package exercised the Clerk path before, which is how two
// defects in it survived: the Redis cache was keyed by the truncated display
// prefix, and the revoke path evicted the account's *remaining* keys instead of
// the one it had just deleted. Both make a cache hit — which is answered without
// any Clerk call — resolve a key to the wrong account, or to an account that
// should no longer authenticate at all.
//
// These tests need a live Redis (the cache is the thing under test) and stand up
// their own Clerk Backend API. They skip without REDIS_URL, the same way the
// login-limiter test does, so the hermetic unit run is unchanged.

// fixturePrefixLen is how much of the key the stored prefix keeps: "tk_" plus
// the first six hex characters, i.e. the value main spells as the literal 10 in
// LookupUserByAPIKey and handleCreateAPIKey.
const fixturePrefixLen = 10

// ---------------------------------------------------------------------------
// A Clerk Backend API, as far as this package uses one
// ---------------------------------------------------------------------------

// fakeClerk serves the three calls this package makes: GET and PATCH
// /v1/users/{id} (unsafe_metadata.api_keys), and the paged GET /v1/users that
// lookupUserByKeyInClerk walks.
type fakeClerk struct {
	mu    sync.Mutex
	users map[string]map[string]any // user id -> unsafe_metadata
	order []string
}

func newFakeClerk() *fakeClerk {
	return &fakeClerk{users: map[string]map[string]any{}}
}

// seed creates a user holding the given keys.
func (f *fakeClerk) seed(userID string, keys ...APIKeyEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(keys)
	var decoded []any
	_ = json.Unmarshal(raw, &decoded)
	f.users[userID] = map[string]any{"api_keys": decoded}
	f.order = append(f.order, userID)
}

// keys returns the user's current key entries.
func (f *fakeClerk) keys(t *testing.T, userID string) []APIKeyEntry {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, err := json.Marshal(f.users[userID]["api_keys"])
	if err != nil {
		t.Fatalf("marshal fake clerk keys: %v", err)
	}
	var entries []APIKeyEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal fake clerk keys: %v", err)
	}
	return entries
}

func (f *fakeClerk) userObject(userID string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	meta := f.users[userID]
	if meta == nil {
		meta = map[string]any{}
	}
	return map[string]any{"id": userID, "unsafe_metadata": meta}
}

func (f *fakeClerk) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.URL.Path == "/v1/users":
		f.mu.Lock()
		order := append([]string(nil), f.order...)
		f.mu.Unlock()

		body := make([]map[string]any, 0, len(order))
		for _, id := range order {
			body = append(body, f.userObject(id))
		}
		_ = json.NewEncoder(w).Encode(body)

	case strings.HasPrefix(r.URL.Path, "/v1/users/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/users/")
		switch r.Method {
		case http.MethodGet:
			f.mu.Lock()
			_, ok := f.users[id]
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"errors":[{"message":"not found"}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(f.userObject(id))
		case http.MethodPatch:
			var payload struct {
				UnsafeMetadata map[string]any `json:"unsafe_metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.users[id] = payload.UnsafeMetadata
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(f.userObject(id))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// testRedis installs a Redis client on a database number of its own, so the test
// neither depends on nor disturbs whatever the shared singleton is doing.
func testRedis(t *testing.T) *goredis.Client {
	t.Helper()
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		t.Skip("REDIS_URL not set — the API-key cache is the thing under test")
	}
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		t.Skipf("unusable REDIS_URL: %v", err)
	}
	opts.DB = 15
	client := goredis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.FlushDB(context.Background()).Err() })
	return client
}

// clerkFixtures puts the package in clerk mode against a fake Clerk and returns
// the server plus two accounts whose keys share a display prefix.
func clerkFixtures(t *testing.T) (*fakeClerk, string, string, string, string) {
	t.Helper()

	client := testRedis(t)
	projredis.SetClientForTest(t, client)

	clerk := newFakeClerk()
	srv := httptest.NewServer(clerk)
	t.Cleanup(srv.Close)

	t.Setenv("CLERK_API_BASE_URL", srv.URL)
	t.Setenv("CLERK_SECRET_KEY", strings.Repeat("sk-test-", 6))
	if err := InitAuthMode("clerk", "approval"); err != nil {
		t.Fatalf("switch to clerk: %v", err)
	}
	t.Cleanup(func() {
		if err := InitAuthMode("", ""); err != nil {
			t.Fatalf("restore auth mode: %v", err)
		}
	})

	// Two keys whose first ten characters are identical and whose tails differ.
	// Hand-built rather than generated: the point is a *guaranteed* prefix
	// collision, not a birthday collision.
	sharedPrefix := "tk_" + strings.Repeat("a", 7) // exactly fixturePrefixLen (10)
	keyA := sharedPrefix + strings.Repeat("1", 57) // 67 characters, like a minted key
	keyB := sharedPrefix + strings.Repeat("2", 57)
	if keyA[:fixturePrefixLen] != keyB[:fixturePrefixLen] {
		t.Fatalf("fixture keys do not share a display prefix: %q vs %q",
			keyA[:fixturePrefixLen], keyB[:fixturePrefixLen])
	}
	if len(keyA) != 67 || len(keyB) != 67 {
		t.Fatalf("fixture keys are %d/%d characters, want 67 like a minted key", len(keyA), len(keyB))
	}

	const userA, userB = "user_alpha", "user_beta"
	clerk.seed(userA, APIKeyEntry{
		ID: "key-a", Prefix: keyA[:fixturePrefixLen], KeyHash: hashAPIKey(keyA),
		Name: "alpha", CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	clerk.seed(userB, APIKeyEntry{
		ID: "key-b", Prefix: keyB[:fixturePrefixLen], KeyHash: hashAPIKey(keyB),
		Name: "beta", CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})

	return clerk, userA, userB, keyA, keyB
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestClerkAPIKeyCacheDoesNotCollideOnTheDisplayPrefix is the regression test for
// the cache key: two keys that share their ten-character prefix must resolve to
// their own accounts, not to whichever one was cached first.
func TestClerkAPIKeyCacheDoesNotCollideOnTheDisplayPrefix(t *testing.T) {
	_, userA, userB, keyA, keyB := clerkFixtures(t)

	if got, err := LookupUserByAPIKey(keyA); err != nil || got != userA {
		t.Fatalf("lookup key A = (%q, %v), want (%q, nil)", got, err, userA)
	}
	// Key B is looked up second, with key A's entry already in the cache. A
	// prefix-keyed cache answers user_alpha here.
	if got, err := LookupUserByAPIKey(keyB); err != nil || got != userB {
		t.Fatalf("lookup key B = (%q, %v), want (%q, nil) — it resolved to the account cached under its prefix", got, err, userB)
	}
}

// TestClerkAPIKeyRevokeEvictsTheRevokedKeysOwnCacheEntry is the regression test for
// the revoke path: a key that was just deleted must stop authenticating on the
// next request, not when its cache entry expires.
func TestClerkAPIKeyRevokeEvictsTheRevokedKeysOwnCacheEntry(t *testing.T) {
	clerk, userA, userB, keyA, keyB := clerkFixtures(t)

	// Warm both entries, so a surviving one is a cache hit rather than a miss.
	if _, err := LookupUserByAPIKey(keyA); err != nil {
		t.Fatalf("warm key A: %v", err)
	}
	if _, err := LookupUserByAPIKey(keyB); err != nil {
		t.Fatalf("warm key B: %v", err)
	}

	if err := revokeClerkAPIKey(userA, "key-a"); err != nil {
		t.Fatalf("revoke key A: %v", err)
	}

	// The entry is gone from Clerk...
	if remaining := clerk.keys(t, userA); len(remaining) != 0 {
		t.Fatalf("key A is still in Clerk metadata after revoke: %+v", remaining)
	}
	// ...and the revoked key must not authenticate out of its cache entry.
	if got, err := LookupUserByAPIKey(keyA); err == nil {
		t.Fatalf("the revoked key still authenticated as %q — its cache entry outlived the revoke", got)
	}
	// The other account is untouched on both halves.
	if got, err := LookupUserByAPIKey(keyB); err != nil || got != userB {
		t.Fatalf("lookup key B after revoking key A = (%q, %v), want (%q, nil)", got, err, userB)
	}
	if code := clientGet(t, projredis.GetClient(), apiKeyCacheKey(hashAPIKey(keyB))); code != userB {
		t.Fatalf("key B's cache entry was evicted by another key's revoke: got %q", code)
	}
}

// TestClerkAPIKeyRevokeLeavesOtherKeysOfTheSameAccountWarm pins the other
// direction: eviction is per key, so revoking one key must not cold-start the
// account's other keys.
func TestClerkAPIKeyRevokeLeavesOtherKeysOfTheSameAccountWarm(t *testing.T) {
	clerk, userA, _, keyA, _ := clerkFixtures(t)

	secondKey := "tk_" + strings.Repeat("b", 7) + strings.Repeat("3", 57)
	clerk.seed(userA,
		APIKeyEntry{ID: "key-a", Prefix: keyA[:fixturePrefixLen], KeyHash: hashAPIKey(keyA), Name: "a"},
		APIKeyEntry{ID: "key-a2", Prefix: secondKey[:fixturePrefixLen], KeyHash: hashAPIKey(secondKey), Name: "a2"},
	)
	if got, err := LookupUserByAPIKey(secondKey); err != nil || got != userA {
		t.Fatalf("lookup the account's second key = (%q, %v), want (%q, nil)", got, err, userA)
	}

	if err := revokeClerkAPIKey(userA, "key-a"); err != nil {
		t.Fatalf("revoke key A: %v", err)
	}

	if got := clientGet(t, projredis.GetClient(), apiKeyCacheKey(hashAPIKey(secondKey))); got != userA {
		t.Fatalf("the account's other key lost its cache entry to an unrelated revoke: got %q", got)
	}
	if got, err := LookupUserByAPIKey(secondKey); err != nil || got != userA {
		t.Fatalf("the account's other key stopped working: (%q, %v)", got, err)
	}
}

// clientGet reads one cache entry back, so a test can tell "still warm" from
// "re-fetched from Clerk".
func clientGet(t *testing.T, rdb *goredis.Client, key string) string {
	t.Helper()
	if rdb == nil {
		t.Fatal("no redis client installed")
	}
	val, err := rdb.Get(context.Background(), key).Result()
	if err != nil {
		return ""
	}
	return val
}
