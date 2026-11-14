package recommend

import (
	"encoding/json"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
)

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
		Order("media_id ASC, \"order\" ASC").
		Find(&credits).Error; err != nil {
		return nil, err
	}

	// Crew first, then cast — the same policy leadPeople uses for the seed.
	// Filling the per-title cap from whichever credit sorted first would let a
	// six-billed cast push out the director, and a title sharing the seed's
	// director would then lose that signal.
	add := func(mediaID int64, personID int64) {
		if personID == 0 || len(result[mediaID]) >= perTitle {
			return
		}
		result[mediaID] = append(result[mediaID], personID)
	}
	for _, credit := range credits {
		if credit.CreditType != "crew" {
			continue
		}
		switch credit.Job {
		case "Director", "Creator", "Writer", "Screenplay":
			add(credit.MediaID, credit.PersonID)
		}
	}
	for _, credit := range credits {
		if credit.CreditType != "cast" {
			continue
		}
		add(credit.MediaID, credit.PersonID)
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
	// The seen set is excluded in SQL, not only in Go: with the filter applied
	// after LIMIT, an account that has already dealt with the whole matched pool
	// would come back empty and be reported as a cold start. GORM renders an
	// empty slice as NULL in NOT IN, which matches nothing — hence the length
	// guard rather than an unconditional clause.
	if seen := profile.seenIDs(); len(seen) > 0 {
		query = query.Where("id NOT IN ?", seen)
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
