package database

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// SearchResult wraps a single search hit with its type and payload.
//
// List hits are absent on purpose: a list is only searchable once it has an
// audience (an instance-shared list, see the list-sharing issue), so a `list`
// type is accepted by the route and answered from nothing rather than shipped
// as a permanently empty pill.
type SearchResult struct {
	Type    string   `json:"type"`
	Movie   *Movie   `json:"movie,omitempty"`
	Show    *Show    `json:"show,omitempty"`
	Episode *Episode `json:"episode,omitempty"`
	Person  *Person  `json:"person,omitempty"`
}

// SearchParams holds the validated search query parameters.
type SearchParams struct {
	Query  string
	Types  []string // e.g. ["movie", "show"]
	Page   int
	Limit  int
	Fields []string
	// Genres narrows the result to rows whose genres include every name here.
	// A search with genres and no text is a browse, and is served by popularity.
	Genres []string
}

// SearchResponse is the paginated search response envelope.
const (
	// defaultSearchLimit is the page size when a caller names none.
	defaultSearchLimit = 20
	// maxSearchWindow bounds how many rows one search may materialise per type.
	// A self-hosted catalogue is not a search engine: past this the caller is
	// paging through a result set nobody scrolls.
	maxSearchWindow = int64(5000)
)

type SearchResponse struct {
	Results    []SearchResult `json:"results"`
	TotalItems int64          `json:"total_items"`
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	TotalPages int            `json:"total_pages"`
}

// sanitizeTsQuery escapes single quotes in a search query for safe use
// inside a plainto_tsquery() PostgreSQL function call string literal.
func sanitizeTsQuery(q string) string {
	return strings.ReplaceAll(q, "'", "''")
}

// tsQuerySQL returns a parameterized WHERE clause and its argument for
// full-text search matching across the search_vector tsvector column.
func tsQuerySQL(query string) (string, string) {
	tsquery := fmt.Sprintf("plainto_tsquery('english', '%s')", sanitizeTsQuery(query))
	return "search_vector @@ " + tsquery, tsquery
}

// SearchMovies performs full-text search on movies using tsvector + tsquery.
func SearchMovies(db *gorm.DB, params SearchParams) ([]Movie, int64, error) {
	var movies []Movie
	var total int64

	query := narrowSearch(db.Model(&Movie{}), params)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count movies: %w", err)
	}

	offset := (params.Page - 1) * params.Limit
	if err := query.Offset(offset).Limit(params.Limit).Find(&movies).Error; err != nil {
		return nil, 0, fmt.Errorf("search movies: %w", err)
	}

	return movies, total, nil
}

// SearchShows performs full-text search on shows using tsvector + tsquery.
func SearchShows(db *gorm.DB, params SearchParams) ([]Show, int64, error) {
	var shows []Show
	var total int64

	query := narrowSearch(db.Model(&Show{}), params)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count shows: %w", err)
	}

	offset := (params.Page - 1) * params.Limit
	if err := query.Offset(offset).Limit(params.Limit).Find(&shows).Error; err != nil {
		return nil, 0, fmt.Errorf("search shows: %w", err)
	}

	return shows, total, nil
}

// SearchEpisodes performs full-text search on episodes using tsvector + tsquery.
func SearchEpisodes(db *gorm.DB, params SearchParams) ([]Episode, int64, error) {
	var episodes []Episode
	var total int64

	whereClause, rankClause := tsQuerySQL(params.Query)

	query := db.Model(&Episode{}).
		Where(whereClause).
		Order("ts_rank(search_vector, " + rankClause + ") DESC")

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count episodes: %w", err)
	}

	offset := (params.Page - 1) * params.Limit
	if err := query.Offset(offset).Limit(params.Limit).Find(&episodes).Error; err != nil {
		return nil, 0, fmt.Errorf("search episodes: %w", err)
	}

	return episodes, total, nil
}

// SearchPeople performs full-text search on people using tsvector + tsquery.
func SearchPeople(db *gorm.DB, params SearchParams) ([]Person, int64, error) {
	var people []Person
	var total int64

	whereClause, rankClause := tsQuerySQL(params.Query)

	query := db.Model(&Person{}).
		Where(whereClause).
		Order("ts_rank(search_vector, " + rankClause + ") DESC")

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count people: %w", err)
	}

	offset := (params.Page - 1) * params.Limit
	if err := query.Offset(offset).Limit(params.Limit).Find(&people).Error; err != nil {
		return nil, 0, fmt.Errorf("search people: %w", err)
	}

	return people, total, nil
}

