package recommend

import (
	"sort"

	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
)

func tasteProfile(db *gorm.DB, userID, kind string) (*Profile, error) {
	profile := NewProfile()

	watched, err := watchedIDs(db, userID, kind)
	if err != nil {
		return nil, err
	}
	for _, id := range watched {
		profile.markSeen(id)
	}

	watchlist, err := idsFrom(db, &database.UserWatchlist{}, userID, kind)
	if err != nil {
		return nil, err
	}
	for _, id := range watchlist {
		profile.markSeen(id)
	}

	favorites, err := idsFrom(db, &database.UserFavorite{}, userID, kind)
	if err != nil {
		return nil, err
	}
	for _, id := range favorites {
		profile.markSeen(id)
	}

	ratings, err := ratingsFor(db, userID, kind)
	if err != nil {
		return nil, err
	}
	for id := range ratings {
		profile.markSeen(id)
	}

	hidden, err := hiddenIDs(db, userID, kind)
	if err != nil {
		return nil, err
	}
	for _, id := range hidden {
		profile.markSeen(id)
	}

	// Weighting: a favourite is the strongest positive statement the account can
	// make, a rating is proportional to how far from the midpoint it sits, and a
	// watch or a watchlist entry is a plain "more of this".
	type source struct {
		ids    []int64
		weight float64
	}
	sources := []source{
		{watched, 1.0},
		{watchlist, 1.0},
		{favorites, 1.6},
	}

	// Genres from every recorded row, in one query per kind.
	all := make([]int64, 0, len(watched)+len(watchlist)+len(favorites)+len(ratings))
	all = append(all, watched...)
	all = append(all, watchlist...)
	all = append(all, favorites...)
	for id := range ratings {
		all = append(all, id)
	}
	rows, err := catalogRows(db, kind, all)
	if err != nil {
		return nil, err
	}
	genresByID := make(map[int64][]string, len(rows))
	for _, row := range rows {
		genresByID[row.ID] = row.genres()
	}

	for _, src := range sources {
		for _, id := range src.ids {
			profile.addGenres(genresByID[id], src.weight)
		}
	}
	// A rating says as much by being low as by being high: 10/10 adds 1.0,
	// 5/10 adds nothing, 1/10 subtracts 0.8.
	for id, rating := range ratings {
		profile.addGenres(genresByID[id], float64(rating-5)/5)
	}

	people, err := peopleForTitles(db, kind, all, 6)
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		for _, id := range src.ids {
			profile.addPeople(people[id], src.weight*0.5)
		}
	}

	profile.finish()
	return profile, nil
}

// watchedIDs returns the media ids the account has watched for one kind. A show
// is watched through its episodes, so episode history is resolved to the show.
func watchedIDs(db *gorm.DB, userID, kind string) ([]int64, error) {
	if kind == "show" {
		var ids []int64
		err := db.Model(&database.UserHistory{}).
			Where("user_id = ? AND media_type = ?", userID, "show").
			Distinct("media_id").
			Pluck("media_id", &ids).Error
		if err != nil {
			return nil, err
		}
		var episodeShowIDs []int64
		err = db.Model(&database.Episode{}).
			Joins("JOIN user_history ON user_history.media_id = episodes.id").
			Where("user_history.user_id = ? AND user_history.media_type = ?", userID, "episode").
			Distinct("episodes.show_id").
			Pluck("episodes.show_id", &episodeShowIDs).Error
		if err != nil {
			return nil, err
		}
		return append(ids, episodeShowIDs...), nil
	}

	var ids []int64
	err := db.Model(&database.UserHistory{}).
		Where("user_id = ? AND media_type = ?", userID, kind).
		Distinct("media_id").
		Pluck("media_id", &ids).Error
	return ids, err
}

// idsFrom reads the media ids of one account list for one kind.
func idsFrom(db *gorm.DB, model any, userID, kind string) ([]int64, error) {
	var ids []int64
	err := db.Model(model).
		Where("user_id = ? AND media_type = ?", userID, kind).
		Pluck("media_id", &ids).Error
	return ids, err
}

