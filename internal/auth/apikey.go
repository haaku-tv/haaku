package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/openapi"
	"github.com/haaku-tv/haaku/internal/response"
	tredis "github.com/haaku-tv/haaku/redis"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	apiKeyPrefix      = "tk_"
	apiKeyRandomBytes = 32 // 32 random bytes → 64 hex chars
	apiKeyCacheTTL    = 1 * time.Hour
	apiKeyRedisPrefix = "apikey:"
)

// ---------------------------------------------------------------------------
// Local error helpers (avoid import cycle with httpapi)
// ---------------------------------------------------------------------------

func writeError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}

func apiBadRequest(c *fiber.Ctx, message string) error {
	return writeError(c, fiber.StatusBadRequest, "invalid_request", message)
}

func apiUnauthorized(c *fiber.Ctx) error {
	return writeError(c, fiber.StatusUnauthorized, "unauthorized", "invalid or expired token")
}

func apiInternalError(c *fiber.Ctx, message string) error {
	return writeError(c, fiber.StatusInternalServerError, "internal", message)
}

func apiNotFound(c *fiber.Ctx, message string) error {
	return writeError(c, fiber.StatusNotFound, "not_found", message)
}

// ---------------------------------------------------------------------------
// OpenAPI route registration
// ---------------------------------------------------------------------------

func init() {
	routes := []openapi.RouteDef{
		{Method: "POST", Path: "/v1/users/me/api_keys", Summary: "Generate a new API key", Tags: []string{"API Keys"}, Auth: "required", StatusCodes: map[int]string{201: "API key created", 400: "Invalid request"}},
		{Method: "GET", Path: "/v1/users/me/api_keys", Summary: "List user's API keys (masked)", Tags: []string{"API Keys"}, Auth: "required", StatusCodes: map[int]string{200: "List of API keys"}},
		{Method: "DELETE", Path: "/v1/users/me/api_keys/{key_id}", Summary: "Revoke an API key", Tags: []string{"API Keys"}, Auth: "required", StatusCodes: map[int]string{200: "Key revoked", 404: "Key not found"}},
	}
	for _, r := range routes {
		openapi.Register(r)
	}
}

// RegisterAPIKeyRoutes wires all API key routes.
//
// AuthRequired is applied to each route, the way users.RegisterRoutes does it:
// passing the middleware to api.Group would register a handler at the group
// prefix itself (Fiber's Group(prefix, handlers...) is a Use), which adds a
// `/v1/users/me/api_keys` route ahead of the real one and trips
// TestNoStaticRouteIsShadowed.
//
// Without it, c.Locals("user_id") is empty for every caller, so each handler's
// own `apiUnauthorized` branch is the only branch that can run and all three
// routes answer 401 to a signed-in client (issue #92). Worse than a dead
// settings page — the frontend clears the stored session on any 401, so opening
// Settings → API keys logged the user out.
func RegisterAPIKeyRoutes(api fiber.Router) {
	me := api.Group("/users/me/api_keys")
	me.Post("/", AuthRequired(), handleCreateAPIKey)
	me.Get("/", AuthRequired(), handleListAPIKeys)
	me.Delete("/:key_id", AuthRequired(), handleRevokeAPIKey)
}

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

