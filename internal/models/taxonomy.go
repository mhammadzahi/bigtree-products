package models

import (
	"context"
	"database/sql"
	"strings"
)

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

// FilterGroups is the sidebar payload: every taxonomy type mapped to its terms,
// ordered so the template/JS can render accordion modules directly.
type FilterGroups struct {
	Categories   []Taxonomy `json:"categories"`
	Collections  []Taxonomy `json:"collections"`  // WooCommerce product tags
	Brands       []Taxonomy `json:"brands"`
	Colors       []Taxonomy `json:"pa_color"`
	Compositions []Taxonomy `json:"pa_composition"`
	Applications []Taxonomy `json:"pa_application"`
	Types        []Taxonomy `json:"pa_types"`
	Features     []Taxonomy `json:"pa_features"`
}

// LoadFilterGroups fetches every non-empty taxonomy term in one pass and buckets
// it by type for the sidebar.
func LoadFilterGroups(ctx context.Context, db *sql.DB) (*FilterGroups, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, slug, type, count
		FROM taxonomies
		ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fg := &FilterGroups{}
	for rows.Next() {
		var t Taxonomy
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Type, &t.Count); err != nil {
			return nil, err
		}
		if isNA(t.Name) || isNA(t.Slug) {
			continue // hide "N/A" placeholder terms from the filters
		}
		switch t.Type {
		case "category":
			fg.Categories = append(fg.Categories, t)
		case "collection":
			fg.Collections = append(fg.Collections, t)
		case "brand":
			fg.Brands = append(fg.Brands, t)
		case "pa_color":
			fg.Colors = append(fg.Colors, t)
		case "pa_composition":
			fg.Compositions = append(fg.Compositions, t)
		case "pa_application":
			fg.Applications = append(fg.Applications, t)
		case "pa_types":
			fg.Types = append(fg.Types, t)
		case "pa_features":
			fg.Features = append(fg.Features, t)
		}
	}
	return fg, rows.Err()
}
