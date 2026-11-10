package discovery

import (
	"context"
	"os"
	"testing"

	projredis "github.com/haaku-tv/haaku/redis"
)

// TestWarmCacheStillCarriesPaginationHeaders pins the defect this fixed: the
// cache-hit path returned the stored body without replaying X-Pagination-*, so
// the SAME request answered different headers depending on whether the Redis
// entry was warm.
//
// That is client-visible (a paginating client loses its next page) and it made
// `make conformance` flap: the harness re-requests a passing payload for its
// injection probes, so the ranked feeds passed on a cold run and failed on the
// one that followed, with `missing header X-Pagination-Page`.
//
// Needs a real Redis, like the rate-limiter tests, so it skips without one.
func TestWarmCacheStillCarriesPaginationHeaders(t *testing.T) {
	if os.Getenv("REDIS_URL") == "" {
		t.Skip("REDIS_URL not set — the cache-replay test needs a real Redis")
	}
	if err := projredis.InitRedis(); err != nil {
		t.Fatalf("redis: %v", err)
	}
	client := projredis.GetClient()
	if client == nil {
		t.Fatal("redis client is nil after a successful InitRedis")
	}

	ctx := context.Background()
	for _, pattern := range []string{"rankings:*", "media:*", "discovery:*"} {
		keys, err := client.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatalf("keys %s: %v", pattern, err)
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Fatalf("flush %s: %v", pattern, err)
			}
		}
	}

	db := setupRankingsDB(t)
	seedMovieRankingData(t, db)
	app := rankingsApp()
	const path = "/v1/movies/hot?limit=2"

	cold := getRankings(t, app, path)
	coldPage := cold.Header.Get("X-Pagination-Page")
	coldTotal := cold.Header.Get("X-Pagination-Total-Items")
	_ = cold.Body.Close()
	if coldPage == "" {
		t.Fatalf("cold request had no X-Pagination-Page; the test would prove nothing")
	}

	// Same request again: this one is served from Redis.
	warm := getRankings(t, app, path)
	warmPage := warm.Header.Get("X-Pagination-Page")
	warmTotal := warm.Header.Get("X-Pagination-Total-Items")
	warmCount := warm.Header.Get("X-Pagination-Items-Count")
	_ = warm.Body.Close()

	if warmPage != coldPage {
		t.Errorf("warm X-Pagination-Page = %q, want %q — a cache hit must replay the headers, not just the body", warmPage, coldPage)
	}
	if warmTotal != coldTotal {
		t.Errorf("warm X-Pagination-Total-Items = %q, want %q", warmTotal, coldTotal)
	}
	if warmCount == "" {
		t.Errorf("warm response had no X-Pagination-Items-Count")
	}
}
