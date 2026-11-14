// Package recommend answers two questions from the catalogue an instance
// already holds: what else is like this title, and what should this account
// watch next.
//
// It is deliberately a catalogue engine, not a provider one. A self-hosted
// haaku usually has no metadata token, and a recommendation row that only works
// after a TMDB call does not work at all on those boxes. Everything here reads
// rows that are already in the database; the provider is used elsewhere, when a
// title is first ingested.
package recommend

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
)

// Tuning. The three terms are added, so the weights decide which signal can
// carry a row: taste shape beats raw quality, and a shared lead actor counts for
// half of a shared genre without ever outranking it.
const (
	weightGenre   = 1.0
	weightPeople  = 0.55
	weightQuality = 0.45

	// poolSize bounds how many catalogue rows one answer is scored against. A
	// home row is 20 cards; scoring a few hundred candidates is enough to rank
	// them well and keeps the query plan simple on a Raspberry Pi.
	poolSize = 600

	// maxPrefilterGenres is how many of the strongest genres are pushed into the
	// SQL prefilter. Widening it stops narrowing anything: the point is to skip
	// an obviously irrelevant shelf of the catalogue, not to be exhaustive.
	maxPrefilterGenres = 4
)

// Item is one ranked row.
type Item struct {
	Kind  string
	ID    int64
	Score float64
}

// Profile is what an account (or a single title) likes, as far as the catalogue
// can tell.
type Profile struct {
	// Genres maps a genre name to how strongly this profile wants it. A rating
	// below the midpoint contributes a negative weight: the engine should steer
	// away from a genre the account rated 2/10, not average it in.
	Genres map[string]float64
	// People maps a person row id to how strongly their involvement matters.
	People map[int64]float64
	// seen holds the ids the caller already has an opinion about.
	seen map[int64]struct{}

	genreTotal  float64
	peopleTotal float64
}

// NewProfile returns an empty profile ready to be filled.
func NewProfile() *Profile {
	return &Profile{
		Genres: make(map[string]float64),
		People: make(map[int64]float64),
		seen:   make(map[int64]struct{}),
	}
}

func (p *Profile) addGenres(genres []string, weight float64) {
	for _, genre := range genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		p.Genres[genre] += weight
	}
}

func (p *Profile) addPeople(ids []int64, weight float64) {
	for _, id := range ids {
		p.People[id] += weight
	}
}

func (p *Profile) markSeen(id int64) {
	if id > 0 {
		p.seen[id] = struct{}{}
	}
}

func (p *Profile) hasSeen(id int64) bool {
	_, ok := p.seen[id]
	return ok
}

// finish normalises the totals the scorer divides by, and drops genres that
// ended up at zero after the positive and negative signals cancelled out.
func (p *Profile) finish() {
	for genre, weight := range p.Genres {
		if weight <= 0 {
			delete(p.Genres, genre)
			continue
		}
		p.genreTotal += weight
	}
	for id, weight := range p.People {
		if weight <= 0 {
			delete(p.People, id)
			continue
		}
		p.peopleTotal += weight
	}
}

// Empty reports whether the profile carries no taste at all, which is the case
// for an account that has not watched, saved or rated anything yet.
func (p *Profile) Empty() bool {
	return len(p.Genres) == 0 && len(p.People) == 0
}

// TopGenres returns the strongest genres, strongest first, at most n of them.
func (p *Profile) TopGenres(n int) []string {
	type pair struct {
		genre  string
		weight float64
	}
	pairs := make([]pair, 0, len(p.Genres))
	for genre, weight := range p.Genres {
		pairs = append(pairs, pair{genre, weight})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].weight != pairs[j].weight {
			return pairs[i].weight > pairs[j].weight
		}
		return pairs[i].genre < pairs[j].genre
	})
	if n > 0 && len(pairs) > n {
		pairs = pairs[:n]
	}
	genres := make([]string, 0, len(pairs))
	for _, item := range pairs {
		genres = append(genres, item.genre)
	}
	return genres
}

// score rates one candidate against the profile.
func (p *Profile) score(genres []string, people []int64, rating float64, votes int64) float64 {
	score := weightQuality * quality(rating, votes)

	if p.genreTotal > 0 {
		var match float64
		for _, genre := range genres {
			if weight, ok := p.Genres[strings.TrimSpace(genre)]; ok {
				match += weight
			}
		}
		if match > p.genreTotal {
			match = p.genreTotal
		}
		score += weightGenre * (match / p.genreTotal)
	}

	if p.peopleTotal > 0 {
		var match float64
		for _, id := range people {
			if weight, ok := p.People[id]; ok {
				match += weight
			}
		}
		if match > p.peopleTotal {
			match = p.peopleTotal
		}
		score += weightPeople * (match / p.peopleTotal)
	}

	return score
}

