package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haaku-tv/haaku/internal/auth"
	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The `/v1/users/me/api_keys` surface (issue #92), exercised through the REAL
// router.
//
// The defect this guards is a wiring one: the three routes were registered
// without auth.AuthRequired, so c.Locals("user_id") was empty for every caller
// and each handler's own unauthorized branch was the only branch that could
// run — all three answered 401 to a signed-in client, and the frontend's
// clearSessionOnUnauthorized then dropped the session. Nothing that mounts a
// handler directly can see that, and the OpenAPI registry already declared
// auth: "required". Only a request through RegisterRoutes can.
//
// The second half is the credential itself: a self-hosted instance must be able
// to mint a `tk_` key and use it as a bearer token, with the raw key never
// recoverable from the database or from a list response.

// apiKeyTestUserID is the caller every test signs in as.
const apiKeyTestUserID = "local_apikey0000000000000000"

// apiKeyTestRootKey is the HAAKU_SECRET_KEY for this test binary, assembled by
// repetition rather than written as a literal: gitleaks scans history, and a
// credential-shaped constant in a test file fails the security gate exactly as
// loudly as a real one.
func apiKeyTestRootKey() string { return strings.Repeat("haaku-test-key-", 4) }

// apiKeyTestApp builds the real app router over an in-memory sqlite database
// with the tables this surface touches, seeds the caller, and returns a live
// session token for it.
func apiKeyTestApp(t *testing.T) (*fiber.App, *gorm.DB, string) {
	t.Helper()
	t.Setenv("HAAKU_SECRET_KEY", apiKeyTestRootKey())
	if err := auth.InitAuthMode("", ""); err != nil {
		t.Fatalf("init auth mode: %v", err)
	}
	t.Cleanup(func() {
		if err := auth.InitAuthMode("", ""); err != nil {
			t.Fatalf("restore auth mode: %v", err)
		}
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.User{},
		&database.LocalSession{},
		&database.APIKey{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	database.SetDBForTest(t, db)

	if err := db.Create(&database.User{
		ID:       apiKeyTestUserID,
		Username: "apikey",
		Email:    "apikey@example.test",
		Status:   database.UserStatusActive,
		Plan:     "free",
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	session, _, err := auth.CreateSession(db, apiKeyTestUserID, "203.0.113.7", "apikey-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Use(Recovery())
	RegisterRoutes(app)
	return app, db, session
}

// apiKeyCall performs one request with an optional bearer token.
func apiKeyCall(t *testing.T, app *fiber.App, method, path, token, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// mintAPIKey creates a key through the real route and returns its id and the
// raw key, which exists only in that response.
func mintAPIKey(t *testing.T, app *fiber.App, session, name string) (string, string) {
	t.Helper()
	status, body := apiKeyCall(t, app, http.MethodPost, "/v1/users/me/api_keys", session,
		`{"name":"`+name+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create API key: got %d, want 201 (%s)", status, body)
	}
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create response %q: %v", body, err)
	}
	if created.ID == "" || created.Key == "" {
		t.Fatalf("create response is missing id or key: %s", body)
	}
	return created.ID, created.Key
}

// TestAPIKeyRoutesRequireAuth is the regression test for the reported defect:
// all three routes must reject an anonymous caller, which only happens when
// auth.AuthRequired is actually on the route.
func TestAPIKeyRoutesRequireAuth(t *testing.T) {
	app, _, _ := apiKeyTestApp(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/v1/users/me/api_keys"},
		{http.MethodPost, "/v1/users/me/api_keys"},
		{http.MethodDelete, "/v1/users/me/api_keys/some-key-id"},
	}
	for _, tc := range cases {
		status, body := apiKeyCall(t, app, tc.method, tc.path, "", "")
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s without a credential: got %d, want 401 (%s)", tc.method, tc.path, status, body)
		}
		if !strings.Contains(body, "unauthorized") {
			t.Errorf("%s %s: response body missing the unauthorized envelope: %q", tc.method, tc.path, body)
		}
	}
}

// TestAPIKeyRoutesAcceptASession is the other half of the same defect: a valid
// session must be accepted, not answered 401. The old wiring made the handlers'
// own unauthorized branch unreachable-but-always-taken, so "401 with a valid
// session" was the only possible outcome.
func TestAPIKeyRoutesAcceptASession(t *testing.T) {
	app, _, session := apiKeyTestApp(t)

	status, body := apiKeyCall(t, app, http.MethodGet, "/v1/users/me/api_keys", session, "")
	if status != http.StatusOK {
		t.Fatalf("GET api keys with a valid session: got %d, want 200 (%s)", status, body)
	}
}

// TestLocalAPIKeyEndToEnd walks the whole acceptance bar on the real routes:
// mint a key, use it as a bearer token, list it masked, revoke it, and confirm
// the revoked key stops authenticating.
func TestLocalAPIKeyEndToEnd(t *testing.T) {
	app, db, session := apiKeyTestApp(t)

	keyID, rawKey := mintAPIKey(t, app, session, "jellyfin")

	if !strings.HasPrefix(rawKey, "tk_") {
		t.Fatalf("minted key %q does not carry the tk_ prefix", rawKey)
	}
	if len(rawKey) != 67 { // "tk_" + 64 hex characters
		t.Fatalf("minted key has length %d, want 67", len(rawKey))
	}

	// The raw key must not be in the database in any form. The store keeps an
	// HMAC blind index under a derived key, so even the hex body of the key is
	// absent.
	var row database.APIKey
	if err := db.First(&row, "id = ?", keyID).Error; err != nil {
		t.Fatalf("load stored key row: %v", err)
	}
	if strings.Contains(row.KeyHash, strings.TrimPrefix(rawKey, "tk_")) {
		t.Fatalf("the stored key_hash contains the raw key material: %s", row.KeyHash)
	}
	if row.Prefix != rawKey[:10] {
		t.Errorf("stored prefix %q does not match the key's first 10 characters %q", row.Prefix, rawKey[:10])
	}

	// It authenticates on an unrelated authenticated route...
	status, body := apiKeyCall(t, app, http.MethodGet, "/v1/users/me", rawKey, "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/users/me with the minted key: got %d, want 200 (%s)", status, body)
	}
	if !strings.Contains(body, apiKeyTestUserID) {
		t.Errorf("GET /v1/users/me resolved the key to the wrong account: %s", body)
	}
	// ...and on its own surface.
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me/api_keys", rawKey, "")
	if status != http.StatusOK {
		t.Fatalf("GET api keys with the minted key: got %d, want 200 (%s)", status, body)
	}

	// The list is masked: the raw key is not in it, and the display value is.
	var listed []map[string]any
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode list response %q: %v", body, err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected exactly 1 listed key, got %d (%s)", len(listed), body)
	}
	if strings.Contains(body, rawKey) {
		t.Fatalf("the list response contains the raw key: %s", body)
	}
	masked, _ := listed[0]["masked_key"].(string)
	if masked == "" || masked == rawKey {
		t.Errorf("masked_key = %q, want a truncated display value", masked)
	}
	if name, _ := listed[0]["name"].(string); name != "jellyfin" {
		t.Errorf("listed key name = %q, want %q", name, "jellyfin")
	}
	// last_used_at is written by the lookups above, so it must now be populated.
	if lastUsed, _ := listed[0]["last_used"].(string); lastUsed == "" {
		t.Error("last_used is empty after the key authenticated two requests")
	}

	// Revoke, then prove the key is dead.
	status, body = apiKeyCall(t, app, http.MethodDelete, "/v1/users/me/api_keys/"+keyID, session, "")
	if status != http.StatusOK {
		t.Fatalf("revoke: got %d, want 200 (%s)", status, body)
	}
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me", rawKey, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /v1/users/me with the revoked key: got %d, want 401 (%s)", status, body)
	}
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me/api_keys", session, "")
	if status != http.StatusOK {
		t.Fatalf("list after revoke: got %d, want 200 (%s)", status, body)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("list after revoke = %s, want []", body)
	}
}

// TestAPIKeyCannotManageAPIKeys is the credential-escalation guard: a `tk_` key
// authenticates the caller, but it must not be able to mint another key (which
// would outlive its own revocation) or revoke its siblings (a leaked key
// becoming a denial of service against the account). A session credential is
// what those two routes require.
func TestAPIKeyCannotManageAPIKeys(t *testing.T) {
	app, _, session := apiKeyTestApp(t)
	keyID, rawKey := mintAPIKey(t, app, session, "jellyfin")

	// Minting with the key: refused, and nothing new is created.
	status, body := apiKeyCall(t, app, http.MethodPost, "/v1/users/me/api_keys", rawKey, `{"name":"spawned"}`)
	if status != http.StatusForbidden {
		t.Fatalf("POST with an API key: got %d, want 403 (%s)", status, body)
	}
	if !strings.Contains(body, "forbidden") {
		t.Errorf("POST with an API key did not answer a forbidden envelope: %s", body)
	}

	// Revoking with the key: refused, and the key itself is still alive.
	status, body = apiKeyCall(t, app, http.MethodDelete, "/v1/users/me/api_keys/"+keyID, rawKey, "")
	if status != http.StatusForbidden {
		t.Fatalf("DELETE with an API key: got %d, want 403 (%s)", status, body)
	}

	// The same key still works as a credential on the ordinary surface, and the
	// read-only list stays open to it — only the mutations are gated.
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me", rawKey, "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/users/me with the key after two 403s: got %d (%s)", status, body)
	}
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me/api_keys", rawKey, "")
	if status != http.StatusOK {
		t.Fatalf("GET api keys with the key: got %d, want 200 (%s)", status, body)
	}
	if !strings.Contains(body, keyID) || strings.Contains(body, rawKey) {
		t.Fatalf("the list is not the expected masked row: %s", body)
	}

	// The session can still do both, so the gate is about the credential type
	// and not a broken route.
	if status, body := apiKeyCall(t, app, http.MethodPost, "/v1/users/me/api_keys", session, `{"name":"second"}`); status != http.StatusCreated {
		t.Fatalf("POST with a session after the API-key refusal: got %d (%s)", status, body)
	}
	if status, body := apiKeyCall(t, app, http.MethodDelete, "/v1/users/me/api_keys/"+keyID, session, ""); status != http.StatusOK {
		t.Fatalf("DELETE with a session after the API-key refusal: got %d (%s)", status, body)
	}
}

