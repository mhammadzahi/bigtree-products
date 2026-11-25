package catalog

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Facet keys (also the order of the filter bar after the two switches).
const (
	FOutdoor   = "o"
	FFlame     = "fr"
	FStyle     = "st"
	FType      = "y"
	FMaterial  = "m"
	FColor     = "c"
	FProject   = "p"
	FApp       = "ap"
	FSelection = "s"
	FMart      = "mt"
	FLight     = "lf"
	FFire      = "fs"
)

// URL parameter of each facet.
var facetParam = map[string]string{
	FOutdoor: "f_out", FFlame: "f_flame", FStyle: "f_style", FType: "f_type", FMaterial: "f_mat",
	FColor: "f_col", FProject: "f_proj", FApp: "f_app", FSelection: "f_sel", FMart: "f_mart",
	FLight: "f_light", FFire: "f_fire",
}

var listFacets = []string{FStyle, FType, FMaterial, FColor, FProject, FApp, FSelection, FFire}

// Filter is the active filter state, read from and written to the URL.
type Filter struct {
	Outdoor, Flame bool
	Lists          map[string][]string // list facets -> selected values
	Mt             int
	Lf             float64
}

// ParseFilter reads f_* parameters: comma lists, trimmed, de-duplicated.
func ParseFilter(q url.Values) Filter {
	f := Filter{Lists: map[string][]string{}}
	f.Outdoor = truthy(q.Get("f_out"))
	f.Flame = truthy(q.Get("f_flame"))
	for _, k := range listFacets {
		seen := map[string]bool{}
		for _, raw := range q[facetParam[k]] {
			for _, v := range strings.Split(raw, ",") {
				v = strings.TrimSpace(v)
				if v != "" && !seen[v] {
					seen[v] = true
					f.Lists[k] = append(f.Lists[k], v)
				}
			}
		}
	}
	f.Mt, _ = strconv.Atoi(q.Get("f_mart"))
	if f.Mt < 0 {
		f.Mt = 0
	}
	if lf, err := strconv.ParseFloat(q.Get("f_light"), 64); err == nil && lf > 0 && lf <= 8 {
		f.Lf = lf
	}
	return f
}

func truthy(s string) bool { return s != "" && s != "0" && s != "false" }

// Query encodes the filter in the fixed parameter order.
func (f Filter) Query() string {
	var parts []string
	if f.Outdoor {
		parts = append(parts, "f_out=1")
	}
	if f.Flame {
		parts = append(parts, "f_flame=1")
	}
	for _, k := range []string{FStyle, FType, FMaterial, FColor, FProject, FApp, FSelection} {
		if vs := f.Lists[k]; len(vs) > 0 {
			parts = append(parts, facetParam[k]+"="+joinEsc(vs))
		}
	}
	if f.Mt > 0 {
		parts = append(parts, "f_mart="+strconv.Itoa(f.Mt))
	}
	if f.Lf > 0 {
		parts = append(parts, "f_light="+strconv.FormatFloat(f.Lf, 'f', -1, 64))
	}
	if vs := f.Lists[FFire]; len(vs) > 0 {
		parts = append(parts, "f_fire="+joinEsc(vs))
	}
	return strings.Join(parts, "&")
}

func joinEsc(vs []string) string {
	esc := make([]string, len(vs))
	for i, v := range vs {
		esc[i] = url.QueryEscape(v)
	}
	return strings.Join(esc, ",")
}

// Active reports whether any filter is set.
func (f Filter) Active() bool { return f.Query() != "" }

// without returns a copy with one facet cleared.
func (f Filter) without(k string) Filter {
	g := Filter{Outdoor: f.Outdoor, Flame: f.Flame, Mt: f.Mt, Lf: f.Lf, Lists: map[string][]string{}}
	for kk, v := range f.Lists {
		if kk != k {
			g.Lists[kk] = v
		}
	}
	switch k {
	case FOutdoor:
		g.Outdoor = false
	case FFlame:
		g.Flame = false
	case FMart:
		g.Mt = 0
	case FLight:
		g.Lf = 0
	}
	return g
}

