package media

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/haaku-tv/haaku/internal/contractshape"
	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// --- Response types for media API ---

// IDs is the standard ID object returned with every media response.
//
// `trakt` is the contract's required integer id and carries THIS instance's row
// id. Every row here is addressable by it, the write surfaces already accept
// `ids.trakt` as the pointer (a comment target, a list item), and a client that
// only knows the contract shape can therefore open a title without knowing the
// shorter `id` key — which stays, holding the same value, because older
// responses and the web client's own types use it.
//
// `slug` is required by the contract's ids object, so it is emitted even when
// the row has none; an empty string is the contract's own "no slug".
type IDs struct {
	ID    int64  `json:"id"`
	Trakt int64  `json:"trakt"`
	Slug  string `json:"slug"`
	TMDB  int    `json:"tmdb,omitempty"`
	IMDB  string `json:"imdb,omitempty"`
	TVDB  string `json:"tvdb,omitempty"`
}

// RatingSummary is the community rating summary.
type RatingSummary struct {
	Rating       float64          `json:"rating"`
	Votes        int64            `json:"votes"`
	Distribution map[string]int64 `json:"distribution"`
}

// MovieResponse is the API response shape for a movie.
//
// The fields above `// Full extended fields` are the ones a card needs; the
// rest are the contract's movie object. The detail route serves the full set
// without a query parameter, because a page that has to ask twice for its own
// header renders half of it on the first paint.
type MovieResponse struct {
	IDs           IDs            `json:"ids"`
	Title         string         `json:"title"`
	Year          int            `json:"year"`
	Images        map[string]any `json:"images"`
	RatingSummary *RatingSummary `json:"rating_summary,omitempty"`

	// Always served: the row holds them and the page reads them.
	Released              string   `json:"released,omitempty"`
	OriginalTitle         string   `json:"original_title,omitempty"`
	Rating                float64  `json:"rating"`
	Votes                 int64    `json:"votes"`
	CommentCount          int64    `json:"comment_count"`
	Trailer               string   `json:"trailer,omitempty"`
	Homepage              string   `json:"homepage,omitempty"`
	Country               string   `json:"country,omitempty"`
	AvailableTranslations []string `json:"available_translations"`

	// Full extended fields
	Overview      string   `json:"overview,omitempty"`
	Tagline       string   `json:"tagline,omitempty"`
	Runtime       int      `json:"runtime,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	Certification string   `json:"certification,omitempty"`
	Language      string   `json:"language,omitempty"`
	TrailerURL    string   `json:"trailer_url,omitempty"`
	Status        string   `json:"status,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`

	// Metadata extended
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ShowResponse is the API response shape for a show.
type ShowResponse struct {
	IDs           IDs            `json:"ids"`
	Title         string         `json:"title"`
	Year          int            `json:"year"`
	Images        map[string]any `json:"images"`
	RatingSummary *RatingSummary `json:"rating_summary,omitempty"`

	// Always served (see MovieResponse).
	Rating                float64  `json:"rating"`
	Votes                 int64    `json:"votes"`
	CommentCount          int64    `json:"comment_count"`
	Trailer               string   `json:"trailer,omitempty"`
	Homepage              string   `json:"homepage,omitempty"`
	Country               string   `json:"country,omitempty"`
	AiredEpisodes         int      `json:"aired_episodes,omitempty"`
	AvailableTranslations []string `json:"available_translations"`

	// Full extended fields
	Overview      string   `json:"overview,omitempty"`
	Tagline       string   `json:"tagline,omitempty"`
	Runtime       int      `json:"runtime,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	Certification string   `json:"certification,omitempty"`
	Language      string   `json:"language,omitempty"`
	Network       string   `json:"network,omitempty"`
	Status        string   `json:"status,omitempty"`
	EpisodeCount  int      `json:"episode_count,omitempty"`
	SeasonCount   int      `json:"season_count,omitempty"`
	FirstAired    string   `json:"first_aired,omitempty"`
	LastAired     string   `json:"last_aired,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`

	// Metadata extended
	Metadata map[string]any `json:"metadata,omitempty"`

	// Show always includes seasons array
	Seasons []SeasonResponse `json:"seasons"`
}

// SeasonResponse is the API response shape for a season. `number` and `ids`
// are the contract's required keys; `season_number` is kept beside `number`
// because the season route's path segment and the row's own column use it.
type SeasonResponse struct {
	IDs           IDs            `json:"ids"`
	Number        int            `json:"number"`
	ShowID        int64          `json:"show_id"`
	SeasonNumber  int            `json:"season_number"`
	EpisodeCount  int            `json:"episode_count"`
	AiredEpisodes int            `json:"aired_episodes,omitempty"`
	Title         string         `json:"title,omitempty"`
	Overview      string         `json:"overview,omitempty"`
	FirstAired    string         `json:"first_aired,omitempty"`
	Rating        float64        `json:"rating"`
	Votes         int64          `json:"votes"`
	Images        map[string]any `json:"images"`
	RatingSummary *RatingSummary `json:"rating_summary,omitempty"`
	AiredAt       string         `json:"aired_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
}

// EpisodeResponse is the API response shape for an episode. `season`, `number`
// and `ids` are the contract's required keys; the flat `episode_number` and the
// row pointers are kept for the internal payloads that join on them.
type EpisodeResponse struct {
	IDs            IDs            `json:"ids"`
	Season         int            `json:"season"`
	Number         int            `json:"number"`
	NumberAbs      *int           `json:"number_abs,omitempty"`
	SeasonID       int64          `json:"season_id"`
	ShowID         int64          `json:"show_id"`
	EpisodeNumber  int            `json:"episode_number"`
	Title          string         `json:"title"`
	Overview       string         `json:"overview,omitempty"`
	Runtime        int            `json:"runtime,omitempty"`
	Rating         float64        `json:"rating"`
	Votes          int64          `json:"votes"`
	CommentCount   int64          `json:"comment_count"`
	Images         map[string]any `json:"images"`
	RatingSummary  *RatingSummary `json:"rating_summary,omitempty"`
	FirstAired     string         `json:"first_aired,omitempty"`
	AiredAt        string         `json:"aired_at,omitempty"`
	AbsoluteNumber *int           `json:"absolute_number,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
}

// PersonResponse is the API response shape for a person.
type PersonResponse struct {
	IDs                IDs            `json:"ids"`
	Name               string         `json:"name"`
	Biography          string         `json:"biography,omitempty"`
	Birthday           string         `json:"birthday,omitempty"`
	Deathday           string         `json:"deathday,omitempty"`
	Gender             int            `json:"gender"`
	KnownForDepartment string         `json:"known_for_department,omitempty"`
	Images             map[string]any `json:"images"`
	Credits            *PersonCredits `json:"credits,omitempty"`
}

// PersonCredits holds movie and show credits for a person.
type PersonCredits struct {
	Cast []CreditResponse `json:"cast,omitempty"`
	Crew []CreditResponse `json:"crew,omitempty"`
}

// CreditResponse is the API response shape for a credit.
type CreditResponse struct {
	MediaType     string `json:"media_type"`
	MediaID       int64  `json:"media_id"`
	Title         string `json:"title,omitempty"`
	Year          int    `json:"year,omitempty"`
	CreditType    string `json:"credit_type"`
	Job           string `json:"job,omitempty"`
	Character     string `json:"character,omitempty"`
	Department    string `json:"department,omitempty"`
	SeriesRegular bool   `json:"series_regular,omitempty"`
	EpisodeCount  int    `json:"episode_count,omitempty"`
}

// PeopleResponse is the response for cast+crew on a movie or show.
type PeopleResponse struct {
	Cast []CastMemberResponse `json:"cast,omitempty"`
	Crew []CrewMemberResponse `json:"crew,omitempty"`
}

// CastMemberResponse is a cast member entry.
type CastMemberResponse struct {
	Person    PersonResponse `json:"person"`
	Character string         `json:"character"`
	Order     int            `json:"order"`
}

// CrewMemberResponse is a crew member entry.
type CrewMemberResponse struct {
	Person     PersonResponse `json:"person"`
	Job        string         `json:"job"`
	Department string         `json:"department"`
}

// StatsResponse is the Trakt stats response shape. collected_episodes is only
// set for shows (movies omit the key entirely).
type StatsResponse struct {
	Watchers          int64  `json:"watchers"`
	Plays             int64  `json:"plays"`
	Collectors        int64  `json:"collectors"`
	CollectedEpisodes *int64 `json:"collected_episodes,omitempty"`
	Comments          int64  `json:"comments"`
	Lists             int64  `json:"lists"`
	Votes             int64  `json:"votes"`
	Favorited         int64  `json:"favorited"`
	Recommended       *int64 `json:"recommended"`
}

// WatchingResponse is a stub — returns empty array for now.
type WatchingResponse []interface{}

// ListsResponse is a stub — returns empty array for now.
type ListsResponse []interface{}

// --- Builders ---

// mediaRef is a (media_type, media_id) key used to look up external_ids rows
// in a single batched query.
type mediaRef struct {
	mediaType string
	mediaID   int64
}

// batchExternalIDs fetches the slug/tvdb external_ids rows for a batch of
// media refs in one query, keyed by ref. This replaces the per-`buildIDs`
// external_ids N+1 (one query per item) with a single query per request batch.
// Unresolvable refs are simply absent from the returned map; failures degrade
// to an empty map (best-effort, matching the old buildIDs behavior of leaving
// slug/tvdb empty on query error).
func batchExternalIDs(db *gorm.DB, refs []mediaRef) map[mediaRef][]database.ExternalID {
	result := make(map[mediaRef][]database.ExternalID, len(refs))
	if db == nil || len(refs) == 0 {
		return result
	}

	seen := make(map[mediaRef]bool, len(refs))
	pairs := make([][]interface{}, 0, len(refs))
	for _, r := range refs {
		if r.mediaID == 0 {
			continue
		}
		if !seen[r] {
			seen[r] = true
			pairs = append(pairs, []interface{}{r.mediaType, r.mediaID})
		}
	}
	if len(pairs) == 0 {
		return result
	}

	var exts []database.ExternalID
	if err := db.Where("(media_type, media_id) IN ? AND source IN ?",
		pairs, []string{"slug", "tvdb"}).Find(&exts).Error; err != nil {
		return result
	}
	for _, ext := range exts {
		ref := mediaRef{mediaType: ext.MediaType, mediaID: ext.MediaID}
		if seen[ref] {
			result[ref] = append(result[ref], ext)
		}
	}
	return result
}

// mediaBatchLookup fetches external_ids for a single media ref via the batched
// path (one query), returning a lookup keyed by that ref. DB-unavailable returns nil.
func mediaBatchLookup(mediaType string, mediaID int64) map[mediaRef][]database.ExternalID {
	if db, err := database.GetDB(); err == nil {
		return batchExternalIDs(db, []mediaRef{{mediaType: mediaType, mediaID: mediaID}})
	}
	return nil
}

// buildIDsFromLookup builds an IDs object using a pre-fetched external_ids
// batch (no extra query). TMDB/IMDB come from the movie/show rows; slug and
// TVDB come from the lookup.
func buildIDsFromLookup(lookup map[mediaRef][]database.ExternalID, mediaType string, mediaID int64, movie *database.Movie, show *database.Show) IDs {
	ids := IDs{ID: mediaID, Trakt: mediaID}

	if movie != nil {
		if movie.TmdbID != 0 {
			ids.TMDB = movie.TmdbID
		}
		if movie.ImdbID != "" {
			ids.IMDB = movie.ImdbID
		}
	} else if show != nil {
		if show.TmdbID != 0 {
			ids.TMDB = show.TmdbID
		}
		if show.ImdbID != "" {
			ids.IMDB = show.ImdbID
		}
	}

	for _, ext := range lookup[mediaRef{mediaType: mediaType, mediaID: mediaID}] {
		switch ext.Source {
		case "slug":
			ids.Slug = ext.ExternalID
		case "tvdb":
			ids.TVDB = ext.ExternalID
		}
	}

	return ids
}

// BuildMovieResponse builds a MovieResponse from a database Movie (exported for cross-package use).
func BuildMovieResponse(m *database.Movie, extended string) MovieResponse {
	return buildMovieResponse(mediaBatchLookup("movie", m.ID), m, extended)
}

func buildMovieResponse(lookup map[mediaRef][]database.ExternalID, m *database.Movie, extended string) MovieResponse {
	year := 0
	if m.Released != nil && !m.Released.IsZero() {
		year = m.Released.Year()
	}

	resp := MovieResponse{
		IDs:           buildIDsFromLookup(lookup, "movie", m.ID, m, nil),
		Title:         m.Title,
		Year:          year,
		Images:        contractshape.MediaImages("movie", m.Images),
		RatingSummary: buildRatingSummary(m.RatingAvg, m.RatingCount),
		// The contract's own header fields, always present: a detail page reads
		// these without asking for an extended level.
		OriginalTitle:         m.OriginalTitle,
		Released:              m.Released.String(),
		Rating:                m.RatingAvg,
		Votes:                 m.RatingCount,
		Trailer:               m.TrailerURL,
		Homepage:              metadataString(m.Metadata, "homepage"),
		Country:               metadataString(m.Metadata, "country"),
		AvailableTranslations: []string{},
		UpdatedAt:             m.UpdatedAt.UTC().Format(time.RFC3339),
	}

	// Apply extended level.
	switch extended {
	case "full", "all":
		resp.Overview = m.Overview
		resp.Tagline = m.Tagline
		resp.Runtime = m.Runtime
		resp.Genres = m.Genres
		resp.Certification = m.Certification
		resp.Language = m.Language
		resp.TrailerURL = m.TrailerURL
		resp.Status = m.Status
		fallthrough
	case "metadata":
		resp.Metadata = m.Metadata
	case "images":
		// Images are always included for now (already sent above).
	}

	// "all" includes everything
	if extended == "all" {
		resp.Overview = m.Overview
		resp.Tagline = m.Tagline
		resp.Runtime = m.Runtime
		resp.Genres = m.Genres
		resp.Certification = m.Certification
		resp.Language = m.Language
		resp.TrailerURL = m.TrailerURL
		resp.Status = m.Status
		resp.Metadata = m.Metadata
	}

	return resp
}

// BuildShowResponse builds a ShowResponse from a database Show (exported for cross-package use).
func BuildShowResponse(s *database.Show, extended string) ShowResponse {
	return buildShowResponse(mediaBatchLookup("show", s.ID), s, extended)
}

func buildShowResponse(lookup map[mediaRef][]database.ExternalID, s *database.Show, extended string) ShowResponse {
	year := 0
	if s.FirstAired != nil && !s.FirstAired.IsZero() {
		year = s.FirstAired.Year()
	}

	resp := ShowResponse{
		IDs:           buildIDsFromLookup(lookup, "show", s.ID, nil, s),
		Title:         s.Title,
		Year:          year,
		Images:        contractshape.MediaImages("show", s.Images),
		RatingSummary: buildRatingSummary(s.RatingAvg, s.RatingCount),
		// Header fields a detail page reads (see MovieResponse).
		Rating:                s.RatingAvg,
		Votes:                 s.RatingCount,
		Trailer:               metadataString(s.Metadata, "trailer"),
		Homepage:              metadataString(s.Metadata, "homepage"),
		Country:               metadataString(s.Metadata, "country"),
		AiredEpisodes:         s.EpisodeCount,
		AvailableTranslations: []string{},
		UpdatedAt:             s.UpdatedAt.UTC().Format(time.RFC3339),
	}

	switch extended {
	case "full", "all":
		resp.Overview = s.Overview
		resp.Genres = s.Genres
		resp.Certification = s.Certification
		resp.Language = s.Language
		resp.Network = s.Network
		resp.Status = s.Status
		resp.EpisodeCount = s.EpisodeCount
		resp.SeasonCount = s.SeasonCount
		if s.FirstAired != nil {
			resp.FirstAired = s.FirstAired.String()
		}
		if s.LastAired != nil {
			resp.LastAired = s.LastAired.String()
		}
		fallthrough
	case "metadata":
		resp.Metadata = s.Metadata
	case "images":
		// images always included
	}

	if extended == "all" {
		resp.Overview = s.Overview
		resp.Genres = s.Genres
		resp.Certification = s.Certification
		resp.Language = s.Language
		resp.Network = s.Network
		resp.Status = s.Status
		resp.EpisodeCount = s.EpisodeCount
		resp.SeasonCount = s.SeasonCount
		if s.FirstAired != nil {
			resp.FirstAired = s.FirstAired.String()
		}
		if s.LastAired != nil {
			resp.LastAired = s.LastAired.String()
		}
		resp.Metadata = s.Metadata
	}

	return resp
}

// BuildSeasonResponse builds a SeasonResponse from a database Season.
func BuildSeasonResponse(s *database.Season) SeasonResponse {
	return buildSeasonResponse(mediaBatchLookup("season", s.ID), s)
}

func buildSeasonResponse(lookup map[mediaRef][]database.ExternalID, s *database.Season) SeasonResponse {
	resp := SeasonResponse{
		IDs:          buildIDsFromLookup(lookup, "season", s.ID, nil, nil),
		Number:       s.SeasonNumber,
		ShowID:       s.ShowID,
		SeasonNumber: s.SeasonNumber,
		EpisodeCount: s.EpisodeCount,
		Title:        metadataString(s.Metadata, "title"),
		Overview:     metadataString(s.Metadata, "overview"),
		UpdatedAt:    s.UpdatedAt.UTC().Format(time.RFC3339),
		// The contract's season images object requires poster and thumb, so the
		// full key set is emitted even when the row holds neither.
		Images: contractshape.MediaImages("season", s.Images),
	}
	if s.AiredAt != nil {
		resp.AiredAt = s.AiredAt.String()
		resp.FirstAired = s.AiredAt.String()
	}
	return resp
}

// BuildEpisodeResponse builds an EpisodeResponse from a database Episode.
//
// The row alone cannot say which season NUMBER it belongs to (it holds the
// season's row id), so this looks the season up — one query per call. Use
// BuildEpisodeResponses for a list.
func BuildEpisodeResponse(e *database.Episode) EpisodeResponse {
	resp := buildEpisodeResponse(mediaBatchLookup("episode", e.ID), e)
	if db, err := database.GetDB(); err == nil {
		if number, ok := seasonNumbers(db, []int64{e.SeasonID})[e.SeasonID]; ok {
			resp.Season = number
		}
	}
	return resp
}

// BuildEpisodeResponses builds a list in two queries: one for the seasons the
// episodes belong to and one batch for their external ids. The per-item builder
// would run both once per episode.
func BuildEpisodeResponses(episodes []database.Episode) []EpisodeResponse {
	out := make([]EpisodeResponse, 0, len(episodes))
	if len(episodes) == 0 {
		return out
	}
	seasonIDs := make([]int64, 0, len(episodes))
	refs := make([]mediaRef, 0, len(episodes))
	for i := range episodes {
		seasonIDs = append(seasonIDs, episodes[i].SeasonID)
		refs = append(refs, mediaRef{mediaType: "episode", mediaID: episodes[i].ID})
	}
	numbers := map[int64]int{}
	var lookup map[mediaRef][]database.ExternalID
	if db, err := database.GetDB(); err == nil {
		numbers = seasonNumbers(db, seasonIDs)
		lookup = batchExternalIDs(db, refs)
	}
	for i := range episodes {
		resp := buildEpisodeResponse(lookup, &episodes[i])
		resp.Season = numbers[episodes[i].SeasonID]
		out = append(out, resp)
	}
	return out
}

// seasonNumbers resolves season row ids to their season numbers in one query.
// An unknown id is simply absent from the map.
func seasonNumbers(db *gorm.DB, ids []int64) map[int64]int {
	out := make(map[int64]int, len(ids))
	if db == nil || len(ids) == 0 {
		return out
	}
	var rows []struct {
		ID           int64
		SeasonNumber int
	}
	if err := db.Table("seasons").Select("id, season_number").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		out[row.ID] = row.SeasonNumber
	}
	return out
}