// quality turns a rating and its vote count into 0..1.
//
// A 9.0 from three votes is not the same claim as a 9.0 from three thousand, so
// the rating is scaled by a confidence factor that reaches 1 at fifty votes.
// A title nobody has rated is not penalised to zero — it is simply unknown, and
// sits at a middling value so it can still be found through its genres.
func quality(rating float64, votes int64) float64 {
	if rating <= 0 {
		return 0.35
	}
	confidence := math.Min(1, float64(votes)/50)
	if votes == 0 {
		confidence = 0.35
	}
	return (rating / 10) * (0.35 + 0.65*confidence)
}

// ---------------------------------------------------------------------------
// Catalogue access
// ---------------------------------------------------------------------------

// kindTable maps a media kind to its table and model.
func kindValid(kind string) bool {
	return kind == "movie" || kind == "show"
}

// mediaRow is the part of a movie or show row the scorer needs.
type mediaRow struct {
	ID          int64
	GenresJSON  string `gorm:"column:genres_json"`
	RatingAvg   float64
	RatingCount int64
	Popularity  float64
}

// genres decodes the JSON column. The engine reads it as text rather than as
// the model's typed slice: a plain Scan into a struct field does not go through
// the type's sql.Scanner, and both dialects hand the column over as text.
func (r mediaRow) genres() []string {
	if strings.TrimSpace(r.GenresJSON) == "" {
		return nil
	}
	var genres []string
	if err := json.Unmarshal([]byte(r.GenresJSON), &genres); err != nil {
		return nil
	}
	return genres
}

// catalogRows loads the genre/rating columns for the given ids of one kind.
func catalogRows(db *gorm.DB, kind string, ids []int64) ([]mediaRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []mediaRow
	if kind == "show" {
		if err := db.Model(&database.Show{}).
			Select("id, genres AS genres_json, rating_avg, rating_count, popularity").
			Where("id IN ?", ids).
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		return rows, nil
	}
	if err := db.Model(&database.Movie{}).
		Select("id, genres AS genres_json, rating_avg, rating_count, popularity").
		Where("id IN ?", ids).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// leadPeople returns the person ids that most identify a title: the top of the
// cast, plus the director or creator and the writers.
func leadPeople(db *gorm.DB, kind string, id int64, limit int) ([]int64, error) {
	var credits []database.Credit
	if err := db.Where("media_type = ? AND media_id = ?", kind, id).
		Order("credit_type ASC, \"order\" ASC").
		Find(&credits).Error; err != nil {
		return nil, err
	}

	people := make([]int64, 0, limit)
	seen := make(map[int64]struct{}, limit)
	add := func(personID int64) {
		if personID == 0 {
			return
		}
		if _, ok := seen[personID]; ok {
			return
		}
		seen[personID] = struct{}{}
		people = append(people, personID)
	}

	// Crew first — a director or creator says more about a title than the fourth
	// billed actor — then the billed cast.
	for _, credit := range credits {
		if credit.CreditType != "crew" {
			continue
		}
		switch credit.Job {
		case "Director", "Creator", "Writer", "Screenplay":
			add(credit.PersonID)
		}
	}
	for _, credit := range credits {
		if credit.CreditType != "cast" {
			continue
		}
		add(credit.PersonID)
	}
	if len(people) > limit {
		people = people[:limit]
	}
	return people, nil
}

// peopleForTitles loads the lead people of several titles at once, keyed by
// media id, so a profile built from fifty watched titles costs one query.
func peopleForTitles(db *gorm.DB, kind string, ids []int64, perTitle int) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var credits []database.Credit
	if err := db.Where("media_type = ? AND media_id IN ?", kind, ids).
		Order("media_id ASC, credit_type ASC, \"order\" ASC").
		Find(&credits).Error; err != nil {
		return nil, err
	}
	for _, credit := range credits {
		bucket := result[credit.MediaID]
		if len(bucket) >= perTitle {
			continue
		}
		if credit.CreditType == "crew" {
			switch credit.Job {
			case "Director", "Creator", "Writer", "Screenplay":
			default:
				continue
			}
		}
		result[credit.MediaID] = append(bucket, credit.PersonID)
	}
	return result, nil
}

// matchAnyGenre narrows a query to rows carrying at least one of the genres.
//
// PostgreSQL answers from the jsonb column with one containment test per genre;
// sqlite (the handler suites) matches the serialised JSON. The prefilter is an
// optimisation only — an empty result is not taken as "nothing matches", the
// caller scores whatever comes back and a wide pool simply means no filter.
func matchAnyGenre(query *gorm.DB, genres []string) *gorm.DB {
	conditions := make([]string, 0, len(genres))
	args := make([]any, 0, len(genres))
	dialect := query.Dialector
	postgres := dialect != nil && dialect.Name() == "postgres"

	for _, genre := range genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		if postgres {
			encoded, err := json.Marshal([]string{genre})
			if err != nil {
				continue
			}
			conditions = append(conditions, "genres @> ?::jsonb")
			args = append(args, string(encoded))
			continue
		}
		conditions = append(conditions, "genres LIKE ?")
		args = append(args, "%\""+genre+"\"%")
	}
	if len(conditions) == 0 {
		return query
	}
	return query.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