// values returns a record's values for a list facet.
func (r *Rec) values(k string) []string {
	p := r.P
	switch k {
	case FStyle:
		return p.Styles
	case FType:
		return p.Types
	case FMaterial:
		return r.Mat
	case FColor:
		return r.Colors
	case FProject:
		return p.Projects
	case FApp:
		return p.Apps
	case FSelection:
		if p.Selection == 0 {
			return nil
		}
		return []string{strconv.FormatInt(p.Selection, 10)}
	case FFire:
		return p.Fire
	}
	return nil
}

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// Match: AND across filters, OR within one filter.
func (r *Rec) Match(f Filter) bool {
	if f.Outdoor && !r.P.Outdoor {
		return false
	}
	if f.Flame && !r.P.Flame {
		return false
	}
	for k, sel := range f.Lists {
		if len(sel) > 0 && !intersects(sel, r.values(k)) {
			return false
		}
	}
	if f.Mt > 0 && r.P.Mt < f.Mt {
		return false
	}
	if f.Lf > 0 && r.P.Lf < f.Lf {
		return false
	}
	return true
}

// Card is one card in a grid: a product, or a colour version Of a lead.
type Card struct {
	Rec *Rec
	Of  int64
}

// Cards for a category under a filter. With a colour filter on, every
// matching colour version becomes its own card right after its lead (even
// when the lead itself does not match). No product appears twice.
func (top *Top) Cards(catID int64, f Filter) []Card {
	ids := top.InCat[catID]
	colorOn := len(f.Lists[FColor]) > 0
	seen := make(map[int64]bool, len(ids))
	out := make([]Card, 0, len(ids))
	for _, pid := range ids {
		r := top.Recs[pid]
		if !seen[pid] && r.Match(f) {
			seen[pid] = true
			out = append(out, Card{Rec: r, Of: pid})
		}
		if colorOn {
			for _, fid := range top.Versions[pid] {
				if seen[fid] {
					continue
				}
				if fr := top.Fin[fid]; fr != nil && fr.Match(f) {
					seen[fid] = true
					out = append(out, Card{Rec: fr, Of: pid})
				}
			}
		}
	}
	return out
}