func buildEpisodeResponse(lookup map[mediaRef][]database.ExternalID, e *database.Episode) EpisodeResponse {
	resp := EpisodeResponse{
		IDs:            buildIDsFromLookup(lookup, "episode", e.ID, nil, nil),
		Number:         e.EpisodeNumber,
		NumberAbs:      e.AbsoluteNumber,
		SeasonID:       e.SeasonID,
		ShowID:         e.ShowID,
		EpisodeNumber:  e.EpisodeNumber,
		Title:          e.Title,
		Overview:       e.Overview,
		Runtime:        e.Runtime,
		Images:         contractshape.MediaImages("episode", e.Images),
		AbsoluteNumber: e.AbsoluteNumber,
		UpdatedAt:      e.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if e.AiredAt != nil {
		resp.AiredAt = e.AiredAt.String()
		resp.FirstAired = e.AiredAt.String()
	}
	return resp
}

// metadataString reads a string out of a row's metadata blob, returning "" when
// the key is absent or is not a string. It exists so the response builders can
// serve a contract field the row keeps in `metadata` (a show has no column for
// its homepage) without a nil-check at every call site.
func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}
	return value
}

// BuildPersonResponse builds a PersonResponse from a database Person.
func BuildPersonResponse(p *database.Person) PersonResponse {
	return buildPersonResponse(mediaBatchLookup("person", p.ID), p)
}

