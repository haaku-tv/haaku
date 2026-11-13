package contractshape

import (
	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/gorm"
)

// Media objects for the item shapes a sync/collection read returns.
//
// A Trakt item is a row envelope (rank, listed_at, rating, …) plus the media
// object it points at, nested under the media type's own key:
//
//	{"rank": 1, "id": 42, "listed_at": "…", "type": "movie", "movie": {…}}
//
// The row envelope belongs to the handler that owns the collection; the nested
// object is this package's job, because it has to be the SAME object the media
// endpoints serve or a client cannot use an item it was handed.

// MediaRef identifies one media row the contract renders as an object: the
// stored media_type ("movie", "show", "season", "episode") and the LOCAL row id
// (never an external id).
type MediaRef struct {
	Type string
	ID   int64
}

// MediaObjects renders media references in the contract's object shape with at
// most one query per media type (plus one for the seasons an episode needs), so
// a page referencing many rows is not one query per row.
//
// A reference whose row no longer exists is absent from the result: the caller
// omits the dangling item rather than fabricating a placeholder.
func MediaObjects(db *gorm.DB, refs []MediaRef) map[MediaRef]map[string]any {
	out := make(map[MediaRef]map[string]any, len(refs))
	byType := map[string][]int64{}
	for _, ref := range refs {
		if ref.ID != 0 {
			byType[ref.Type] = append(byType[ref.Type], ref.ID)
		}
	}
	if len(byType) == 0 {
		return out
	}

	movies := loadMovieIDs(db, byType["movie"])
	shows := loadShowIDs(db, byType["show"])
	seasons := loadSeasonIDs(db, byType["season"])
	episodes := loadEpisodeIDs(db, byType["episode"])

	// An episode object carries its season's number, so an episode-only page
	// still needs one query for the seasons it references.
	episodeSeasons := make([]int64, 0, len(episodes))
	for _, ep := range episodes {
		episodeSeasons = append(episodeSeasons, ep.SeasonID)
	}
	for id, season := range loadSeasonIDs(db, episodeSeasons) {
		seasons[id] = season
	}

	for _, ref := range refs {
		switch ref.Type {
		case "movie":
			if m, ok := movies[ref.ID]; ok {
				m := m
				out[ref] = Movie(&m)
			}
		case "show":
			if s, ok := shows[ref.ID]; ok {
				s := s
				out[ref] = Show(&s)
			}
		case "season":
			if s, ok := seasons[ref.ID]; ok {
				s := s
				out[ref] = Season(&s)
			}
		case "episode":
			if e, ok := episodes[ref.ID]; ok {
				e := e
				out[ref] = Episode(&e, seasons[e.SeasonID].SeasonNumber)
			}
		}
	}
	return out
}

// MediaObject renders a single media row; ok is false when the row is gone.
func MediaObject(db *gorm.DB, mediaType string, mediaID int64) (map[string]any, bool) {
	ref := MediaRef{Type: mediaType, ID: mediaID}
	row, ok := MediaObjects(db, []MediaRef{ref})[ref]
	return row, ok
}

// EpisodeShowIDs maps each episode row id to its parent show id, in one query.
// An episode object does not carry its show, but a contract history entry does,
// so the caller resolves the parents for a whole page at once.
func EpisodeShowIDs(db *gorm.DB, episodeIDs []int64) map[int64]int64 {
	out := make(map[int64]int64, len(episodeIDs))
	ids := dedupeIDs(episodeIDs)
	if len(ids) == 0 {
		return out
	}
	var rows []database.Episode
	if db.Select("id", "show_id").Where("id IN ?", ids).Find(&rows).Error != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = rows[i].ShowID
	}
	return out
}

// loadMovieIDs batch-loads movies by id. A query error yields an empty map, and
// the caller then omits those items — the same behaviour as a missing row.
func loadMovieIDs(db *gorm.DB, ids []int64) map[int64]database.Movie {
	out := make(map[int64]database.Movie, len(ids))
	ids = dedupeIDs(ids)
	if len(ids) == 0 {
		return out
	}
	var rows []database.Movie
	if db.Where("id IN ?", ids).Find(&rows).Error != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = rows[i]
	}
	return out
}

// loadShowIDs batch-loads shows by id.
func loadShowIDs(db *gorm.DB, ids []int64) map[int64]database.Show {
	out := make(map[int64]database.Show, len(ids))
	ids = dedupeIDs(ids)
	if len(ids) == 0 {
		return out
	}
	var rows []database.Show
	if db.Where("id IN ?", ids).Find(&rows).Error != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = rows[i]
	}
	return out
}

// loadSeasonIDs batch-loads seasons by id.
func loadSeasonIDs(db *gorm.DB, ids []int64) map[int64]database.Season {
	out := make(map[int64]database.Season, len(ids))
	ids = dedupeIDs(ids)
	if len(ids) == 0 {
		return out
	}
	var rows []database.Season
	if db.Where("id IN ?", ids).Find(&rows).Error != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = rows[i]
	}
	return out
}

// loadEpisodeIDs batch-loads episodes by id.
func loadEpisodeIDs(db *gorm.DB, ids []int64) map[int64]database.Episode {
	out := make(map[int64]database.Episode, len(ids))
	ids = dedupeIDs(ids)
	if len(ids) == 0 {
		return out
	}
	var rows []database.Episode
	if db.Where("id IN ?", ids).Find(&rows).Error != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = rows[i]
	}
	return out
}

// dedupeIDs drops duplicates and the zero id so an IN list stays small and can
// never match the zero row.
func dedupeIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
