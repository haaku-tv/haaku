package discovery

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	projredis "github.com/haaku-tv/haaku/redis"
)

// hotListingCacheKey is the key the hot feed under test reads and writes:
// serveHot builds `rankings:hot:<type>:page<N>:limit<N>`, and these tests drive
// /v1/movies/hot?limit=2.
const hotListingCacheKey = "rankings:hot:movie:page1:limit2"

// clearHotListingCache makes the next request for hotListingCacheKey a real miss
// and deletes the entry again on cleanup.
//
// It removes exactly that one key. Sweeping rankings:*, media:* and discovery:*
// also evicted every OTHER listing's entry — state the test neither owns nor
// created — which on a shared Redis silently discards another process's cache.
func clearHotListingCache(t *testing.T) {
	t.Helper()
	client := projredis.GetClient()
	if client == nil {
		t.Fatal("redis client is nil after a successful InitRedis")
	}
	if err := client.Del(context.Background(), hotListingCacheKey).Err(); err != nil {
		t.Fatalf("clear %s: %v", hotListingCacheKey, err)
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), hotListingCacheKey).Err() })
}

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
	clearHotListingCache(t)

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

// TestEnvelopeWithoutABodyIsAMiss covers the other half of the cache contract: an
// entry that unmarshals but carries no body must be treated as a miss. Answering
// 200 with an empty body is worse than a cache miss, and the old body-only
// format made that reachable.
func TestEnvelopeWithoutABodyIsAMiss(t *testing.T) {
	if os.Getenv("REDIS_URL") == "" {
		t.Skip("REDIS_URL not set — the cache-replay test needs a real Redis")
	}
	if err := projredis.InitRedis(); err != nil {
		t.Fatalf("redis: %v", err)
	}
	clearHotListingCache(t)

	db := setupRankingsDB(t)
	seedMovieRankingData(t, db)
	app := rankingsApp()

	// A well-formed envelope with no body, under the key the handler will read.
	bodyless := []byte(`{"page":1,"limit":2,"total":9,"items":9}`)
	if err := projredis.SetCacheData(hotListingCacheKey, bodyless, time.Minute); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	resp := getRankings(t, app, "/v1/movies/hot?limit=2")
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(raw) == 0 || string(raw) == "null" {
		t.Fatalf("body = %q, want the recomputed listing — a bodyless entry must be a miss, not an empty 200", raw)
	}
	// The recomputed answer must describe the real data, not the poisoned envelope.
	if got := resp.Header.Get("X-Pagination-Total-Items"); got == "9" {
		t.Errorf("X-Pagination-Total-Items = %q, want the recomputed value, not the bodyless entry's", got)
	}
}
