package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/haaku-tv/haaku/internal/auth"
	"github.com/haaku-tv/haaku/internal/cache"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/openapi"
	statsengine "github.com/haaku-tv/haaku/internal/stats"
	projredis "github.com/haaku-tv/haaku/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/haaku-tv/haaku/internal/response"
	"gorm.io/gorm"
)

func init() {
	registerOpenAPIRoutes()
}

func registerOpenAPIRoutes() {
	routes := []openapi.RouteDef{
		{
			Method:       "GET",
			Path:         "/v1/users/{id}/stats",
			Summary:      "Get user statistics (per-type plays, watched, minutes, collected, ratings and comments)",
			Tags:         []string{"Users"},
			Auth:         "optional",
			ResponseType: userStatsSchema(),
			StatusCodes: map[int]string{
				200: "User stats",
				403: "Private profile",
				404: "User not found",
			},
		},
	}
	for _, r := range routes {
		openapi.Register(r)
	}
}

// RegisterRoutes wires stats routes to the Fiber router.
func RegisterRoutes(api fiber.Router) {
	// Nested under /users/{id}/stats.
	//
	// Privacy-gated with the same auth.PrivacyFilter("id") the rest of the
	// /users/:id read surface uses: a block in either direction and an unknown
	// user are 404, a private profile is 403 to a stranger and 200 to
	// self/admins/followers. Without it an anonymous or authenticated stranger
	// could read a private user's aggregate stats (plays/minutes/ratings).
	// The public profile's aggregate stats are part of the profile surface, so
	// they share the profile section flag (users.settings.privacy.profile) in
	// addition to the is_private/block gate. A block in either direction and an
	// unknown user are 404, a private profile is 403 to a stranger and 200 to
	// self/admins/followers; profile="friends"/"private" tightens the same way.
	// OptionalAuth runs first so PrivacyFilterSection can see WHO is asking
	// (self/follower/admin vs stranger) and so "me" resolves to the caller.
	api.Get("/users/:id/stats", auth.OptionalAuth(), auth.PrivacyFilterSection("id", "profile"), handleGetUserStats)
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// handleGetUserStats returns computed user statistics.
// Cached via user_version keying; recomputed on demand if stale > 1h.
func handleGetUserStats(c *fiber.Ctx) error {
	// "me" is the caller's own id. The middleware already resolved it for the
	// privacy gate and answered 401 when nobody is signed in; the handler has
	// to resolve it too, or it looks up a user literally named "me" and 404s.
	userID := auth.ResolveTargetID(c, "id")
	if userID == "" {
		if c.Params("id") == "me" {
			return response.WriteError(c, fiber.StatusUnauthorized, "unauthorized", "authentication required")
		}
		return response.BadRequest(c, "user id is required")
	}

	db, err := database.GetDB()
	if err != nil {
		return response.WriteError(c, fiber.StatusInternalServerError, "internal", "database unavailable")
	}

	// Check if user exists.
	var user database.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "user not found")
		}
		// A DB outage is not the same as a missing user — don't mask it as 404.
		log.Printf("[stats] lookup user %s failed: %v", userID, err)
		return response.ServiceUnavailable(c, "stats are temporarily unavailable")
	}

	// Serve a version-keyed cached response when one exists for the user's
	// current version. The version is embedded in the key, so a history/data
	// write that bumps the user version counter makes this miss automatically
	// and the rebuild path below runs. Redis is best-effort: any miss or
	// outage falls through to the DB-backed read.
	if resp, ok := getVersionKeyedStatsResponse(userID); ok {
		return c.JSON(resp)
	}

	// Try fetching the last compute marker.
	cachedStats, err := statsengine.GetUserStats(db, userID)
	if err != nil {
		log.Printf("[stats] failed to fetch cached stats for %s: %v", userID, err)
		return response.WriteError(c, fiber.StatusInternalServerError, "internal", "failed to fetch stats")
	}

	// Recompute if no stats, stale > 1h, or the user version counter has
	// advanced since the last compute (history/data writes bump it, so stats
	// stop serving stale data after history changes instead of waiting an hour).
	var payload statsengine.Payload
	if cachedStats == nil || isStale(cachedStats.ComputedAt) || statsVersionMismatch(userID) {
		log.Printf("[stats] recomputing stats for user %s", userID)
		recomputed, fresh, rerr := recomputeUserStats(userID)
		if rerr != nil {
			// A recompute failure is a service-side fault — surface it instead
			// of hiding the outage behind a 200-with-zeroed-stats body.
			log.Printf("[stats] failed to recompute stats for %s: %v", userID, rerr)
			return response.ServiceUnavailable(c, "stats are temporarily unavailable")
		}
		payload = fresh

		if recomputed {
			// Bump user version for cache invalidation, then snapshot it so the
			// next write that advances it triggers a recompute (version-driven
			// staleness).
			if rdb := projredis.GetClient(); rdb != nil {
				if _, err := projredis.IncrementUserVersion(userID); err != nil {
					log.Printf("[stats] failed to bump user version for %s: %v", userID, err)
				}
			}
			storeStatsVersionSnapshot(userID)
		}
	} else {
		// Fresh enough: the payload is an aggregate over stored rows, so it is
		// read straight from them rather than from the user_stats row (which
		// carries only the flat lifetime totals and the compute timestamp).
		fresh, perr := statsengine.ComputeUserStatsPayload(db, userID)
		if perr != nil {
			log.Printf("[stats] failed to aggregate stats for %s: %v", userID, perr)
			return response.ServiceUnavailable(c, "stats are temporarily unavailable")
		}
		payload = fresh
	}

	// Persist the serialized response under the user's current version so the
	// next request can serve it without hitting the DB/compute path.
	storeVersionKeyedStatsResponse(userID, payload)

	return c.JSON(payload)
}