// APIKeyEntry stored in Clerk unsafe_metadata.api_keys.
type APIKeyEntry struct {
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`   // tk_<first 8 chars>
	KeyHash   string `json:"key_hash"` // SHA-256 of the full key
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used,omitempty"`
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

type createAPIKeyResponse struct {
	ID     string `json:"id"`
	Key    string `json:"key"` // Only returned once!
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

type listAPIKeyResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	// MaskedKey is the same value as Prefix in the shape the settings page
	// reads for display. The raw key is never part of a list response — it
	// exists only in the create response.
	MaskedKey string `json:"masked_key"`
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used,omitempty"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// POST /v1/users/me/api_keys — generate new API key
func handleCreateAPIKey(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return apiUnauthorized(c)
	}

	var req createAPIKeyRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}

	// Which store owns the key is the instance's identity mode, not a
	// per-request decision (issue #92).
	if LocalAuthEnabled() {
		return createLocalAPIKeyResponse(c, userID, req.Name)
	}

	// Generate tk_ prefix + 32 random bytes hex
	randomBytes, err := generateAPIKeyBytes()
	if err != nil {
		return apiInternalError(c, "failed to generate API key")
	}

	fullKey := apiKeyPrefix + hex.EncodeToString(randomBytes)
	prefix := fullKey[:apiKeyDisplayLen]
	keyHash := hashAPIKey(fullKey)

	entry := APIKeyEntry{
		ID:        generateShortID(),
		Prefix:    prefix,
		KeyHash:   keyHash,
		Name:      strings.TrimSpace(req.Name),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Store in Clerk user metadata
	if err := addAPIKeyToClerk(userID, &entry); err != nil {
		log.Printf("[apikey] failed to store API key in Clerk: %v", err)
		return apiInternalError(c, "failed to store API key")
	}

	return c.Status(fiber.StatusCreated).JSON(createAPIKeyResponse{
		ID:     entry.ID,
		Key:    fullKey,
		Name:   entry.Name,
		Prefix: prefix,
	})
}

// createLocalAPIKeyResponse mints a key against the local store. The raw key is
// in this response and nowhere else.
func createLocalAPIKeyResponse(c *fiber.Ctx, userID, name string) error {
	db, err := database.GetDB()
	if err != nil {
		return apiInternalError(c, "failed to store API key")
	}
	fullKey, row, err := createLocalAPIKey(db, userID, name)
	if err != nil {
		log.Printf("[apikey] failed to store local API key: %v", err)
		return apiInternalError(c, "failed to store API key")
	}
	return c.Status(fiber.StatusCreated).JSON(createAPIKeyResponse{
		ID:     row.ID,
		Key:    fullKey,
		Name:   row.Name,
		Prefix: row.Prefix,
	})
}

// GET /v1/users/me/api_keys — list user's keys (masked)
func handleListAPIKeys(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return apiUnauthorized(c)
	}

	if LocalAuthEnabled() {
		return listLocalAPIKeyResponse(c, userID)
	}

	keys, err := getAPIKeysFromClerk(userID)
	if err != nil {
		log.Printf("[apikey] failed to list API keys from Clerk: %v", err)
		return apiInternalError(c, "failed to retrieve API keys")
	}

	result := make([]listAPIKeyResponse, 0, len(keys))
	for _, k := range keys {
		result = append(result, listAPIKeyResponse{
			ID:        k.ID,
			Name:      k.Name,
			Prefix:    k.Prefix,
			MaskedKey: maskKey(k.Prefix),
			CreatedAt: k.CreatedAt,
			LastUsed:  k.LastUsed,
		})
	}

	return c.JSON(result)
}

// listLocalAPIKeyResponse serves the caller's locally stored keys. Nothing here
// can leak a raw key: the store keeps only the blind index, which is not part of
// the response struct.
func listLocalAPIKeyResponse(c *fiber.Ctx, userID string) error {
	db, err := database.GetDB()
	if err != nil {
		return apiInternalError(c, "failed to retrieve API keys")
	}
	rows, err := listLocalAPIKeys(db, userID)
	if err != nil {
		log.Printf("[apikey] failed to list local API keys: %v", err)
		return apiInternalError(c, "failed to retrieve API keys")
	}
	result := make([]listAPIKeyResponse, 0, len(rows))
	for _, k := range rows {
		result = append(result, listAPIKeyResponse{
			ID:        k.ID,
			Name:      k.Name,
			Prefix:    k.Prefix,
			MaskedKey: maskKey(k.Prefix),
			CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
			LastUsed:  formatLastUsed(k.LastUsedAt),
		})
	}
	return c.JSON(result)
}

// formatLastUsed renders a nullable timestamp the way the rest of this file
// renders times: RFC3339 UTC, empty when the key has never been used.
func formatLastUsed(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// DELETE /v1/users/me/api_keys/:key_id — revoke an API key
func handleRevokeAPIKey(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return apiUnauthorized(c)
	}

	keyID := c.Params("key_id")
	if keyID == "" {
		return apiBadRequest(c, "key_id is required")
	}

	if LocalAuthEnabled() {
		return revokeLocalAPIKeyResponse(c, userID, keyID)
	}

	if err := removeAPIKeyFromClerk(userID, keyID); err != nil {
		log.Printf("[apikey] failed to revoke API key from Clerk: %v", err)
		return apiInternalError(c, "failed to revoke API key")
	}

	// Invalidate Redis cache for all prefixes associated with this user
	if err := invalidateAPIKeyCacheForUser(userID); err != nil {
		log.Printf("[apikey] failed to invalidate cache for user %s: %v", userID, err)
	}

	return c.JSON(fiber.Map{"status": "revoked"})
}

