package discovery

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/handlers/media"
	"github.com/haaku-tv/haaku/internal/openapi"
	projredis "github.com/haaku-tv/haaku/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/haaku-tv/haaku/internal/response"
)

func init() {
	registerOpenAPIRoutes()
}

// registerOpenAPIRoutes registers all discovery endpoints in the OpenAPI registry.
func registerOpenAPIRoutes() {
	routes := []openapi.RouteDef{
		// Movies discovery
		{Method: "GET", Path: "/v1/movies/trending", Summary: "Get trending movies", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Trending movies"}},
		{Method: "GET", Path: "/v1/movies/popular", Summary: "Get popular movies", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Popular movies"}},
		{Method: "GET", Path: "/v1/movies/recommendations/{id}", Summary: "Get movie recommendations", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Recommendations", 501: "Not implemented (requires recommendations engine)"}},

		// Shows discovery
		{Method: "GET", Path: "/v1/shows/trending", Summary: "Get trending shows", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Trending shows"}},
		{Method: "GET", Path: "/v1/shows/popular", Summary: "Get popular shows", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Popular shows"}},
		{Method: "GET", Path: "/v1/shows/recommendations/{id}", Summary: "Get show recommendations", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Recommendations", 501: "Not implemented (requires recommendations engine)"}},
	}

	for _, r := range routes {
		openapi.Register(r)
	}
}

// RegisterRoutes wires all discovery route handlers to the Fiber router.
func RegisterRoutes(api fiber.Router) {
	// --- Movies ---
	api.Get("/movies/trending", handleMoviesTrending)
	api.Get("/movies/popular", handleMoviesPopular)
	api.Get("/movies/recommendations/:id", handleMoviesRecommendations)

	// --- Shows ---
	api.Get("/shows/trending", handleShowsTrending)
	api.Get("/shows/popular", handleShowsPopular)
	api.Get("/shows/recommendations/:id", handleShowsRecommendations)

	// Contract-shaped anticipated feeds — static "/anticipated" must precede
	// media's parameterised /movies/:id and /shows/:id.
	registerContractAnticipatedRoutes(api)

	// Period rankings, hot, and the merged cross-type feeds (issue #66). Every
	// static segment here (watched, played, collected, favorited, hot, media)
	// must be registered before media's parameterised /movies/:id and
	// /shows/:id routes.
	registerRankingRoutes(api)
	registerMediaRankingRoutes(api)
}

// ---------------------------------------------------------------------------
// Movies — Trending / Popular
// ---------------------------------------------------------------------------

func handleMoviesTrending(c *fiber.Ctx) error {
	return moviesList(c, "trending")
}

func handleMoviesPopular(c *fiber.Ctx) error {
	return moviesList(c, "popular")
}

func handleShowsTrending(c *fiber.Ctx) error {
	return showsList(c, "trending")
}

func handleShowsPopular(c *fiber.Ctx) error {
	return showsList(c, "popular")
}

// moviesList queries movies ordered by popularity DESC with pagination.
// Results are cached in Redis (short TTL) because these are hot, public,
// read-heavy endpoints. A cache failure never fails the request — it falls
// through to the DB.
func moviesList(c *fiber.Ctx, kind string) error {
	page, limit := response.ParsePagination(c, 20)
	extended := media.GetExtended(c)

	cacheKey := discoveryCacheKey("movies", kind, page, limit, extended)
	if data, ok := discoveryGetCache(cacheKey); ok {
		c.Set("Content-Type", "application/json")
		return c.Send(data)
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c, "database unavailable")
	}

	var movies []database.Movie
	if err := db.Order("popularity DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&movies).Error; err != nil {
		return response.InternalError(c, "database query error")
	}

	resp := make([]media.MovieResponse, 0, len(movies))
	for i := range movies {
		resp = append(resp, media.BuildMovieResponse(&movies[i], extended))
	}

	discoverySetCache(cacheKey, resp)
	return c.JSON(resp)
}

// showsList queries shows ordered by popularity DESC with pagination.
func showsList(c *fiber.Ctx, kind string) error {
	page, limit := response.ParsePagination(c, 20)
	extended := media.GetExtended(c)

	cacheKey := discoveryCacheKey("shows", kind, page, limit, extended)
	if data, ok := discoveryGetCache(cacheKey); ok {
		c.Set("Content-Type", "application/json")
		return c.Send(data)
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c, "database unavailable")
	}

	var shows []database.Show
	if err := db.Order("popularity DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&shows).Error; err != nil {
		return response.InternalError(c, "database query error")
	}

	resp := make([]media.ShowResponse, 0, len(shows))
	for i := range shows {
		resp = append(resp, media.BuildShowResponse(&shows[i], extended))
	}

	discoverySetCache(cacheKey, resp)
	return c.JSON(resp)
}

// ---------------------------------------------------------------------------
// Recommendations — blocked on the recommendations engine (issue #5)
// ---------------------------------------------------------------------------

func handleMoviesRecommendations(c *fiber.Ctx) error {
	// Recommendations require the recommendation graph (issue #5), which has not
	// landed. Fails closed with 501 rather than return a fabricated empty array
	// with a 200 — a made-up payload is worse than an honest "not implemented".
	return response.NotImplemented(c, "recommendations require the recommendations engine, which is not implemented")
}

func handleShowsRecommendations(c *fiber.Ctx) error {
	// Same blocker as movies: the recommendations engine has not landed. Fails
	// closed with 501 rather than return a fabricated empty array.
	return response.NotImplemented(c, "recommendations require the recommendations engine, which is not implemented")
}

// ---------------------------------------------------------------------------
// Redis TTL cache for hot public listings
// ---------------------------------------------------------------------------

// discoveryListTTL bounds how long a hot public listing (trending/popular) is
// served from cache. Short enough to stay fresh, long enough to absorb bursts.
const discoveryListTTL = 5 * time.Minute

// discoveryCacheKey builds a stable key from every input that affects the
// response body: media type, list kind, page, limit, and extended level.
func discoveryCacheKey(mediaType, kind string, page, limit int, extended string) string {
	return fmt.Sprintf("discovery:%s:%s:page%d:limit%d:extended%s", mediaType, kind, page, limit, extended)
}

// cachedListing is what the paginated list endpoints store in Redis: the encoded
// response body PLUS the pagination metadata that is delivered as headers.
//
// Caching only the body meant a cache hit answered without X-Pagination-*, so
// the same request returned different headers depending on whether the entry was
// warm. That is a real client-visible loss, and it made the ranked feeds flap
// between conformant and failing in `make conformance` — the harness re-requests
// a passing payload for its injection probes, so a run could see a cold first
// request (headers present, PASS) and the next run a warm one (no headers,
// FAIL).
type cachedListing struct {
	Body  json.RawMessage `json:"body"`
	Page  int             `json:"page"`
	Limit int             `json:"limit"`
	Total int64           `json:"total"`
	Items int             `json:"items"`
}

// discoveryGetListing replays a cached listing onto the response, headers
// included, and reports whether it answered. A miss, an unreadable entry or any
// Redis problem returns false so the caller falls through to the DB — the cache
// is best-effort and never fails the request.
func discoveryGetListing(c *fiber.Ctx, key string) bool {
	if projredis.GetClient() == nil {
		return false
	}
	data, err := projredis.GetCacheData(key)
	if err != nil {
		return false
	}
	var listing cachedListing
	if err := json.Unmarshal(data, &listing); err != nil || len(listing.Body) == 0 {
		// An entry written by the old body-only shape, or one with no body, is
		// unusable: treat it as a miss so the next request recomputes and
		// rewrites it rather than answering 200 with an empty body.
		return false
	}
	response.SetPaginationHeaders(c, listing.Page, listing.Limit, listing.Total, listing.Items)
	c.Set("Content-Type", "application/json")
	return c.Send(listing.Body) == nil
}

// discoverySetListing stores the body together with its pagination metadata.
// Best-effort: a Redis or marshal failure only logs.
func discoverySetListing(key string, page, limit int, total int64, items int, v interface{}) {
	if projredis.GetClient() == nil {
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("[discovery] cache marshal error for key %s: %v", key, err)
		return
	}
	payload, err := json.Marshal(cachedListing{
		Body: body, Page: page, Limit: limit, Total: total, Items: items,
	})
	if err != nil {
		log.Printf("[discovery] cache envelope error for key %s: %v", key, err)
		return
	}
	if err := projredis.SetCacheData(key, payload, discoveryListTTL); err != nil {
		log.Printf("[discovery] cache set error for key %s: %v", key, err)
	}
}

// discoveryGetCache returns the cached raw JSON body for key. It returns
// (nil, false) on any miss or Redis unavailability so the caller falls through
// to the DB — the cache is best-effort and never blocks or fails the request.
func discoveryGetCache(key string) ([]byte, bool) {
	if projredis.GetClient() == nil {
		return nil, false
	}
	data, err := projredis.GetCacheData(key)
	if err != nil {
		return nil, false
	}
	return data, true
}

// discoverySetCache stores the marshaled JSON of v under key with discoveryListTTL.
// Best-effort: a Redis failure only logs.
func discoverySetCache(key string, v interface{}) {
	if projredis.GetClient() == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[discovery] cache marshal error for key %s: %v", key, err)
		return
	}
	if err := projredis.SetCacheData(key, data, discoveryListTTL); err != nil {
		log.Printf("[discovery] cache set error for key %s: %v", key, err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