func buildPersonResponse(lookup map[mediaRef][]database.ExternalID, p *database.Person) PersonResponse {
	ids := buildIDsFromLookup(lookup, "person", p.ID, nil, nil)
	// buildIDs only reads movie/show rows for TMDB/IMDB, so add the person's
	// own external ids here to keep parity with the fields on the row.
	if p.TmdbID != 0 {
		ids.TMDB = p.TmdbID
	}
	if p.ImdbID != "" {
		ids.IMDB = p.ImdbID
	}
	resp := PersonResponse{
		IDs:                ids,
		Name:               p.Name,
		Biography:          p.Biography,
		Gender:             p.Gender,
		KnownForDepartment: p.KnownForDepartment,
		Images:             contractshape.MediaImages("person", p.Images),
	}
	if p.Birthday != nil {
		resp.Birthday = p.Birthday.String()
	}
	if p.Deathday != nil {
		resp.Deathday = p.Deathday.String()
	}
	return resp
}

// emptyRatingDistribution is Trakt's histogram: every bucket 1..10 is present,
// including the ones with no votes.
func emptyRatingDistribution() map[string]int64 {
	distribution := make(map[string]int64, 10)
	for i := 1; i <= 10; i++ {
		distribution[strconv.Itoa(i)] = 0
	}
	return distribution
}