// revokeLocalAPIKeyResponse deletes one of the caller's keys. Somebody else's
// key is a 404, not a 500 and not a delete: the ownership predicate is part of
// the DELETE, so there is no read-then-write window either.
func revokeLocalAPIKeyResponse(c *fiber.Ctx, userID, keyID string) error {
	db, err := database.GetDB()
	if err != nil {
		return apiInternalError(c, "failed to revoke API key")
	}
	switch err := revokeLocalAPIKey(db, userID, keyID); {
	case errors.Is(err, ErrAPIKeyNotFound):
		return apiNotFound(c, "api key not found")
	case err != nil:
		log.Printf("[apikey] failed to revoke local API key: %v", err)
		return apiInternalError(c, "failed to revoke API key")
	}
	return c.JSON(fiber.Map{"status": "revoked"})
}

// ---------------------------------------------------------------------------
// LookupUserByAPIKey — resolves an API key to a user ID
// ---------------------------------------------------------------------------

// LookupUserByAPIKey resolves a full API key string to the owning user ID.
//
// The two identity modes own separate key stores and an instance is only ever
// one of them (issue #92): a local instance has no Clerk tenant to page
// through, and a Clerk instance must not authenticate a row it never wrote.
// authmode.go is the single switch, the same one the local session path uses.
//
// Clerk mode uses the Redis cache (1h TTL) with a Clerk API fallback; local
// mode is a direct indexed lookup in the local store.
func LookupUserByAPIKey(key string) (string, error) {
	if !strings.HasPrefix(key, apiKeyPrefix) {
		return "", fmt.Errorf("key does not have tk_ prefix")
	}

	// Strip prefix for lookup
	strippedKey := strings.TrimPrefix(key, apiKeyPrefix)
	if len(strippedKey) == 0 {
		return "", fmt.Errorf("empty key")
	}

	if LocalAuthEnabled() {
		return localLookupUserByAPIKey(key)
	}

	// Compute key hash for matching
	keyHash := hashAPIKey(apiKeyPrefix + strippedKey)
	prefix := key[:apiKeyDisplayLen] // tk_ + first 6 hex chars

	// Try Redis cache first
	cacheKey := apiKeyRedisPrefix + prefix
	rdb := tredis.GetClient()
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if cachedUserID, err := rdb.Get(ctx, cacheKey).Result(); err == nil && cachedUserID != "" {
			return cachedUserID, nil
		}
	}

	// Cache miss — query Clerk API
	userID, err := lookupUserByKeyInClerk(prefix, keyHash)
	if err != nil {
		return "", fmt.Errorf("clerk lookup failed: %w", err)
	}
	if userID == "" {
		return "", fmt.Errorf("api key not found")
	}

	// Cache back (fail-open)
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rdb.Set(ctx, cacheKey, userID, apiKeyCacheTTL).Err(); err != nil {
			log.Printf("[apikey] failed to cache API key lookup: %v", err)
		}
	}

	return userID, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func generateAPIKeyBytes() ([]byte, error) {
	b := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func generateShortID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func maskKey(prefix string) string {
	if len(prefix) < 10 {
		return prefix + "\u2026"
	}
	return prefix + "\u2026"
}

// ---------------------------------------------------------------------------
// Clerk API integration
// ---------------------------------------------------------------------------

func clerkBaseURL() string {
	return os.Getenv("CLERK_API_BASE_URL")
}

func clerkSecretKey() string {
	return os.Getenv("CLERK_SECRET_KEY")
}

func clerkHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// getAPIKeysFromClerk retrieves the api_keys array from Clerk user metadata.
func getAPIKeysFromClerk(userID string) ([]APIKeyEntry, error) {
	baseURL := clerkBaseURL()
	if baseURL == "" {
		baseURL = "https://api.clerk.com"
	}
	secret := clerkSecretKey()
	if secret == "" {
		return nil, fmt.Errorf("CLERK_SECRET_KEY not set")
	}

	url := fmt.Sprintf("%s/v1/users/%s", strings.TrimRight(baseURL, "/"), userID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := clerkHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clerk API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		UnsafeMetadata struct {
			APIKeys json.RawMessage `json:"api_keys"`
		} `json:"unsafe_metadata"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result.UnsafeMetadata.APIKeys == nil {
		return []APIKeyEntry{}, nil
	}

	var keys []APIKeyEntry
	if err := json.Unmarshal(result.UnsafeMetadata.APIKeys, &keys); err != nil {
		return nil, err
	}
	if keys == nil {
		return []APIKeyEntry{}, nil
	}
	return keys, nil
}

// addAPIKeyToClerk appends an API key entry to Clerk user metadata.
func addAPIKeyToClerk(userID string, entry *APIKeyEntry) error {
	existing, err := getAPIKeysFromClerk(userID)
	if err != nil {
		return fmt.Errorf("failed to fetch existing keys: %w", err)
	}

	existing = append(existing, *entry)

	return updateClerkAPIKeys(userID, existing)
}

// removeAPIKeyFromClerk removes an API key entry from Clerk user metadata by ID.
func removeAPIKeyFromClerk(userID, keyID string) error {
	existing, err := getAPIKeysFromClerk(userID)
	if err != nil {
		return fmt.Errorf("failed to fetch existing keys: %w", err)
	}

	var filtered []APIKeyEntry
	found := false
	for _, k := range existing {
		if k.ID == keyID {
			found = true
			continue
		}
		filtered = append(filtered, k)
	}

	if !found {
		return fmt.Errorf("key %s not found for user %s", keyID, userID)
	}

	return updateClerkAPIKeys(userID, filtered)
}

// updateClerkAPIKeys writes the api_keys array to Clerk user metadata.
func updateClerkAPIKeys(userID string, keys []APIKeyEntry) error {
	baseURL := clerkBaseURL()
	if baseURL == "" {
		baseURL = "https://api.clerk.com"
	}
	secret := clerkSecretKey()
	if secret == "" {
		return fmt.Errorf("CLERK_SECRET_KEY not set")
	}

	body := map[string]interface{}{
		"unsafe_metadata": map[string]interface{}{
			"api_keys": keys,
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/users/%s", strings.TrimRight(baseURL, "/"), userID)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := clerkHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clerk API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// lookupUserByKeyInClerk searches all users to find one with a matching API key.
// This is the fallback when Redis cache is empty.
func lookupUserByKeyInClerk(prefix, keyHash string) (string, error) {
	baseURL := clerkBaseURL()
	if baseURL == "" {
		baseURL = "https://api.clerk.com"
	}
	secret := clerkSecretKey()
	if secret == "" {
		return "", fmt.Errorf("CLERK_SECRET_KEY not set")
	}

	const limit = 100
	offset := 0

	for {
		url := fmt.Sprintf("%s/v1/users?limit=%d&offset=%d&order_by=created_at",
			strings.TrimRight(baseURL, "/"), limit, offset)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")

		resp, err := clerkHTTPClient().Do(req)
		if err != nil {
			return "", err
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "", err
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("clerk API returned %d", resp.StatusCode)
		}

		var users []struct {
			ID             string `json:"id"`
			UnsafeMetadata struct {
				APIKeys json.RawMessage `json:"api_keys"`
			} `json:"unsafe_metadata"`
		}
		if err := json.Unmarshal(body, &users); err != nil {
			return "", err
		}

		if len(users) == 0 {
			break
		}

		for _, user := range users {
			if user.UnsafeMetadata.APIKeys == nil {
				continue
			}
			var keys []APIKeyEntry
			if err := json.Unmarshal(user.UnsafeMetadata.APIKeys, &keys); err != nil {
				continue
			}
			for _, k := range keys {
				if k.Prefix == prefix && k.KeyHash == keyHash {
					return user.ID, nil
				}
			}
		}

		if len(users) < limit {
			break
		}
		offset += limit
	}

	return "", nil
}

// invalidateAPIKeyCacheForUser clears Redis cache entries for a user's API keys.
func invalidateAPIKeyCacheForUser(userID string) error {
	rdb := tredis.GetClient()
	if rdb == nil {
		return nil
	}

	keys, err := getAPIKeysFromClerk(userID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, k := range keys {
		cacheKey := apiKeyRedisPrefix + k.Prefix
		rdb.Del(ctx, cacheKey)
	}

	return nil
}
