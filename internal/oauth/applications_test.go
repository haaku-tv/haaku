package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/haaku-tv/haaku/internal/auth"
	"github.com/haaku-tv/haaku/internal/database"
)

// setupOAuthDB points the database singleton at an in-memory store holding the
// tables the OAuth handlers touch.
func setupOAuthDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	// The OAuth tables use PostgreSQL types (uuid defaults, text[]) that sqlite
	// cannot parse, so the test store creates the same columns as plain text.
	ddl := []string{
		`CREATE TABLE oauth_clients (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, redirect_uris TEXT NOT NULL DEFAULT '[]',
			secret_hash TEXT NOT NULL, user_id TEXT NOT NULL, created_at DATETIME)`,
		`CREATE TABLE oauth_grants (
			id TEXT PRIMARY KEY, client_id TEXT NOT NULL, user_id TEXT NOT NULL,
			code_hash TEXT NOT NULL, code_challenge TEXT, code_challenge_method TEXT,
			redirect_uri TEXT NOT NULL, scopes TEXT NOT NULL DEFAULT '{}',
			expires_at DATETIME, used BOOLEAN NOT NULL DEFAULT 0, created_at DATETIME)`,
		`CREATE TABLE oauth_tokens (
			id TEXT PRIMARY KEY, grant_id TEXT, client_id TEXT NOT NULL, user_id TEXT NOT NULL,
			token_hash TEXT NOT NULL, refresh_token_hash TEXT, refresh_token_family TEXT,
			scopes TEXT NOT NULL DEFAULT '{}', expires_at DATETIME, refresh_expires_at DATETIME,
			revoked BOOLEAN NOT NULL DEFAULT 0, created_at DATETIME)`,
	}
	for _, stmt := range ddl {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	database.SetDBForTest(t, db)
	return db
}

// oauthApp mounts the OAuth routes with a fixed signed-in user, standing in for
// the auth middleware that fronts them in production.
func oauthApp(userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("user_id", userID)
		}
		return c.Next()
	})
	RegisterRoutes(app)
	return app
}