// TestLocalAPIKeyRevokeIsScopedToTheOwner proves the DELETE cannot reach across
// accounts: somebody else's key is a 404, and the key keeps working.
func TestLocalAPIKeyRevokeIsScopedToTheOwner(t *testing.T) {
	app, db, session := apiKeyTestApp(t)
	keyID, rawKey := mintAPIKey(t, app, session, "mine")

	other := "local_other00000000000000000000"
	if err := db.Create(&database.User{
		ID: other, Username: "other", Email: "other@example.test",
		Status: database.UserStatusActive, Plan: "free",
	}).Error; err != nil {
		t.Fatalf("seed second user: %v", err)
	}
	otherSession, _, err := auth.CreateSession(db, other, "203.0.113.8", "apikey-test")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	status, body := apiKeyCall(t, app, http.MethodDelete, "/v1/users/me/api_keys/"+keyID, otherSession, "")
	if status != http.StatusNotFound {
		t.Fatalf("revoking another account's key: got %d, want 404 (%s)", status, body)
	}
	if !strings.Contains(body, "not_found") {
		t.Errorf("cross-account revoke did not answer a not_found envelope: %s", body)
	}

	// The key is untouched.
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me", rawKey, "")
	if status != http.StatusOK {
		t.Fatalf("the key stopped working after a failed cross-account revoke: %d (%s)", status, body)
	}

	// The other account sees none of it.
	status, body = apiKeyCall(t, app, http.MethodGet, "/v1/users/me/api_keys", otherSession, "")
	if status != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Fatalf("the second account's list = %d %s, want 200 []", status, body)
	}
}
