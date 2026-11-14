package stats

import "strconv"

// userStatsSchema is the OpenAPI shape of stats.Payload, which is the shape the
// contract declares for GET /users/{id}/stats: required per-type blocks
// (movies, shows, seasons, episodes), a network block and a ratings total with
// a 1..10 distribution, plus the nullable lifetime totals. It is declared as a
// schema map rather than reflected from the Go type so the generator emits
// exactly the contract's keys — the Go struct carries the same JSON tags, and
// TestUserStatsSchemaMatchesPayload fails if the two drift.
func userStatsSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":     "object",
		"required": []string{"seasons", "shows", "movies", "episodes", "network", "ratings"},
		"properties": map[string]interface{}{
			"seasons": map[string]interface{}{
				"type":     "object",
				"required": []string{"ratings", "comments"},
				"properties": map[string]interface{}{
					"ratings":  integerSchema(),
					"comments": integerSchema(),
				},
			},
			"shows": map[string]interface{}{
				"type":     "object",
				"required": []string{"watched", "collected", "ratings", "comments"},
				"properties": map[string]interface{}{
					"watched":   integerSchema(),
					"collected": integerSchema(),
					"ratings":   integerSchema(),
					"comments":  integerSchema(),
				},
			},
			"movies":   mediaStatsSchema(),
			"episodes": mediaStatsSchema(),
			"network": map[string]interface{}{
				"type":     "object",
				"required": []string{"friends", "followers", "following"},
				"properties": map[string]interface{}{
					"friends":   integerSchema(),
					"followers": integerSchema(),
					"following": integerSchema(),
				},
			},
			"ratings": map[string]interface{}{
				"type":     "object",
				"required": []string{"total", "distribution"},
				"properties": map[string]interface{}{
					"total":        integerSchema(),
					"distribution": ratingDistributionSchema(),
				},
			},
			// Lifetime totals over the per-type blocks. The contract declares
			// both as nullable integers; this API always answers a number.
			"total_plays":   integerSchema(),
			"total_minutes": integerSchema(),
		},
	}
}

// mediaStatsSchema is the movies/episodes block: plays and minutes only exist
// for watchable media, and both blocks carry the same six counters.
func mediaStatsSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":     "object",
		"required": []string{"plays", "minutes", "watched", "collected", "ratings", "comments"},
		"properties": map[string]interface{}{
			"plays":     integerSchema(),
			"minutes":   integerSchema(),
			"watched":   integerSchema(),
			"collected": integerSchema(),
			"ratings":   integerSchema(),
			"comments":  integerSchema(),
		},
	}
}

// ratingDistributionSchema is the ten-bucket 1..10 distribution: an object whose
// keys are the stringified ratings, each a float percentage.
func ratingDistributionSchema() map[string]interface{} {
	buckets := make(map[string]interface{}, 10)
	required := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		key := strconv.Itoa(i)
		buckets[key] = map[string]interface{}{"type": "number", "format": "float"}
		required = append(required, key)
	}
	return map[string]interface{}{
		"type":       "object",
		"required":   required,
		"properties": buckets,
	}
}

func integerSchema() map[string]interface{} {
	return map[string]interface{}{"type": "integer"}
}
