package catalog

import (
	"sort"
	"strings"
	"unicode"
)

// Path A of the live site: Selection > Collection pages, a port of
// "Shortcode for Collection Query - 14" (#181) and "Collection Helpers" (#212).
// The staff app is the admin view: published and private products count.
// Products hidden from the catalog (colour versions, swatches) never do.

// buildCollections indexes the collection tree and each term's products.
func (ix *Index) buildCollections() {
	ix.CollBySlug = map[string]*Term{}
	ix.CollChildren = map[int64][]int64{}
	ix.CollDirect = map[int64][]int64{}
	ix.collTree = map[int64][]int64{}
	for _, t := range ix.Terms {
		if t.Type != "collection" {
			continue
		}
		ix.CollBySlug[t.Slug] = t
		if t.Parent != 0 {
			ix.CollChildren[t.Parent] = append(ix.CollChildren[t.Parent], t.ID)
		}
	}
	for pid, kids := range ix.CollChildren {
		ix.sortTermsByName(kids)
		ix.CollChildren[pid] = kids
	}
	for _, p := range ix.Products {
		if p.Hidden() {
			continue
		}
		for _, c := range p.Colls {
			ix.CollDirect[c] = append(ix.CollDirect[c], p.ID)
		}
	}
	for c, ids := range ix.CollDirect {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		ix.CollDirect[c] = ids
	}
}

func (ix *Index) sortTermsByName(ids []int64) {
	sort.SliceStable(ids, func(i, j int) bool { return natLess(ix.Terms[ids[i]].Name, ix.Terms[ids[j]].Name) })
}