// ---------------------------------------------------------------------------
// Version-keyed response cache
// ---------------------------------------------------------------------------

// statsResponseCacheTTL is the TTL for a version-keyed stats response entry.
// Correctness is enforced by the embedded user version (the entry is only served
// when it matches the current counter), so the TTL is just a cleanup backstop
// for orphaned versions from a user whose counter was reset.
const statsResponseCacheTTL = 7 * 24 * time.Hour

// statsResponseKey namespaces the cached response by payload shape (`v2`) as
// well as by user version, so entries serialized in the old flat shape (which
// would unmarshal into an all-zero payload) can never be served. Correctness
// still rests on the embedded user version.
func statsResponseKey(userID string, version int64) string {
	return fmt.Sprintf("stats:response:v2:%s:v%d", userID, version)
}

// getVersionKeyedStatsResponse serves the user's stats from Redis when an entry
// exists keyed to their current version counter. Returns (zero, false) on any
// miss or Redis unavailability so the caller runs the DB-backed path.
func getVersionKeyedStatsResponse(userID string) (StatsResponse, bool) {
	var out StatsResponse
	if projredis.GetClient() == nil {
		return out, false
	}
	version, err := cache.GetUserVersion(userID)
	if err != nil {
		return out, false
	}
	data, err := projredis.GetCacheData(statsResponseKey(userID, version))
	if err != nil {
		return out, false
	}
	if err := json.Unmarshal(data, &out); err != nil {
		log.Printf("[stats] cache unmarshal error for user %s version %d: %v", userID, version, err)
		return out, false
	}
	return out, true
}

