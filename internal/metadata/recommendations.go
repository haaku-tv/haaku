package metadata

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/haaku-tv/haaku/external/tmdb"
	"github.com/haaku-tv/haaku/internal/database"
)

var (
	defaultOnce     sync.Once
	defaultResolver *Resolver
)

// maxRecommendationSeeds bounds how many titles one personal-recommendation
// request asks the provider about. Each seed is one upstream call, and the
// aggregate is dominated by the first few anyway.
const maxRecommendationSeeds = 5

// RecommendationSeed is one catalogue row the provider should be asked to
// expand, with how strongly that row speaks for the account.
type RecommendationSeed struct {
	Kind   string // movie | show
	TmdbID int
	Weight float64
}

// ProviderRecommendations returns the provider's related titles for a set of
// seeds, aggregated and ordered best-first.
//
// This is the backbone for an instance with no viewing history of its own: TMDB
// already knows which titles cluster together, and the caller then keeps the
// ones this catalogue actually holds. A seed with no provider id, or a failed
// upstream call, contributes nothing — the catalogue engine covers the rest.
func (r *Resolver) ProviderRecommendations(ctx context.Context, seeds []RecommendationSeed, limit int) ([]int, error) {
	if limit <= 0 {
		limit = 20
	}
	if len(seeds) > maxRecommendationSeeds {
		seeds = seeds[:maxRecommendationSeeds]
	}

	score := make(map[int]float64, limit*4)
	for _, seed := range seeds {
		if seed.TmdbID <= 0 || r == nil || r.TMDB == nil {
			continue
		}
		// A cancelled request should not keep walking seeds: five sequential
		// provider timeouts would hold the response open for a minute.
		if ctx.Err() != nil {
			break
		}
		ids, err := r.providerRelated(ctx, seed.Kind, seed.TmdbID)
		if err != nil {
			slog.Debug("[recommend] provider related failed",
				"kind", seed.Kind, "tmdb_id", seed.TmdbID, "error", err)
			continue
		}
		weight := seed.Weight
		if weight <= 0 {
			weight = 1
		}
		for rank, id := range ids {
			if id <= 0 {
				continue
			}
			// A title backed by several seeds outranks one backed by a single
			// seed, and an early provider position is worth more than a late one:
			// 1/(rank+1) decays quickly, which keeps the aggregate from
			// flattening into "everything any seed mentioned".
			score[id] += weight / float64(rank+1)
		}
	}

	ranked := make([]int, 0, len(score))
	for id := range score {
		ranked = append(ranked, id)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if score[ranked[i]] != score[ranked[j]] {
			return score[ranked[i]] > score[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

// providerRelated asks TMDB for one title's related list.
func (r *Resolver) providerRelated(ctx context.Context, kind string, tmdbID int) ([]int, error) {
	if kind == "show" {
		resp, err := r.TMDB.GetShowRecommendationsContext(ctx, tmdbID, 1)
		if err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(resp.Results))
		for _, item := range resp.Results {
			ids = append(ids, item.ID)
		}
		return ids, nil
	}

	resp, err := r.TMDB.GetMovieRecommendationsContext(ctx, tmdbID, 1)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(resp.Results))
	for _, item := range resp.Results {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

// LocalIDsForTmdb maps provider ids onto catalogue rows of one kind, keeping the
// provider's order. Ids this catalogue does not hold are dropped: a card the
// instance cannot render is not a recommendation.
func (r *Resolver) LocalIDsForTmdb(ctx context.Context, kind string, tmdbIDs []int) ([]int64, error) {
	if len(tmdbIDs) == 0 || r.DB == nil {
		return nil, nil
	}

	type row struct {
		ID     int64
		TmdbID int
	}
	byTmdb := make(map[int]int64, len(tmdbIDs))
	rows := make([]row, 0, len(tmdbIDs))
	var err error
	if kind == "show" {
		err = r.DB.WithContext(ctx).
			Model(&struct{}{}).
			Table("shows").
			Select("id, tmdb_id").
			Where("tmdb_id IN ?", tmdbIDs).
			Scan(&rows).Error
	} else {
		err = r.DB.WithContext(ctx).
			Model(&struct{}{}).
			Table("movies").
			Select("id, tmdb_id").
			Where("tmdb_id IN ?", tmdbIDs).
			Scan(&rows).Error
	}
	if err != nil {
		return nil, err
	}
	for _, item := range rows {
		byTmdb[item.TmdbID] = item.ID
	}

	ids := make([]int64, 0, len(tmdbIDs))
	for _, tmdbID := range tmdbIDs {
		if local, ok := byTmdb[tmdbID]; ok {
			ids = append(ids, local)
		}
	}
	return ids, nil
}

// DefaultResolver returns a process-wide resolver built from the environment,
// or nil when no metadata provider is configured.
//
// Handler packages need the provider without holding a resolver of their own;
// a nil answer is the normal case on a self-hosted box with no token, and every
// caller treats it as "answer from the catalogue".
func DefaultResolver() *Resolver {
	defaultOnce.Do(func() {
		client, err := tmdb.New()
		if err != nil {
			return
		}
		db, err := database.GetDB()
		if err != nil {
			return
		}
		defaultResolver = NewResolver(db, client, nil)
	})
	return defaultResolver
}