// ratingsFor reads the account's scores for one kind.
func ratingsFor(db *gorm.DB, userID, kind string) (map[int64]int, error) {
	type row struct {
		MediaID int64
		Rating  int
	}
	var rows []row
	if err := db.Model(&database.UserRating{}).
		Select("media_id, rating").
		Where("user_id = ? AND media_type = ?", userID, kind).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	ratings := make(map[int64]int, len(rows))
	for _, r := range rows {
		ratings[r.MediaID] = r.Rating
	}
	return ratings, nil
}

// hiddenIDs reads what the account has dismissed from its recommendations.
func hiddenIDs(db *gorm.DB, userID, kind string) ([]int64, error) {
	var ids []int64
	err := db.Model(&database.UserHidden{}).
		Where("user_id = ? AND media_type = ? AND section = ?", userID, kind, HiddenSection).
		Pluck("media_id", &ids).Error
	return ids, err
}

// HiddenSection is the user_hidden section the recommendation rows honour.
const HiddenSection = "recommendations"

// idsOf collects the id column of a pool.
func idsOf(rows []mediaRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// ---------------------------------------------------------------------------
// Provider backbone helpers
// ---------------------------------------------------------------------------

// Seed is one catalogue row the metadata provider can be asked to expand.
type Seed struct {
	ID     int64
	TmdbID int
	Weight float64
}

// Seeds returns the account's strongest signals for one kind, best first, with
// the provider ids needed to ask TMDB what belongs next to them.
//
// This is what makes the provider a backbone rather than a guess: the seeds come
// from what the account actually did, and TMDB answers "these belong together"
// for a catalogue that has no other users to learn from.
func Seeds(db *gorm.DB, userID, kind string, limit int) ([]Seed, error) {
	if limit <= 0 {
		limit = maxRecommendationSeeds
	}

	watched, err := watchedIDs(db, userID, kind)
	if err != nil {
		return nil, err
	}
	watchlist, err := idsFrom(db, &database.UserWatchlist{}, userID, kind)
	if err != nil {
		return nil, err
	}
	favorites, err := idsFrom(db, &database.UserFavorite{}, userID, kind)
	if err != nil {
		return nil, err
	}
	ratings, err := ratingsFor(db, userID, kind)
	if err != nil {
		return nil, err
	}

	weights := make(map[int64]float64, len(watched)+len(watchlist)+len(favorites)+len(ratings))
	for _, id := range watched {
		weights[id] += 1.0
	}
	for _, id := range watchlist {
		weights[id] += 1.2
	}
	for _, id := range favorites {
		weights[id] += 1.6
	}
	for id, rating := range ratings {
		weights[id] += float64(rating-5) / 2.5
	}

	ids := make([]int64, 0, len(weights))
	for id, weight := range weights {
		if weight <= 0 {
			continue
		}
		ids = append(ids, id)
	}

	// Fetch the provider ids for the candidates in one query.
	type tmdbRow struct {
		ID     int64
		TmdbID int
	}
	var rows []tmdbRow
	table := "movies"
	if kind == "show" {
		table = "shows"
	}
	if len(ids) > 0 {
		if err := db.Table(table).Select("id, tmdb_id").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
			return nil, err
		}
	}

	seeds := make([]Seed, 0, len(rows))
	for _, row := range rows {
		if row.TmdbID <= 0 {
			continue
		}
		seeds = append(seeds, Seed{ID: row.ID, TmdbID: row.TmdbID, Weight: weights[row.ID]})
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		if seeds[i].Weight != seeds[j].Weight {
			return seeds[i].Weight > seeds[j].Weight
		}
		return seeds[i].ID < seeds[j].ID
	})
	if len(seeds) > limit {
		seeds = seeds[:limit]
	}
	return seeds, nil
}

// SeenIDs is everything the account already has an opinion about for one kind:
// watched, saved, favourited, rated or dismissed. Provider picks are filtered
// through it so the TMDB backbone cannot recommend what is already on the shelf.
func SeenIDs(db *gorm.DB, userID, kind string) (map[int64]struct{}, error) {
	profile, err := tasteProfile(db, userID, kind)
	if err != nil {
		return nil, err
	}
	return profile.seen, nil
}

// maxRecommendationSeeds bounds how many seeds are taken when the caller does
// not say.
const maxRecommendationSeeds = 5