// SearchMulti performs a multi-type full-text search and returns unified results.
// Results are sorted by relevance (ts_rank), but pagination is per-type for simplicity
// in the initial implementation. Each type is fetched independently and combined.
func SearchMulti(db *gorm.DB, params SearchParams) (*SearchResponse, error) {
	var results []SearchResult
	var totalItems int64

	page := params.Page
	if page < 1 {
		page = 1
	}
	limit := params.Limit
	if limit < 1 {
		limit = defaultSearchLimit
	}

	// Each type is fetched with the window the caller asked for. Passing page 1
	// and slicing the combined rows afterwards answered every page from the
	// first page's rows — a second page whose rows happened to sit past the
	// first window came back empty while the total said otherwise.
	//
	// That window is bounded. `page * limit` sizes the fetch, so an unbounded
	// page asks each type for millions of rows just to slice all but the last
	// few away, and a large enough page overflows int on the way. Past the cap
	// the rows are not wanted at all: the counts are still read (one row per
	// type) so the answer keeps its total, and the results come back empty
	// because the requested page sits outside what this endpoint will
	// materialise.
	offset := int64(page) * int64(limit)
	beyondWindow := offset > maxSearchWindow
	windowSize := int(offset)
	if beyondWindow {
		windowSize = 1
	}

	window := SearchParams{
		Query:  params.Query,
		Page:   1,
		Limit:  windowSize,
		Fields: params.Fields,
		// Genres ride with the window so the per-type searches filter in SQL
		// instead of the rows being fetched and discarded here.
		Genres: params.Genres,
	}

	for _, typ := range params.Types {
		switch strings.ToLower(typ) {
		case "movie":
			movies, count, err := SearchMovies(db, window)
			if err != nil {
				return nil, err
			}
			totalItems += count
			for i := range movies {
				results = append(results, SearchResult{Type: "movie", Movie: &movies[i]})
			}
		case "show":
			shows, count, err := SearchShows(db, window)
			if err != nil {
				return nil, err
			}
			totalItems += count
			for i := range shows {
				results = append(results, SearchResult{Type: "show", Show: &shows[i]})
			}
		case "episode":
			// Catalogue-only by nature: the metadata provider has no episode
			// search endpoint, so an episode is findable once its show has been
			// ingested and not before. The rows used to be counted and dropped.
			episodes, count, err := SearchEpisodes(db, window)
			if err != nil {
				return nil, err
			}
			totalItems += count
			for i := range episodes {
				results = append(results, SearchResult{Type: "episode", Episode: &episodes[i]})
			}
		case "person":
			people, count, err := SearchPeople(db, window)
			if err != nil {
				return nil, err
			}
			totalItems += count
			for i := range people {
				results = append(results, SearchResult{Type: "person", Person: &people[i]})
			}
		}
	}

	// Apply pagination as a slice window over combined results.
	totalPages := 1
	if limit > 0 {
		totalPages = (int(totalItems) + limit - 1) / limit
	}
	if beyondWindow {
		// The window was not materialised beyond the first row of each type, so
		// there is nothing to slice: the page the caller asked for sits past
		// everything this endpoint will read. The totals above are real.
		return &SearchResponse{
			Results:    []SearchResult{},
			TotalItems: totalItems,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		}, nil
	}
	start := (page - 1) * limit
	end := start + limit
	if start > len(results) {
		start = len(results)
	}
	if end > len(results) {
		end = len(results)
	}

	return &SearchResponse{
		Results:    results[start:end],
		TotalItems: totalItems,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
	}, nil
}

