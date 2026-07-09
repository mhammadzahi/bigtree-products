package models

import "strings"

// isNA reports whether a term is an "N/A" placeholder that shouldn't be a filter.
func isNA(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "n/a", "na", "n-a", "n\\a":
		return true
	}
	return false
}

// Taxonomy is one filterable term (a category, a B2B collection, a colour, ...).
type Taxonomy struct {
	ID    uint64 `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Type  string `json:"type"`
	Count uint   `json:"count"`
}

// FilterGroups is the sidebar payload: each facet's available options, in the
// display order used by the template/JS. Populated by LoadFacets (product.go)
// over the current filtered result set. Values are matched by Name.
type FilterGroups struct {
	Brands       []Taxonomy `json:"brands"`
	Collections  []Taxonomy `json:"collections"`  // leaf categories (series)
	Categories   []Taxonomy `json:"categories"`   // parent categories
	Applications []Taxonomy `json:"pa_application"`
	Colors       []Taxonomy `json:"pa_color"`
	Compositions []Taxonomy `json:"pa_composition"`
	Features     []Taxonomy `json:"pa_features"`
}
