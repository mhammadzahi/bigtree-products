package handlers

import (
	"html/template"
	"strings"
	"unicode"
)

// TemplateFuncs are the helpers available to every HTML template (pages and
// the catalog fragments rendered for the JSON API).
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// contains reports whether slug is in the selected slice (sidebar ticks).
		"contains": func(list []string, slug string) bool {
			for _, s := range list {
				if s == slug {
					return true
				}
			}
			return false
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		// safeHTML renders first-party WooCommerce rich text (product
		// descriptions) as HTML on the staff-only dashboard. Only ever feed it
		// content that originates from the store, never user input.
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		// safeCSS passes a colour-dot background from the fixed swatch table
		// (catalog.ColorSwatch), never user input.
		"safeCSS": func(s string) template.CSS { return template.CSS(s) },
		// label prettifies a taxonomy type or ACF key for display, e.g.
		// "pa_color" -> "Color", "minimum_order_quantity" -> "Minimum Order Quantity".
		// qhref turns a catalog filter query (built by catalog.Filter, every
		// value already URL-escaped) into a relative link "?f_...".
		"qhref":        func(q string) template.URL { return template.URL("?" + q) },
		"label":        prettyLabel,
		"downloadName": DownloadName,
		// lines splits a multi-line custom-field value into its non-blank
		// lines (editors often leave whitespace-only lines between items).
		"lines": func(s string) []string {
			var out []string
			for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					out = append(out, l)
				}
			}
			return out
		},
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},
	}
}

// prettyLabel turns a machine key into a human heading.
func prettyLabel(s string) string {
	s = strings.TrimPrefix(s, "pa_")
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	fields := strings.Fields(s)
	for i, w := range fields {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		fields[i] = string(r)
	}
	return strings.Join(fields, " ")
}