// buildRatingSummary builds a RatingSummary from avg and count.
func buildRatingSummary(avg float64, count int64) *RatingSummary {
	return &RatingSummary{
		Rating:       avg,
		Votes:        count,
		Distribution: emptyRatingDistribution(),
	}
}

// MarshalJSON ensures maps are never nil.
func (r MovieResponse) MarshalJSON() ([]byte, error) {
	type Alias MovieResponse
	a := Alias(r)
	if a.Images == nil {
		a.Images = contractshape.MediaImages("movie", nil)
	}
	if a.Genres == nil {
		a.Genres = make([]string, 0)
	}
	return json.Marshal(a)
}

// MarshalJSON ensures maps are never nil.
func (r ShowResponse) MarshalJSON() ([]byte, error) {
	type Alias ShowResponse
	a := Alias(r)
	if a.Images == nil {
		a.Images = contractshape.MediaImages("show", nil)
	}
	if a.Genres == nil {
		a.Genres = make([]string, 0)
	}
	if a.Seasons == nil {
		a.Seasons = make([]SeasonResponse, 0)
	}
	return json.Marshal(a)
}

// MarshalJSON ensures maps are never nil.
func (r PersonResponse) MarshalJSON() ([]byte, error) {
	type Alias PersonResponse
	a := Alias(r)
	if a.Images == nil {
		a.Images = contractshape.MediaImages("person", nil)
	}
	return json.Marshal(a)
}

// GetExtended returns the extended level from query params, defaulting to "min".
func GetExtended(c *fiber.Ctx) string {
	ext := c.Query("extended", "min")
	switch ext {
	case "min", "full", "metadata", "images", "all":
		return ext
	default:
		return "min"
	}
}

// getExtended is the internal alias for GetExtended.
func getExtended(c *fiber.Ctx) string { return GetExtended(c) }

// detailExtended is getExtended for a DETAIL route: an explicit `?extended=`
// wins, and the default is full. A detail page reads the overview, runtime,
// rating, artwork and credits on its first paint, so making it ask for
// `extended=full` only moves the cost into a second round trip — and every
// client that forgets renders a header with half its fields missing. List
// routes keep the minimal default; they serve many rows and a card needs little.
func detailExtended(c *fiber.Ctx) string {
	if extended := getExtended(c); extended != "min" {
		return extended
	}
	return "full"
}