// CollProducts returns the visible products on a collection term and every
// term below it, by id.
func (ix *Index) CollProducts(id int64) []int64 {
	if v, ok := ix.collTree[id]; ok {
		return v
	}
	seen := map[int64]bool{}
	var out []int64
	var walk func(int64, int)
	walk = func(t int64, depth int) {
		if depth > 20 {
			return
		}
		for _, p := range ix.CollDirect[t] {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		for _, k := range ix.CollChildren[t] {
			walk(k, depth+1)
		}
	}
	walk(id, 0)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	ix.collMu.Lock()
	ix.collTree[id] = out
	ix.collMu.Unlock()
	return out
}

// HasChildren reports a Selection (or folder) rather than a leaf collection.
func (ix *Index) HasChildren(id int64) bool { return len(ix.CollChildren[id]) > 0 }

// ---------------------------------------------------------------------------
// Pills
// ---------------------------------------------------------------------------

// Pill is a filter button: one label, possibly several category slugs.
type Pill struct {
	Label string
	Slugs []string
}

// SanitizeTitle mimics WordPress sanitize_title for pill slugs.
func SanitizeTitle(s string) string { return slugifyRunes(Decode(s)) }

func slugifyRunes(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func isOutdoorCat(t *Term) bool {
	return Norm(t.Name) == "outdoor" || t.Slug == "outdoor" || t.Slug == "outdoor-2"
}

// pillsFrom groups slug -> label into one pill per uppercase label, sorted.
func pillsFrom(labels map[string]string) []Pill {
	byLabel := map[string][]string{}
	for slug, label := range labels {
		l := strings.ToUpper(label)
		byLabel[l] = append(byLabel[l], slug)
	}
	out := make([]Pill, 0, len(byLabel))
	for l, slugs := range byLabel {
		sort.Strings(slugs)
		out = append(out, Pill{Label: l, Slugs: slugs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// ---------------------------------------------------------------------------
// All Selections
// ---------------------------------------------------------------------------

// SelectionCard is one card on the All Selections page or a Selection grid.
type SelectionCard struct {
	Term     *Term
	Image    string
	Labels   []string // categories shown under the name
	Slugs    []string // pill slugs this card carries
	Outdoor  bool
	Products int
}

// AllSelections: every root collection with at least one product, by name.
// Pills are the level-1 departments (Outdoor dropped: it is a switch).
func (ix *Index) AllSelections() ([]SelectionCard, []Pill) {
	var roots []int64
	for _, t := range ix.Terms {
		if t.Type == "collection" && t.Parent == 0 {
			roots = append(roots, t.ID)
		}
	}
	ix.sortTermsByName(roots)

	pillLabels := map[string]string{}
	var cards []SelectionCard
	for _, rid := range roots {
		ids := ix.CollProducts(rid)
		if len(ids) == 0 {
			continue
		}
		card := SelectionCard{Term: ix.Terms[rid], Products: len(ids), Image: ix.firstImage(ids)}
		deps := map[int64]*Term{}
		for _, pid := range ids {
			p := ix.Products[pid]
			if p.Outdoor {
				card.Outdoor = true
			}
			for _, c := range p.Cats {
				if r := ix.root(c); r != nil {
					deps[r.ID] = r
				}
			}
		}
		depList := make([]*Term, 0, len(deps))
		for _, d := range deps {
			depList = append(depList, d)
		}
		sort.Slice(depList, func(i, j int) bool { return depList[i].Name < depList[j].Name })
		for _, d := range depList {
			card.Labels = append(card.Labels, d.Name)
			slug := SanitizeTitle(d.Name)
			if slug == "outdoor" {
				continue
			}
			card.Slugs = appendUniq(card.Slugs, slug)
			pillLabels[slug] = d.Name
		}
		cards = append(cards, card)
	}
	return cards, pillsFrom(pillLabels)
}

func (ix *Index) firstImage(ids []int64) string {
	for _, id := range ids {
		if img := ix.Products[id].Image; img != "" {
			return img
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Selection grid (a term with children)
// ---------------------------------------------------------------------------

// gridCardTerms returns the cards of a Selection: its direct children, but
// when it has <= 3 children and one of them has its own children, each such
// child is replaced by its direct children (the intermediate-folder unpack).
func (ix *Index) gridCardTerms(id int64) []int64 {
	kids := ix.CollChildren[id]
	unpack := false
	if len(kids) <= 3 {
		for _, k := range kids {
			if ix.HasChildren(k) {
				unpack = true
				break
			}
		}
	}
	if !unpack {
		return kids
	}
	var out []int64
	for _, k := range kids {
		if ix.HasChildren(k) {
			out = append(out, ix.CollChildren[k]...)
		} else {
			out = append(out, k)
		}
	}
	return out
}

// isFinish: the product sits in a category named "Finishes" (or below one).
func (ix *Index) isFinish(p *Product) bool {
	for _, c := range p.Cats {
		if ix.chainHasName(c, "finishes") {
			return true
		}
	}
	return false
}

// chain is a category's ancestors from the root (level 1) down to itself.
func (ix *Index) chain(catID int64) []*Term {
	ids := ix.ancestors(catID)
	out := make([]*Term, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		out = append(out, ix.Terms[ids[i]])
	}
	return out
}

// SelectionGrid builds the cards of a Selection page and its pills, picking
// the category level that best splits the cards (adaptive depth):
// >= 3 departments -> level 1; else > 1 level-2 -> 2; else > 1 level-3 -> 3.
func (ix *Index) SelectionGrid(id int64) ([]SelectionCard, []Pill) {
	type pending struct {
		card   SelectionCard
		chains [][]*Term
	}
	var all []pending
	levels := [3]map[int64]bool{{}, {}, {}}

	for _, tid := range ix.gridCardTerms(id) {
		ids := ix.CollProducts(tid)
		if len(ids) == 0 {
			continue
		}
		// Hide collections made only of finishes.
		onlyFinishes := true
		for _, pid := range ids {
			if !ix.isFinish(ix.Products[pid]) {
				onlyFinishes = false
				break
			}
		}
		if onlyFinishes {
			continue
		}
		pd := pending{card: SelectionCard{Term: ix.Terms[tid], Products: len(ids), Image: ix.firstImage(ids)}}
		seenCat := map[int64]bool{}
		for _, pid := range ids {
			p := ix.Products[pid]
			if p.Outdoor {
				pd.card.Outdoor = true
			}
			for _, c := range p.Cats {
				if seenCat[c] {
					continue
				}
				seenCat[c] = true
				ch := ix.chain(c)
				pd.chains = append(pd.chains, ch)
				for lvl := 0; lvl < 3 && lvl < len(ch); lvl++ {
					levels[lvl][ch[lvl].ID] = true
				}
			}
		}
		all = append(all, pd)
	}

	level := 1
	switch {
	case len(levels[0]) >= 3:
		level = 1
	case len(levels[1]) > 1:
		level = 2
	case len(levels[2]) > 1:
		level = 3
	case len(levels[1]) > 0:
		level = 2
	}

	pillLabels := map[string]string{}
	cards := make([]SelectionCard, 0, len(all))
	for _, pd := range all {
		card := pd.card
		labels := map[string]bool{}
		for _, ch := range pd.chains {
			t := ch[0]
			if len(ch) >= level {
				t = ch[level-1]
			}
			if isOutdoorCat(t) {
				continue
			}
			slug := SanitizeTitle(t.Slug)
			card.Slugs = appendUniq(card.Slugs, slug)
			pillLabels[slug] = t.Name
			if !labels[t.Name] {
				labels[t.Name] = true
				card.Labels = append(card.Labels, t.Name)
			}
		}
		sort.Strings(card.Labels)
		cards = append(cards, card)
	}
	return cards, pillsFrom(pillLabels)
}

// ---------------------------------------------------------------------------
// Collection page (a leaf collection)
// ---------------------------------------------------------------------------

// CollectionProduct is a product on a leaf collection page with its pills.
type CollectionProduct struct {
	P     *Product
	Slugs []string
}

// CollectionProducts: the products directly on a leaf collection (natural
// title order, then id) and the child-category pills (shown when > 1).
func (ix *Index) CollectionProducts(id int64) ([]CollectionProduct, []Pill) {
	ids := append([]int64(nil), ix.CollDirect[id]...)
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := ix.Products[ids[i]], ix.Products[ids[j]]
		if c := natCmp(a.Title, b.Title); c != 0 {
			return c < 0
		}
		return a.ID < b.ID
	})
	pillLabels := map[string]string{}
	out := make([]CollectionProduct, 0, len(ids))
	for _, pid := range ids {
		p := ix.Products[pid]
		cp := CollectionProduct{P: p}
		for _, c := range p.Cats {
			t := ix.Terms[c]
			if t.Parent == 0 {
				continue
			}
			slug := SanitizeTitle(t.Slug)
			cp.Slugs = appendUniq(cp.Slugs, slug)
			pillLabels[slug] = t.Name
		}
		out = append(out, cp)
	}
	pills := pillsFrom(pillLabels)
	if len(pills) < 2 {
		pills = nil
	}
	return out, pills
}

// SelectionOf returns the root (Selection) of a collection term.
func (ix *Index) SelectionOf(id int64) *Term { return ix.root(id) }

// CollCrumbs from the Selection down to the term.
func (ix *Index) CollCrumbs(id int64) []*Term {
	var out []*Term
	for t, i := ix.Terms[id], 0; t != nil && i < 20; i++ {
		out = append([]*Term{t}, out...)
		if t.Parent == 0 {
			break
		}
		t = ix.Terms[t.Parent]
	}
	return out
}
