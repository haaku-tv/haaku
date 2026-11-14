package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/haaku-tv/haaku/external/tmdb"
	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newProviderResolver spins a fake TMDB that answers the two recommendation
// endpoints with fixed lists, and returns a resolver pointed at it.
func newProviderResolver(t *testing.T, movieRecs, showRecs map[int][]int) *Resolver {
	t.Helper()

	mux := http.NewServeMux()
	writePage := func(w http.ResponseWriter, ids []int, key string) {
		results := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			results = append(results, map[string]any{key + "_placeholder": true, "id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"page": 1, "results": results, "total_pages": 1, "total_results": len(results),
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var id int
		var kind string
		if _, err := fmt.Sscanf(r.URL.Path, "/movie/%d/recommendations", &id); err == nil {
			kind = "movie"
		} else if _, err := fmt.Sscanf(r.URL.Path, "/tv/%d/recommendations", &id); err == nil {
			kind = "show"
		}
		switch kind {
		case "movie":
			writePage(w, movieRecs[id], "title")
		case "show":
			writePage(w, showRecs[id], "name")
		default:
			t.Logf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Setenv("TMDB_API_KEY", "test-token")
	client, err := tmdb.New()
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}
	client.SetBaseURL(srv.URL)

	return NewResolver(nil, client, nil)
}

func TestProviderRecommendationsAggregatesSeeds(t *testing.T) {
	// Seed 10 recommends [a=100, b=101]; seed 11 recommends [b=101].
	// b is backed by both seeds, so it must lead even though it is second in
	// each list on its own.
	resolver := newProviderResolver(t, map[int][]int{
		10: {100, 101},
		11: {101},
	}, nil)

	ids, err := resolver.ProviderRecommendations(context.Background(), []RecommendationSeed{
		{Kind: "movie", TmdbID: 10, Weight: 1},
		{Kind: "movie", TmdbID: 11, Weight: 1},
	}, 10)
	if err != nil {
		t.Fatalf("ProviderRecommendations: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d ids, want 2 deduped: %v", len(ids), ids)
	}
	if ids[0] != 101 {
		t.Fatalf("first id = %d, want the title both seeds agree on (101)", ids[0])
	}
}

func TestProviderRecommendationsWeightsTheSeed(t *testing.T) {
	// A strongly-weighted seed should outvote a weak one.
	resolver := newProviderResolver(t, map[int][]int{
		10: {100},
		11: {101},
	}, nil)

	ids, err := resolver.ProviderRecommendations(context.Background(), []RecommendationSeed{
		{Kind: "movie", TmdbID: 10, Weight: 2},
		{Kind: "movie", TmdbID: 11, Weight: 1},
	}, 10)
	if err != nil {
		t.Fatalf("ProviderRecommendations: %v", err)
	}
	if len(ids) != 2 || ids[0] != 100 {
		t.Fatalf("ids = %v, want the heavier seed's pick first", ids)
	}
}

func TestProviderRecommendationsSurvivesAnUpstreamFailure(t *testing.T) {
	resolver := newProviderResolver(t, map[int][]int{10: {100}}, nil)

	ids, err := resolver.ProviderRecommendations(context.Background(), []RecommendationSeed{
		{Kind: "movie", TmdbID: 999}, // 404 from the fake provider
		{Kind: "movie", TmdbID: 10},
	}, 10)
	if err != nil {
		t.Fatalf("a failed seed must not fail the whole answer: %v", err)
	}
	if len(ids) != 1 || ids[0] != 100 {
		t.Fatalf("ids = %v, want the surviving seed's pick", ids)
	}
}

func TestProviderRecommendationsWithoutASeedIsEmpty(t *testing.T) {
	resolver := newProviderResolver(t, nil, nil)
	ids, err := resolver.ProviderRecommendations(context.Background(), nil, 10)
	if err != nil {
		t.Fatalf("ProviderRecommendations: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want none", ids)
	}
}

func TestLocalIDsForTmdbKeepsProviderOrderAndDropsUnknown(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Movie{}, &database.Show{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	movie := database.Movie{Title: "In catalogue", TmdbID: 500}
	if err := db.Create(&movie).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	resolver := NewResolver(db, nil, nil)
	// 501 is not in the catalogue; it must be dropped, not returned as 0.
	ids, err := resolver.LocalIDsForTmdb(context.Background(), "movie", []int{501, 500})
	if err != nil {
		t.Fatalf("LocalIDsForTmdb: %v", err)
	}
	if len(ids) != 1 || ids[0] != movie.ID {
		t.Fatalf("ids = %v, want [%d]", ids, movie.ID)
	}
}
