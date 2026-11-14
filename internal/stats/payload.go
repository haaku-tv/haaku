package stats

import (
	"fmt"
	"strconv"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Contract-shaped user stats (issue #98)
//
// GET /v1/users/{id}/stats used to answer flat totals (plays/minutes/unique
// counts/ratings_count). The contract this API is written against declares a
// nested object instead: per-type blocks for movies/shows/seasons/episodes plus
// a network block and a ratings total with a 1..10 distribution. A client that
// renders a profile stats panel reads those nested fields, so the flat shape
// left it showing zeros for every per-type number.
//
// Everything here is an aggregate over rows that already exist (user_history,
// user_collection, user_ratings, comments, user_follows) — nothing is
// materialised, and the cached/version-keyed/user_stats-row machinery in
// ComputeUserStats is untouched.
// ---------------------------------------------------------------------------

// MediaStats is the contract's block for movies and episodes: lifetime plays,
// distinct items watched, minutes watched, collection size, and how many
// ratings and comments the user left on that type.
type MediaStats struct {
	Plays     int64 `json:"plays"`
	Minutes   int64 `json:"minutes"`
	Watched   int64 `json:"watched"`
	Collected int64 `json:"collected"`
	Ratings   int64 `json:"ratings"`
	Comments  int64 `json:"comments"`
}

// ShowStats is the contract's shows block. It declares no plays and no minutes:
// a play is an episode play, and a show has no single runtime. Watched counts
// the distinct shows the user has at least one episode of in their history.
type ShowStats struct {
	Watched   int64 `json:"watched"`
	Collected int64 `json:"collected"`
	Ratings   int64 `json:"ratings"`
	Comments  int64 `json:"comments"`
}

// SeasonStats is the contract's seasons block: seasons can be rated and
// commented on but are not watchable or collectable items of their own.
type SeasonStats struct {
	Ratings  int64 `json:"ratings"`
	Comments int64 `json:"comments"`
}

// NetworkStats is the contract's network block: followers, following, and the
// mutual follows between them.
type NetworkStats struct {
	Friends   int64 `json:"friends"`
	Followers int64 `json:"followers"`
	Following int64 `json:"following"`
}

// RatingStats is the user's overall rating count plus the share of their
// ratings that landed in each 1..10 bucket. The distribution is a percentage
// (0-100), matching this API's media rating distributions (see the episode and
// season /ratings handlers), so 12.5 means an eighth of the user's ratings.
// All ten buckets are always present, zero included.
type RatingStats struct {
	Total        int64              `json:"total"`
	Distribution map[string]float64 `json:"distribution"`
}

// Payload is the response body of GET /v1/users/{id}/stats: exactly the object
// the contract declares. Every required block is always emitted; a user with no
// activity gets zeroed blocks rather than an empty (or partial) object, because
// a client that reads stats.movies.plays must find the key, not a missing path.
type Payload struct {
	Seasons  SeasonStats  `json:"seasons"`
	Shows    ShowStats    `json:"shows"`
	Movies   MediaStats   `json:"movies"`
	Episodes MediaStats   `json:"episodes"`
	Network  NetworkStats `json:"network"`
	Ratings  RatingStats  `json:"ratings"`
	// Lifetime totals over the per-type blocks. The contract declares both as
	// nullable; we always have a number, so we always send one.
	TotalPlays   int64 `json:"total_plays"`
	TotalMinutes int64 `json:"total_minutes"`
}

// DistributionBuckets is the number of buckets in a 1..10 rating scale.
const DistributionBuckets = 10

// EmptyPayload is the payload a user with no activity gets: zero in every
// counter and all ten distribution buckets present at zero.
func EmptyPayload() Payload {
	return Payload{Ratings: RatingStats{Distribution: emptyRatingDistribution()}}
}

// emptyRatingDistribution returns all ten buckets at 0 percent.
func emptyRatingDistribution() map[string]float64 {
	dist := make(map[string]float64, DistributionBuckets)
	for i := 1; i <= DistributionBuckets; i++ {
		dist[strconv.Itoa(i)] = 0
	}
	return dist
}

// ComputeUserStatsPayload aggregates the contract's stats object for one user
// from the stored rows. It is read-only: unlike ComputeUserStats it writes
// nothing, so callers can run it inside the stats advisory-lock transaction or
// straight off the request path.
func ComputeUserStatsPayload(db *gorm.DB, userID string) (Payload, error) {
	payload := EmptyPayload()

	movies, err := computeMovieStats(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}
	episodes, err := computeEpisodeStats(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}
	showsWatched, err := countWatchedShows(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}

	collected, err := countCollectedByType(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}
	ratings, err := countRatingsByType(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}
	comments, err := countCommentsByType(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}
	network, err := computeNetworkStats(db, userID)
	if err != nil {
		return EmptyPayload(), err
	}

	payload.Movies = movies
	payload.Movies.Collected = collected["movie"]
	payload.Movies.Comments = comments["movie"]
	payload.Movies.Ratings = ratings.total["movie"]

	payload.Episodes = episodes
	payload.Episodes.Collected = collected["episode"]
	payload.Episodes.Comments = comments["episode"]
	payload.Episodes.Ratings = ratings.total["episode"]

	payload.Shows = ShowStats{
		Watched:   showsWatched,
		Collected: collected["show"],
		Ratings:   ratings.total["show"],
		Comments:  comments["show"],
	}
	payload.Seasons = SeasonStats{
		Ratings:  ratings.total["season"],
		Comments: comments["season"],
	}
	payload.Network = network
	payload.Ratings.Total = ratings.overall
	payload.Ratings.Distribution = ratings.distribution

	payload.TotalPlays = payload.Movies.Plays + payload.Episodes.Plays
	payload.TotalMinutes = payload.Movies.Minutes + payload.Episodes.Minutes

	return payload, nil
}

// computeMovieStats aggregates the user's movie play history: one history row
// is one play, the distinct media_id is the number of movies watched, and the
// minutes come from the movie's own runtime.
func computeMovieStats(db *gorm.DB, userID string) (MediaStats, error) {
	var row struct {
		Plays   int64
		Watched int64
		Minutes int64
	}
	err := db.Raw(`
		SELECT
			COUNT(*) AS plays,
			COUNT(DISTINCT h.media_id) AS watched,
			COALESCE(SUM(COALESCE(m.runtime, 0)), 0) AS minutes
		FROM user_history h
		LEFT JOIN movies m ON m.id = h.media_id
		WHERE h.user_id = ? AND h.media_type = 'movie'
	`, userID).Scan(&row).Error
	if err != nil {
		return MediaStats{}, fmt.Errorf("movie history totals: %w", err)
	}
	return MediaStats{Plays: row.Plays, Watched: row.Watched, Minutes: row.Minutes}, nil
}

// computeEpisodeStats aggregates the user's episode play history. A play is one
// history row, an episode is watched once, and minutes come from the episode's
// runtime. The join matches either episode_id or media_id: the sync engine
// writes both, and the history row's media_id is always the episode id (see
// recommend.idsFrom), so an older row with a NULL episode_id still counts.
func computeEpisodeStats(db *gorm.DB, userID string) (MediaStats, error) {
	var row struct {
		Plays   int64
		Watched int64
		Minutes int64
	}
	err := db.Raw(`
		SELECT
			COUNT(*) AS plays,
			COUNT(DISTINCT COALESCE(h.episode_id, h.media_id)) AS watched,
			COALESCE(SUM(COALESCE(e.runtime, 0)), 0) AS minutes
		FROM user_history h
		LEFT JOIN episodes e ON e.id = COALESCE(h.episode_id, h.media_id)
		WHERE h.user_id = ? AND h.media_type = 'episode'
	`, userID).Scan(&row).Error
	if err != nil {
		return MediaStats{}, fmt.Errorf("episode history totals: %w", err)
	}
	return MediaStats{Plays: row.Plays, Watched: row.Watched, Minutes: row.Minutes}, nil
}

// countWatchedShows counts the distinct shows the user has an episode of in
// their history. History rows are per episode, so the show comes from the
// episode row rather than from the history row itself.
func countWatchedShows(db *gorm.DB, userID string) (int64, error) {
	var watched int64
	err := db.Raw(`
		SELECT COUNT(DISTINCT e.show_id)
		FROM user_history h
		JOIN episodes e ON e.id = COALESCE(h.episode_id, h.media_id)
		WHERE h.user_id = ? AND h.media_type = 'episode'
	`, userID).Scan(&watched).Error
	if err != nil {
		return 0, fmt.Errorf("shows watched: %w", err)
	}
	return watched, nil
}

// countCollectedByType counts the user's collection rows per media type. The
// collection holds movies, shows and episodes (the sync engine writes episode
// rows too), so one grouped query feeds all three blocks. A media type with no
// rows is absent from the map and reads as zero.
func countCollectedByType(db *gorm.DB, userID string) (map[string]int64, error) {
	type row struct {
		MediaType string
		Total     int64
	}
	var rows []row
	err := db.Raw(`
		SELECT media_type, COUNT(*) AS total
		FROM user_collection
		WHERE user_id = ?
		GROUP BY media_type
	`, userID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("collection counts: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.MediaType] = r.Total
	}
	return counts, nil
}

// countCommentsByType counts the comments the user left per media type. List
// comments have a NULL media_type; they are not any of the contract's per-type
// counts, so they are folded to the empty key and ignored by the caller.
func countCommentsByType(db *gorm.DB, userID string) (map[string]int64, error) {
	type row struct {
		MediaType string
		Total     int64
	}
	var rows []row
	err := db.Raw(`
		SELECT COALESCE(media_type, '') AS media_type, COUNT(*) AS total
		FROM comments
		WHERE user_id = ?
		GROUP BY COALESCE(media_type, '')
	`, userID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("comment counts: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		if r.MediaType == "" {
			continue
		}
		counts[r.MediaType] = r.Total
	}
	return counts, nil
}

// ratingTotals is the result of one pass over the user's ratings: per-type
// counts, the overall total, and the 1..10 distribution.
type ratingTotals struct {
	total        map[string]int64
	overall      int64
	distribution map[string]float64
}

// countRatingsByType counts the user's ratings per media type and builds the
// 1..10 distribution from the same rows, so the per-type counts and the
// distribution can never disagree.
func countRatingsByType(db *gorm.DB, userID string) (ratingTotals, error) {
	type row struct {
		MediaType string
		Rating    int
		Total     int64
	}
	var rows []row
	err := db.Raw(`
		SELECT media_type, rating, COUNT(*) AS total
		FROM user_ratings
		WHERE user_id = ?
		GROUP BY media_type, rating
	`, userID).Scan(&rows).Error
	if err != nil {
		return ratingTotals{}, fmt.Errorf("rating counts: %w", err)
	}

	out := ratingTotals{
		total:        make(map[string]int64, 4),
		distribution: emptyRatingDistribution(),
	}
	// Per-bucket counts first, so the shares can be computed from the true
	// total once every row has been read.
	buckets := make(map[int]int64, DistributionBuckets)
	for _, r := range rows {
		out.total[r.MediaType] += r.Total
		out.overall += r.Total
		if r.Rating >= 1 && r.Rating <= DistributionBuckets {
			buckets[r.Rating] += r.Total
		}
	}
	if out.overall == 0 {
		return out, nil
	}
	for rating, count := range buckets {
		out.distribution[strconv.Itoa(rating)] = float64(count) / float64(out.overall) * 100
	}
	return out, nil
}

// computeNetworkStats counts the user's followers, the accounts they follow,
// and their mutual follows (friends). Blocks are not subtracted here: the
// counts describe follow rows, and a blocked follow is dropped when it is
// unblocked rather than silently hidden from the user's own numbers.
func computeNetworkStats(db *gorm.DB, userID string) (NetworkStats, error) {
	var row struct {
		Followers int64
		Following int64
		Friends   int64
	}
	err := db.Raw(`
		SELECT
			(SELECT COUNT(*) FROM user_follows WHERE following_id = ?) AS followers,
			(SELECT COUNT(*) FROM user_follows WHERE follower_id = ?) AS following,
			(SELECT COUNT(*) FROM user_follows f
			   JOIN user_follows r
			     ON r.follower_id = f.following_id
			    AND r.following_id = f.follower_id
			  WHERE f.follower_id = ?) AS friends
	`, userID, userID, userID).Scan(&row).Error
	if err != nil {
		return NetworkStats{}, fmt.Errorf("network counts: %w", err)
	}
	return NetworkStats{Followers: row.Followers, Following: row.Following, Friends: row.Friends}, nil
}
