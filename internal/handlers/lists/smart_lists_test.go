package lists

import (
	"strings"
	"testing"

	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestApplyUserFiltersReferencesTheOuterTable guards the correlation name in the
// watched/collected subqueries. They used to interpolate the media TYPE ("movie"),
// but the outer query selects the TABLE ("movies") with no alias, so PostgreSQL
// rejected `movie.id` — and because the filter is only built when the caller asks
// for watched/collected, a smart list without those filters never noticed.
func TestApplyUserFiltersReferencesTheOuterTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	cases := []struct {
		mediaType string
		table     string
		watcher   string
	}{
		{"movie", "movies", "user_history"},
		{"show", "shows", "user_history"},
	}
	for _, tc := range cases {
		t.Run(tc.mediaType, func(t *testing.T) {
			query := applyUserFilters(
				db.Table(tc.table),
				database.JSONMap{"watched": "all", "collected": "none"},
				"user-1",
				tc.mediaType,
			)
			// DryRun compiles the statement without touching the (empty) table.
			var rows []database.ListItem
			stmt := query.Session(&gorm.Session{DryRun: true}).Find(&rows).Statement
			sql := stmt.SQL.String()

			if !strings.Contains(sql, tc.table+".id") {
				t.Errorf("subquery does not reference %s.id: %s", tc.table, sql)
			}
			// The old code interpolated the TYPE, not the table.
			singular := tc.mediaType + ".id"
			if strings.Contains(sql, singular) {
				t.Errorf("subquery still references the type name %q: %s", singular, sql)
			}
			if len(stmt.Vars) < 2 || stmt.Vars[0] != "user-1" || stmt.Vars[1] != tc.mediaType {
				t.Errorf("media_type must stay the bound TYPE value, got vars %v", stmt.Vars)
			}
		})
	}
}
