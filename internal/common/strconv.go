package common

import "strconv"

// AtoiDefault converts the provided string to an integer falling back to the default when parsing fails.
func AtoiDefault(value string, def int) int {
	if value == "" {
		return def
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return def
	}
	return parsed
}
