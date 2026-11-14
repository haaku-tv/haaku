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