// narrowSearch applies a search's text and genre constraints to a media query.
//
// The text clause is the full-text index; when the caller sent no text it is
// omitted rather than matched against an empty tsquery (which matches nothing),
// so "genre only" is a browse in popularity order instead of an empty page.
func narrowSearch(query *gorm.DB, params SearchParams) *gorm.DB {
	if strings.TrimSpace(params.Query) != "" {
		whereClause, rankClause := tsQuerySQL(params.Query)
		query = query.Where(whereClause).
			Order("ts_rank(search_vector, " + rankClause + ") DESC")
	} else {
		query = query.Order("popularity DESC")
	}
	return narrowByGenres(query, params.Genres)
}

// narrowByGenres keeps only rows whose genres array carries every requested
// name.
//
// PostgreSQL answers from the jsonb column with a containment test. The handler
// suites run on sqlite, where the same column is text, so the fallback matches
// the serialised JSON instead of failing with "no such operator".
func narrowByGenres(query *gorm.DB, genres []string) *gorm.DB {
	if len(genres) == 0 {
		return query
	}
	postgres := query.Dialector != nil && query.Dialector.Name() == "postgres"
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
			query = query.Where("genres @> ?::jsonb", string(encoded))
			continue
		}
		query = query.Where("genres LIKE ?", "%\""+genre+"\"%")
	}
	return query
}

// SearchExact performs an exact title match (case-insensitive) on movies or shows.
// Returns a single result or an error if not found. Candidates provided on 404.
func SearchExactMovie(db *gorm.DB, title string) (*Movie, error) {
	var movie Movie
	normalized := strings.TrimSpace(strings.ToLower(title))

	err := db.Where("LOWER(title) = ?", normalized).First(&movie).Error
	if err == nil {
		return &movie, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("exact search movie: %w", err)
	}

	return nil, gorm.ErrRecordNotFound
}

// SearchExactShow performs an exact title match on shows.
func SearchExactShow(db *gorm.DB, title string) (*Show, error) {
	var show Show
	normalized := strings.TrimSpace(strings.ToLower(title))

	err := db.Where("LOWER(title) = ?", normalized).First(&show).Error
	if err == nil {
		return &show, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("exact search show: %w", err)
	}

	return nil, gorm.ErrRecordNotFound
}

// SuggestCandidates returns up to 5 partial matches (ILIKE) for a given title and type.
func SuggestCandidates(db *gorm.DB, mediaType, title string, limit int) ([]string, error) {
	var titles []string
	pattern := "%" + strings.ToLower(title) + "%"

	var query *gorm.DB
	switch mediaType {
	case "movie":
		query = db.Model(&Movie{}).Where("LOWER(title) LIKE ?", pattern)
	case "show":
		query = db.Model(&Show{}).Where("LOWER(title) LIKE ?", pattern)
	default:
		return nil, nil
	}

	if limit <= 0 {
		limit = 5
	}
	if err := query.Limit(limit).Pluck("title", &titles).Error; err != nil {
		return nil, fmt.Errorf("suggest candidates %s: %w", mediaType, err)
	}
	return titles, nil
}

// LookupExternal finds a media object by external ID (e.g. imdb, tmdb).
// Returns the media type, media ID, and an error if not found.
func LookupExternal(db *gorm.DB, idType, externalID string) (string, int64, error) {
	var ext ExternalID
	err := db.Where("source = ? AND external_id = ?", idType, externalID).First(&ext).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", 0, gorm.ErrRecordNotFound
		}
		return "", 0, fmt.Errorf("lookup external id: %w", err)
	}
	return ext.MediaType, ext.MediaID, nil
}

// LookupByExternal resolves a full media object from an external ID.
func LookupByExternal(db *gorm.DB, idType, externalID string) (*SearchResult, error) {
	mediaType, mediaID, err := LookupExternal(db, idType, externalID)
	if err != nil {
		return nil, err
	}

	switch mediaType {
	case "movie":
		var movie Movie
		if err := db.First(&movie, mediaID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil, gorm.ErrRecordNotFound
			}
			return nil, fmt.Errorf("lookup movie: %w", err)
		}
		return &SearchResult{Type: "movie", Movie: &movie}, nil

	case "show":
		var show Show
		if err := db.First(&show, mediaID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil, gorm.ErrRecordNotFound
			}
			return nil, fmt.Errorf("lookup show: %w", err)
		}
		return &SearchResult{Type: "show", Show: &show}, nil

	default:
		return nil, fmt.Errorf("unknown media type: %s", mediaType)
	}
}