// candidates returns the distinct records that would be cards for f in a
// category, ignoring whether the lead/version split dedupes them per value.
func (top *Top) candidates(catID int64, f Filter, withVersions bool) []*Rec {
	ids := top.InCat[catID]
	seen := make(map[int64]bool, len(ids))
	out := make([]*Rec, 0, len(ids))
	for _, pid := range ids {
		r := top.Recs[pid]
		if !seen[pid] && r.Match(f) {
			seen[pid] = true
			out = append(out, r)
		}
		if withVersions {
			for _, fid := range top.Versions[pid] {
				if fr := top.Fin[fid]; fr != nil && !seen[fid] && fr.Match(f) {
					seen[fid] = true
					out = append(out, fr)
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Filter bar
// ---------------------------------------------------------------------------

// Option is one choice in a dropdown.
type Option struct {
	Value    string
	Label    string
	Count    int
	Selected bool
	Swatch   string // colour dot (Color only)
}

// Facet is one control in the filter bar.
type Facet struct {
	Key      string
	Param    string
	Label    string
	Switch   bool
	Single   bool // Martindale / lightfastness: one minimum
	On       bool // switch state
	Count    int  // switch: cards with the flag
	Options  []Option
	Selected int // how many options are selected
}

// Chip is an active filter shown as a removable chip.
type Chip struct {
	Label string
	Query string // the filter query without this value
}

// Bar is the whole filter bar for a leaf page.
type Bar struct {
	Facets []Facet
	Chips  []Chip
}

var facetLabels = map[string]string{
	FOutdoor: "Outdoor", FFlame: "Flame Retardant", FStyle: "Style", FType: "Type", FMaterial: "Material",
	FColor: "Color", FProject: "Project", FApp: "Application", FSelection: "Selection",
	FMart: "Martindale (rubs)", FLight: "Lightfastness", FFire: "Fire Standard",
}

var martBuckets = []struct {
	v     int
	label string
}{{20000, "20,000+"}, {30000, "30,000+"}, {50000, "50,000+"}, {100000, "100,000+"}}

var lightBuckets = []struct {
	v     float64
	label string
}{{4, "4+"}, {5, "5+"}, {6, "6+"}}

func (ix *Index) facetShown(top *Top, k string) bool {
	if hiddenFacets[top.Slug][k] {
		return false
	}
	switch k {
	case FProject:
		return projectTops[top.Slug]
	case FMart, FLight, FFire:
		return perfTops[top.Slug]
	}
	return true
}

// Bar builds the filter bar: option counts equal the cards the visitor would
// see after picking that option; zero options and single-option facets hide.
func (ix *Index) Bar(top *Top, cat *Term, f Filter) Bar {
	var bar Bar
	colorOn := len(f.Lists[FColor]) > 0

	// switches
	for _, k := range []string{FOutdoor, FFlame} {
		if !ix.facetShown(top, k) {
			continue
		}
		on := (k == FOutdoor && f.Outdoor) || (k == FFlame && f.Flame)
		base := top.candidates(cat.ID, f.without(k), colorOn)
		nOn := 0
		for _, r := range base {
			if (k == FOutdoor && r.P.Outdoor) || (k == FFlame && r.P.Flame) {
				nOn++
			}
		}
		if on || (nOn > 0 && nOn < len(base)) {
			bar.Facets = append(bar.Facets, Facet{Key: k, Param: facetParam[k], Label: facetLabels[k],
				Switch: true, On: on, Count: nOn})
		}
	}

	// list facets
	for _, k := range []string{FStyle, FType, FMaterial, FColor, FProject, FApp, FSelection} {
		if !ix.facetShown(top, k) {
			continue
		}
		withVersions := colorOn || k == FColor
		base := top.candidates(cat.ID, f.without(k), withVersions)
		counts := map[string]int{}
		for _, r := range base {
			for _, v := range r.values(k) {
				if k == FType && typeIgnore[top.Slug][v] {
					continue
				}
				counts[v]++
			}
		}
		fc := ix.listFacet(top, k, counts, f.Lists[k])
		if fc != nil {
			bar.Facets = append(bar.Facets, *fc)
		}
	}

	// Fabrics performance
	if ix.facetShown(top, FMart) {
		base := top.candidates(cat.ID, f.without(FMart), colorOn)
		fc := Facet{Key: FMart, Param: facetParam[FMart], Label: facetLabels[FMart], Single: true}
		for _, b := range martBuckets {
			n := 0
			for _, r := range base {
				if r.P.Mt >= b.v {
					n++
				}
			}
			sel := f.Mt == b.v
			if n > 0 || sel {
				fc.Options = append(fc.Options, Option{Value: strconv.Itoa(b.v), Label: b.label, Count: n, Selected: sel})
			}
			if sel {
				fc.Selected = 1
			}
		}
		if len(fc.Options) >= 2 || fc.Selected > 0 {
			bar.Facets = append(bar.Facets, fc)
		}
	}
	if ix.facetShown(top, FLight) {
		base := top.candidates(cat.ID, f.without(FLight), colorOn)
		fc := Facet{Key: FLight, Param: facetParam[FLight], Label: facetLabels[FLight], Single: true}
		for _, b := range lightBuckets {
			n := 0
			for _, r := range base {
				if r.P.Lf >= b.v {
					n++
				}
			}
			sel := f.Lf == b.v
			if n > 0 || sel {
				fc.Options = append(fc.Options, Option{Value: strconv.FormatFloat(b.v, 'f', -1, 64), Label: b.label, Count: n, Selected: sel})
			}
			if sel {
				fc.Selected = 1
			}
		}
		if len(fc.Options) >= 2 || fc.Selected > 0 {
			bar.Facets = append(bar.Facets, fc)
		}
	}
	if ix.facetShown(top, FFire) {
		base := top.candidates(cat.ID, f.without(FFire), colorOn)
		counts := map[string]int{}
		for _, r := range base {
			for _, v := range r.P.Fire {
				counts[v]++
			}
		}
		if fc := ix.listFacet(top, FFire, counts, f.Lists[FFire]); fc != nil {
			bar.Facets = append(bar.Facets, *fc)
		}
	}

	bar.Chips = ix.chips(top, f)
	return bar
}

func (ix *Index) listFacet(top *Top, k string, counts map[string]int, selected []string) *Facet {
	sel := set(selected...)
	fc := &Facet{Key: k, Param: facetParam[k], Label: facetLabels[k]}
	if k == FApp && top.Slug == "fabrics" {
		fc.Label = "Use"
	}
	values := map[string]bool{}
	for v, n := range counts {
		if n > 0 {
			values[v] = true
		}
	}
	for v := range sel {
		values[v] = true
	}
	for v := range values {
		o := Option{Value: v, Label: ix.optionLabel(k, v), Count: counts[v], Selected: sel[v]}
		if k == FColor {
			o.Swatch = ColorSwatch[v]
		}
		fc.Options = append(fc.Options, o)
		if o.Selected {
			fc.Selected++
		}
	}
	usable := 0
	for _, o := range fc.Options {
		if o.Count > 0 {
			usable++
		}
	}
	if usable < 2 && fc.Selected == 0 {
		return nil
	}
	sortOptions(k, fc.Options)
	return fc
}

func (ix *Index) optionLabel(k, v string) string {
	switch k {
	case FStyle:
		return nameOr(ix.TermName["pa_style"][v], v)
	case FType:
		return nameOr(ix.TermName["pa_types"][v], v)
	case FApp:
		return nameOr(ix.TermName["pa_application"][v], v)
	case FMaterial:
		return MaterialLabel(v)
	case FColor:
		return titleLabel(v)
	case FProject:
		return nameOr(ix.Projects[v], v)
	case FSelection:
		id, _ := strconv.ParseInt(v, 10, 64)
		if t := ix.Terms[id]; t != nil {
			return t.Name
		}
	case FFire:
		for _, s := range FireStandards {
			if s.Key == v {
				return s.Label
			}
		}
	case FMart:
		for _, b := range martBuckets {
			if strconv.Itoa(b.v) == v {
				return b.label
			}
		}
	case FLight:
		for _, b := range lightBuckets {
			if strconv.FormatFloat(b.v, 'f', -1, 64) == v {
				return b.label
			}
		}
	}
	return v
}

func nameOr(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

func sortOptions(k string, opts []Option) {
	switch k {
	case FColor:
		rank := map[string]int{}
		for i, c := range ColorOrder {
			rank[c] = i
		}
		sort.SliceStable(opts, func(i, j int) bool {
			ri, oki := rank[opts[i].Value]
			rj, okj := rank[opts[j].Value]
			if !oki {
				ri = len(ColorOrder)
			}
			if !okj {
				rj = len(ColorOrder)
			}
			if ri != rj {
				return ri < rj
			}
			return natLess(opts[i].Label, opts[j].Label)
		})
	case FFire:
		rank := map[string]int{}
		for i, s := range FireStandards {
			rank[s.Key] = i
		}
		sort.SliceStable(opts, func(i, j int) bool { return rank[opts[i].Value] < rank[opts[j].Value] })
	default:
		sort.SliceStable(opts, func(i, j int) bool {
			oi, oj := opts[i].Value == "other", opts[j].Value == "other"
			if oi != oj {
				return oj // "Other" last
			}
			return natLess(opts[i].Label, opts[j].Label)
		})
	}
}

// chips lists the active filters, each with the query that removes it.
func (ix *Index) chips(top *Top, f Filter) []Chip {
	var out []Chip
	add := func(label string, g Filter) { out = append(out, Chip{Label: label, Query: g.Query()}) }
	if f.Outdoor {
		add("Outdoor", f.without(FOutdoor))
	}
	if f.Flame {
		add("Flame Retardant", f.without(FFlame))
	}
	for _, k := range []string{FStyle, FType, FMaterial, FColor, FProject, FApp, FSelection, FFire} {
		label := facetLabels[k]
		if k == FApp && top.Slug == "fabrics" {
			label = "Use"
		}
		for _, v := range f.Lists[k] {
			g := f.without(k)
			for _, other := range f.Lists[k] {
				if other != v {
					g.Lists[k] = append(g.Lists[k], other)
				}
			}
			add(label+": "+ix.optionLabel(k, v), g)
		}
	}
	if f.Mt > 0 {
		add(facetLabels[FMart]+": "+ix.optionLabel(FMart, strconv.Itoa(f.Mt)), f.without(FMart))
	}
	if f.Lf > 0 {
		add(facetLabels[FLight]+": "+ix.optionLabel(FLight, strconv.FormatFloat(f.Lf, 'f', -1, 64)), f.without(FLight))
	}
	return out
}

// Toggle returns the query after toggling one value of a facet (used to
// build each option's link, so the bar works without JavaScript too).
func (f Filter) Toggle(k, v string) string {
	g := f.without(k)
	switch k {
	case FOutdoor:
		g.Outdoor = !f.Outdoor
	case FFlame:
		g.Flame = !f.Flame
	case FMart:
		if n, _ := strconv.Atoi(v); n != f.Mt {
			g.Mt = n
		}
	case FLight:
		if n, _ := strconv.ParseFloat(v, 64); n != f.Lf {
			g.Lf = n
		}
	default:
		found := false
		for _, x := range f.Lists[k] {
			if x == v {
				found = true
				continue
			}
			g.Lists[k] = append(g.Lists[k], x)
		}
		if !found {
			g.Lists[k] = append(g.Lists[k], v)
		}
	}
	return g.Query()
}