func seedClient(t *testing.T, db *gorm.DB, userID, name string) database.OAuthClient {
	t.Helper()
	// The production column defaults its uuid in PostgreSQL; sqlite has no such
	// default, so the test assigns one.
	client := database.OAuthClient{
		ID:   "client-" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		Name: name, UserID: userID,
		RedirectURIs: database.StringArray{"https://app.example/callback"},
		SecretHash:   hashSecret("secret"),
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	return client
}

func TestListApplicationsReturnsOnlyTheCallersClients(t *testing.T) {
	db := setupOAuthDB(t)
	mine := seedClient(t, db, "local_me", "My App")
	seedClient(t, db, "local_other", "Someone Else")

	if err := db.Create(&database.OAuthToken{
		ID:        "token-mine",
		ClientID:  mine.ID,
		UserID:    "local_me",
		TokenHash: hashToken("hoa_x"),
		Scopes:    database.TextArray{"profile", "history:read"},
		ExpiresAt: time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	resp, err := oauthApp("local_me").Test(
		httptest.NewRequest(http.MethodGet, "/oauth/applications", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (this was the 405)", resp.StatusCode)
	}

	var body struct {
		Items []applicationView `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("got %d applications, want only the caller's one", len(body.Items))
	}
	if body.Items[0].Name != "My App" {
		t.Fatalf("name = %q", body.Items[0].Name)
	}
	if len(body.Items[0].Scopes) != 2 || body.Items[0].Scopes[0] != "profile" {
		t.Fatalf("scopes = %v, want the granted ones", body.Items[0].Scopes)
	}
	if body.Items[0].ActiveTokens != 1 {
		t.Fatalf("active tokens = %d, want 1", body.Items[0].ActiveTokens)
	}
}

func TestDeleteApplicationRevokesItsTokens(t *testing.T) {
	db := setupOAuthDB(t)
	client := seedClient(t, db, "local_me", "My App")

	token := database.OAuthToken{
		ID:        "token-live",
		ClientID:  client.ID,
		UserID:    "local_me",
		TokenHash: hashToken("hoa_live"),
		Scopes:    database.TextArray{"profile"},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	app := oauthApp("local_me")
	resp, err := app.Test(httptest.NewRequest(http.MethodDelete,
		"/oauth/applications/"+client.ID, nil))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	var stored database.OAuthToken
	if err := db.First(&stored, "id = ?", token.ID).Error; err != nil {
		t.Fatalf("token row vanished instead of being revoked: %v", err)
	}
	if !stored.Revoked {
		t.Fatal("deleting the application left a live token behind")
	}

	var count int64
	if err := db.Model(&database.OAuthClient{}).Where("id = ?", client.ID).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatal("application was not deleted")
	}
}

func TestDeletingAnotherAccountsApplicationDoesNothing(t *testing.T) {
	db := setupOAuthDB(t)
	theirs := seedClient(t, db, "local_other", "Not Yours")

	resp, err := oauthApp("local_me").Test(httptest.NewRequest(http.MethodDelete,
		"/oauth/applications/"+theirs.ID, nil))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (the response must not reveal the row exists)", resp.StatusCode)
	}

	var count int64
	if err := db.Model(&database.OAuthClient{}).Where("id = ?", theirs.ID).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatal("another account's application was deleted")
	}
}

// TestAuthorizeAsksForConsentThenRedirects covers the browser flow: the first
// request renders a page naming the client and the scopes, and only an explicit
// approval redirects back with a code.
func TestAuthorizeAsksForConsentThenRedirects(t *testing.T) {
	db := setupOAuthDB(t)
	client := seedClient(t, db, "local_me", "Silotest")

	app := oauthApp("local_me")
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"https://app.example/callback"},
		"scope":                 {"profile history:read"},
		"state":                 {"nonce-1"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
	}

	consent, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/oauth/authorize?"+query.Encode(), nil))
	if err != nil {
		t.Fatalf("consent request: %v", err)
	}
	body := make([]byte, 4096)
	n, _ := consent.Body.Read(body)
	_ = consent.Body.Close()
	page := string(body[:n])

	if consent.StatusCode != http.StatusOK {
		t.Fatalf("consent status = %d, want 200 with a page", consent.StatusCode)
	}
	if ct := consent.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("consent content-type = %q, want text/html", ct)
	}
	for _, want := range []string{"Silotest", "profile", "history:read", "decision"} {
		if !strings.Contains(page, want) {
			t.Fatalf("consent page is missing %q:\n%s", want, page)
		}
	}

	// Deny first: no code, and the client is told access_denied.
	denied := url.Values{}
	for key, value := range query {
		denied[key] = value
	}
	denied.Set("decision", "deny")
	denyResp, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/oauth/authorize?"+denied.Encode(), nil))
	if err != nil {
		t.Fatalf("deny request: %v", err)
	}
	_ = denyResp.Body.Close()
	if denyResp.StatusCode != http.StatusFound {
		t.Fatalf("deny status = %d, want a redirect", denyResp.StatusCode)
	}
	if location := denyResp.Header.Get("Location"); !strings.Contains(location, "access_denied") {
		t.Fatalf("deny redirect = %q, want access_denied", location)
	}

	// Approve: a code comes back to the registered redirect_uri.
	approved := url.Values{}
	for key, value := range query {
		approved[key] = value
	}
	approved.Set("decision", "approve")
	okResp, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/oauth/authorize?"+approved.Encode(), nil))
	if err != nil {
		t.Fatalf("approve request: %v", err)
	}
	_ = okResp.Body.Close()
	if okResp.StatusCode != http.StatusFound {
		t.Fatalf("approve status = %d, want a redirect", okResp.StatusCode)
	}
	location := okResp.Header.Get("Location")
	if !strings.HasPrefix(location, "https://app.example/callback?") {
		t.Fatalf("approve redirect = %q, want the registered redirect_uri", location)
	}
	if !strings.Contains(location, "code=") || !strings.Contains(location, "state=nonce-1") {
		t.Fatalf("approve redirect = %q, want code and state", location)
	}
}

// TestAccessTokenAuthenticates is the issue in one test: a token minted by
// /oauth/token now authenticates a protected call.
func TestAccessTokenAuthenticates(t *testing.T) {
	db := setupOAuthDB(t)
	now := time.Now().UTC()
	if err := db.Create(&database.User{
		ID: "local_me", Username: "me", Email: "me@example.test", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	access := accessTokenPrefix + "deadbeef"
	if err := db.Create(&database.OAuthToken{
		ID:        "token-access",
		ClientID:  "11111111-1111-1111-1111-111111111111",
		UserID:    "local_me",
		TokenHash: hashToken(access),
		Scopes:    database.TextArray{"profile"},
		ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	app := fiber.New()
	app.Get("/v1/whoami", auth.AuthRequired(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"user_id": c.Locals("user_id"), "method": c.Locals("auth_method")})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an issued token must authenticate", resp.StatusCode)
	}
	var body struct {
		UserID string `json:"user_id"`
		Method string `json:"method"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.UserID != "local_me" || body.Method != "oauth" {
		t.Fatalf("whoami = %+v, want local_me via oauth", body)
	}
}

func TestRevokedTokenStopsAuthenticating(t *testing.T) {
	db := setupOAuthDB(t)
	now := time.Now().UTC()
	if err := db.Create(&database.User{
		ID: "local_me", Username: "me", Email: "me@example.test", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	access := accessTokenPrefix + "cafebabe"
	token := database.OAuthToken{
		ID:        "token-revoked",
		ClientID:  "11111111-1111-1111-1111-111111111111",
		UserID:    "local_me",
		TokenHash: hashToken(access),
		Scopes:    database.TextArray{"profile"},
		ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := db.Model(&token).Update("revoked", true).Error; err != nil {
		t.Fatalf("revoke: %v", err)
	}

	app := fiber.New()
	app.Get("/v1/whoami", auth.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a revoked token", resp.StatusCode)
	}
}

func TestUserCodeHalvesAreIndependent(t *testing.T) {
	seen := make(map[string]struct{}, 200)
	sameHalf := 0
	for i := 0; i < 200; i++ {
		code, err := generateUserCode()
		if err != nil {
			t.Fatalf("generateUserCode: %v", err)
		}
		parts := strings.Split(code, "-")
		if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 4 {
			t.Fatalf("code %q is not XXXX-XXXX", code)
		}
		if parts[0] == parts[1] {
			sameHalf++
		}
		seen[code] = struct{}{}
	}
	if sameHalf > 5 {
		t.Fatalf("%d of 200 codes repeated the same four characters; the halves are not independent", sameHalf)
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct codes in 200 draws", len(seen))
	}
}

// TestOAuthTokenIsLimitedToItsScopes is the finding behind the guard: a token
// granted only "profile" must not be able to write history, and the refusal
// names the scope it would have needed.
func TestOAuthTokenIsLimitedToItsScopes(t *testing.T) {
	db := setupOAuthDB(t)
	now := time.Now().UTC()
	if err := db.Create(&database.User{
		ID: "local_me", Username: "me", Email: "me@example.test", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	access := accessTokenPrefix + "scoped"
	if err := db.Create(&database.OAuthToken{
		ID:        "token-scoped",
		ClientID:  "11111111-1111-1111-1111-111111111111",
		UserID:    "local_me",
		TokenHash: hashToken(access),
		Scopes:    database.TextArray{"profile"},
		ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	app := fiber.New()
	api := app.Group("/v1", auth.OAuthScopeGuard())
	api.Get("/users/me", auth.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})
	api.Post("/sync/history", auth.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusCreated)
	})

	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if status := do(http.MethodGet, "/v1/users/me"); status != http.StatusOK {
		t.Fatalf("profile read = %d, want 200 (the token holds profile)", status)
	}
	if status := do(http.MethodPost, "/v1/sync/history"); status != http.StatusForbidden {
		t.Fatalf("history write = %d, want 403 (the token holds only profile)", status)
	}
}

// TestOAuthTokenWithWriteScopeMayWrite is the other half: the guard must not
// block a token that does hold the scope.
func TestOAuthTokenWithWriteScopeMayWrite(t *testing.T) {
	db := setupOAuthDB(t)
	now := time.Now().UTC()
	if err := db.Create(&database.User{
		ID: "local_me", Username: "me", Email: "me@example.test", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	access := accessTokenPrefix + "writer"
	if err := db.Create(&database.OAuthToken{
		ID:        "token-writer",
		ClientID:  "11111111-1111-1111-1111-111111111111",
		UserID:    "local_me",
		TokenHash: hashToken(access),
		Scopes:    database.TextArray{"history:write"},
		ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	app := fiber.New()
	api := app.Group("/v1", auth.OAuthScopeGuard())
	api.Get("/sync/history", auth.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})

	// A write scope implies its read counterpart, which is what the consent
	// page advertises.
	req := httptest.NewRequest(http.MethodGet, "/v1/sync/history", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history read = %d, want 200 for a history:write token", resp.StatusCode)
	}
}

// TestSessionCredentialRejectsOAuthTokens covers the escalation: minting API
// keys is a session-only surface, and an OAuth token is not a session.
func TestSessionCredentialRejectsOAuthTokens(t *testing.T) {
	app := fiber.New()
	app.Post("/v1/users/me/api-keys",
		func(c *fiber.Ctx) error {
			c.Locals("user_id", "local_me")
			c.Locals("auth_method", "oauth")
			return c.Next()
		},
		auth.RequireSessionCredential(),
		func(c *fiber.Ctx) error { return c.SendStatus(http.StatusCreated) },
	)

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/v1/users/me/api-keys", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — an OAuth token must not mint API keys", resp.StatusCode)
	}
}