// storeVersionKeyedStatsResponse caches resp under the user's current version
// counter. Best-effort: failures only log and never affect the response.
func storeVersionKeyedStatsResponse(userID string, resp StatsResponse) {
	if projredis.GetClient() == nil {
		return
	}
	version, err := cache.GetUserVersion(userID)
	if err != nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if err := projredis.SetCacheData(statsResponseKey(userID, version), data, statsResponseCacheTTL); err != nil {
		log.Printf("[stats] cache set error for user %s version %d: %v", userID, version, err)
	}
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// StatsResponse is the JSON body of GET /v1/users/:id/stats. It is the engine's
// contract-shaped payload (see stats.Payload): the per-type blocks the contract
// declares, always present and zeroed for a user with no activity. Kept as an
// alias rather than a parallel struct so the handler, the version-keyed cache
// and the OpenAPI schema cannot drift into three slightly different shapes.
type StatsResponse = statsengine.Payload

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isStale(t *time.Time) bool {
	if t == nil {
		return true
	}
	return time.Since(*t) > time.Hour
}

// recomputeUserStats recomputes a user's stats under a per-user advisory lock
// (mirroring checkin/sync) so concurrent stale requests don't both recompute.
// It re-checks staleness after acquiring the lock — if a concurrent request
// already recomputed fresh stats while we waited, it skips the write, but it
// still aggregates the payload under the lock so the caller always gets the
// numbers the lock was taken to protect. Returns whether this call performed
// the recompute, and the contract-shaped payload.
func recomputeUserStats(userID string) (bool, statsengine.Payload, error) {
	recomputed := false
	payload := statsengine.EmptyPayload()

	err := cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "stats:"+userID).Error; err != nil {
			return nil, fmt.Errorf("advisory lock: %w", err)
		}

		cur, cerr := statsengine.GetUserStats(tx, userID)
		if cerr != nil {
			return nil, fmt.Errorf("fetch current stats under lock: %w", cerr)
		}
		if cur == nil || isStale(cur.ComputedAt) || statsVersionMismatch(userID) {
			if err := statsengine.ComputeUserStats(tx, userID); err != nil {
				return nil, fmt.Errorf("compute stats: %w", err)
			}
			recomputed = true
		}

		fresh, perr := statsengine.ComputeUserStatsPayload(tx, userID)
		if perr != nil {
			return nil, fmt.Errorf("compute stats payload: %w", perr)
		}
		payload = fresh
		return cacheTx, nil
	})
	if err != nil {
		return false, statsengine.EmptyPayload(), err
	}
	return recomputed, payload, nil
}

// statsVersionSnapshotTTL governs how long a stats version snapshot lives in
// Redis before it expires. Long enough that a snapshot from the last compute
// survives between writes; if it expires, staleness falls back to wall-clock.
const statsVersionSnapshotTTL = 7 * 24 * time.Hour

func statsVersionSnapshotKey(userID string) string {
	return "stats:user_version:" + userID
}

// statsVersionMismatch reports whether the user's version counter has advanced
// past the snapshot taken when their stats were last computed. History/data
// writes bump the user version (via their cache-tx), so a mismatch means the
// cached stats may be stale even if they're younger than 1h. Redis is best
// effort: if unavailable or no snapshot exists yet, it returns false and the
// 1h wall-clock staleness still applies.
func statsVersionMismatch(userID string) bool {
	current, err := cache.GetUserVersion(userID)
	if err != nil {
		return false
	}
	snap, err := projredis.GetCacheData(statsVersionSnapshotKey(userID))
	if err != nil {
		return false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(snap)), 10, 64)
	if err != nil {
		return false
	}
	return v != current
}

// storeStatsVersionSnapshot records the user's current version counter as the
// baseline against which future stats staleness is measured. Called after a
// recompute (and its version bump) so the snapshot matches the freshly
// computed stats.
func storeStatsVersionSnapshot(userID string) {
	if projredis.GetClient() == nil {
		return
	}
	v, err := cache.GetUserVersion(userID)
	if err != nil {
		return
	}
	if err := projredis.SetCacheData(statsVersionSnapshotKey(userID), []byte(strconv.FormatInt(v, 10)), statsVersionSnapshotTTL); err != nil {
		log.Printf("[stats] failed to store version snapshot for %s: %v", userID, err)
	}
}
