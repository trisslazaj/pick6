package fpl

import "strings"

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func contains(hay, needle string) bool {
	return needle != "" && strings.Contains(hay, needle)
}

func joinComma(ss []string) string { return strings.Join(ss, ", ") }
