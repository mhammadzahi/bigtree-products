// Package catalog holds the in-memory product index behind the category pages:
// every visible product with its categories, collections and derived filter
// values (material and colour families, outdoor, flame retardant, ...).
// Counts, filter options and cards are all computed from it, never from live
// SQL queries. The rules are a port of the live site's "BigTree Category
// Pages" snippets (Data Layer v5, Tile Index v12, Leaf Grid v14, Fabrics v12,
// Add-on Categories) — see BigTree_Filters_Architecture_And_Snippets_*.md.
package catalog

import (
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	reAmp    = regexp.MustCompile(`\s*&\s*`)
	reOpen   = regexp.MustCompile(`\s*\(\s*`)
	reClose  = regexp.MustCompile(`\s*\)\s*`)
	reSpaces = regexp.MustCompile(`\s+`)
)

// Norm is bigtree_cat_norm: decode entities, lowercase, NBSP to space, space
// out "&" and parentheses, collapse whitespace, trim.
func Norm(s string) string {
	s = html.UnescapeString(s)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = reAmp.ReplaceAllString(s, " & ")
	s = reOpen.ReplaceAllString(s, " (")
	s = reClose.ReplaceAllString(s, ") ")
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// Decode is the display form of a WordPress name: entities decoded, trimmed.
func Decode(s string) string { return strings.TrimSpace(html.UnescapeString(s)) }

// kwScanner emulates bigtree_cat_kw_regex:
//
//	/(?<![\p{L}])(?:kw1|kw2|...)(?:e?s)?(?![\p{L}])/u   (keywords longest first)
//
// Go's RE2 has no look-around, so the scan is done by hand with the same
// semantics: leftmost match, longest keyword first, a "not a letter" boundary
// on both sides (digits and punctuation are boundaries, so "8.lin" matches
// "lin"), scanning on after each match like preg_match_all.
type kwScanner struct {
	kws    []string          // normalised, longest first
	group  map[string]string // keyword -> family key
	plural bool              // allow an optional "s" / "es" suffix
}

type kwHit struct {
	pos    int
	family string
}

func newScanner(plural bool, families ...kwFamily) *kwScanner {
	sc := &kwScanner{group: map[string]string{}, plural: plural}
	for _, f := range families {
		for _, k := range f.kws {
			n := Norm(k)
			if n == "" {
				continue
			}
			if _, dup := sc.group[n]; !dup {
				sc.kws = append(sc.kws, n)
			}
			sc.group[n] = f.key // later family wins, like the PHP flat map
		}
	}
	sort.SliceStable(sc.kws, func(i, j int) bool { return len(sc.kws[i]) > len(sc.kws[j]) })
	return sc
}

func isLetterBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsLetter(r)
}

func isLetterAt(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsLetter(r)
}

// scan returns every match (position + family) in s, which must be normalised.
func (sc *kwScanner) scan(s string) []kwHit {
	var hits []kwHit
	for i := 0; i < len(s); {
		matched := false
		if !isLetterBefore(s, i) {
			for _, kw := range sc.kws {
				if !strings.HasPrefix(s[i:], kw) {
					continue
				}
				end := i + len(kw)
				var ends []int
				if sc.plural { // (?:e?s)? is greedy: try "es", then "s", then nothing
					if strings.HasPrefix(s[end:], "es") {
						ends = append(ends, end+2)
					}
					if strings.HasPrefix(s[end:], "s") {
						ends = append(ends, end+1)
					}
				}
				ends = append(ends, end)
				for _, e := range ends {
					if !isLetterAt(s, e) {
						hits = append(hits, kwHit{pos: i, family: sc.group[kw]})
						i = e
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
		}
		if matched {
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return hits
}

func (sc *kwScanner) matches(s string) bool { return len(sc.scan(s)) > 0 }

type kwFamily struct {
	key string
	kws []string
}

// natLess is strnatcasecmp(a, b) < 0: case-insensitive, digit runs compared
// by numeric value.
func natLess(a, b string) bool { return natCmp(a, b) < 0 }

func natCmp(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		ca, cb := ra[i], rb[j]
		if unicode.IsDigit(ca) && unicode.IsDigit(cb) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				if len(na) < len(nb) {
					return -1
				}
				return 1
			}
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			continue
		}
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case len(ra)-i < len(rb)-j:
		return -1
	case len(ra)-i > len(rb)-j:
		return 1
	}
	return 0
}

// titleLabel is ucwords(str_replace(['-','_'], ' ', key)).
func titleLabel(key string) string {
	key = strings.NewReplacer("-", " ", "_", " ").Replace(key)
	words := strings.Fields(key)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