// candidateRows loads the pool the scorer ranks: the most popular rows of one
// kind, optionally pre-narrowed to the profile's strongest genres.
func candidateRows(db *gorm.DB, kind string, profile *Profile, excludeID int64) ([]mediaRow, error) {
	query := db.Model(&database.Movie{}).Select("id, genres AS genres_json, rating_avg, rating_count, popularity")
	if kind == "show" {
		query = db.Model(&database.Show{}).Select("id, genres AS genres_json, rating_avg, rating_count, popularity")
	}

	if genres := profile.TopGenres(maxPrefilterGenres); len(genres) > 0 {
		query = matchAnyGenre(query, genres)
	}
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}

	var rows []mediaRow
	if err := query.Order("popularity DESC").Order("id ASC").Limit(poolSize).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// rank scores the pool, drops the rows the profile has already seen, and returns
// the best limit items. Ties break on popularity and then id, so the same
// catalogue always produces the same row.
func rank(rows []mediaRow, popularity map[int64]float64, profile *Profile, people map[int64][]int64, limit int) []Item {
	scored := make([]Item, 0, len(rows))
	for _, row := range rows {
		if profile.hasSeen(row.ID) {
			continue
		}
		item := Item{
			ID:    row.ID,
			Score: profile.score(row.genres(), people[row.ID], row.RatingAvg, row.RatingCount),
		}
		scored = append(scored, item)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		pi, pj := popularity[scored[i].ID], popularity[scored[j].ID]
		if pi != pj {
			return pi > pj
		}
		return scored[i].ID < scored[j].ID
	})

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// ---------------------------------------------------------------------------
// Public entry points
// ---------------------------------------------------------------------------

// Related ranks the titles most like one title of the same kind.
func Related(db *gorm.DB, kind string, id int64, limit int) ([]Item, error) {
	if !kindValid(kind) {
		return nil, fmt.Errorf("recommend: unknown kind %q", kind)
	}
	if limit <= 0 {
		limit = 10
	}

	seeds, err := catalogRows(db, kind, []int64{id})
	if err != nil || len(seeds) == 0 {
		return nil, err
	}

	lead, err := leadPeople(db, kind, id, 6)
	if err != nil {
		return nil, err
	}

	profile := NewProfile()
	profile.addGenres(seeds[0].genres(), 1)
	profile.addPeople(lead, 1)
	profile.markSeen(id)
	profile.finish()

	if profile.Empty() {
		return []Item{}, nil
	}

	rows, err := candidateRows(db, kind, profile, id)
	if err != nil {
		return nil, err
	}
	// Popularity is only a tie-break, never a score term: a title is not more
	// like another because more people watched it.
	popularity := make(map[int64]float64, len(rows))
	for _, row := range rows {
		popularity[row.ID] = row.Popularity
	}

	people, err := peopleForTitles(db, kind, idsOf(rows), 6)
	if err != nil {
		return nil, err
	}
	return rank(rows, popularity, profile, people, limit), nil
}

// Personal ranks unseen titles for an account from everything it has an
// opinion about. The second return value reports whether the answer came from
// taste at all — false means the account is new and the caller got the
// catalogue's popular titles instead.
func Personal(db *gorm.DB, userID, kind string, limit int) ([]Item, bool, error) {
	if !kindValid(kind) {
		return nil, false, fmt.Errorf("recommend: unknown kind %q", kind)
	}
	if limit <= 0 {
		limit = 20
	}

	profile, err := tasteProfile(db, userID, kind)
	if err != nil {
		return nil, false, err
	}

	if profile.Empty() {
		return popular(db, kind, profile, limit)
	}

	rows, err := candidateRows(db, kind, profile, 0)
	if err != nil {
		return nil, true, err
	}
	popularity := make(map[int64]float64, len(rows))
	for _, row := range rows {
		popularity[row.ID] = row.Popularity
	}
	people, err := peopleForTitles(db, kind, idsOf(rows), 6)
	if err != nil {
		return nil, true, err
	}
	items := rank(rows, popularity, profile, people, limit)
	if len(items) == 0 {
		// The profile is real but the catalogue under it holds nothing new; a
		// short popular row beats an empty one.
		return popular(db, kind, profile, limit)
	}
	return items, true, nil
}

// popular is the cold-start answer: what this instance watches most, minus what
// the account has already dealt with.
func popular(db *gorm.DB, kind string, profile *Profile, limit int) ([]Item, bool, error) {
	query := db.Model(&database.Movie{}).Select("id, popularity")
	if kind == "show" {
		query = db.Model(&database.Show{}).Select("id, popularity")
	}
	var rows []mediaRow
	if err := query.Order("popularity DESC").Order("id ASC").Limit(poolSize).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	items := make([]Item, 0, limit)
	for _, row := range rows {
		if profile.hasSeen(row.ID) {
			continue
		}
		items = append(items, Item{Kind: kind, ID: row.ID, Score: row.Popularity})
		if len(items) == limit {
			break
		}
	}
	return items, false, nil
}

// tasteProfile builds the account's profile for one kind from its whole
// recorded opinion: what it watched, what it wants to watch, what it loved and
// what it rated.
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
