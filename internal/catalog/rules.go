package catalog

import (
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Feature roles (#207 L66706-46, plus 'commercial' from #213)
// ---------------------------------------------------------------------------

var featureRole = func() map[string]string {
	roles := map[string][]string{
		"outdoor": {"outdoor"},
		"indoor":  {"indoor"},
		"project": {"contract", "hospitality", "residential", "healthcare", "workplace", "workspace", "aviation",
			"marine", "marine (interior)", "mobility", "crèche/fitness", "creche/fitness", "commercial"},
		"flame": {"flame retardant"},
		"performance": {"antibacterial", "color fastness", "color pilling", "color resistance", "durability",
			"pilling", "seam slippage", "shrinkage wet", "thermal resistance", "weather resistance"},
		"ignore": {"furniture", "upholstery", "n/a"},
	}
	m := map[string]string{}
	for role, names := range roles {
		for _, n := range names {
			m[Norm(n)] = role
		}
	}
	return m
}()

// FeatureRole maps a pa_features term name to outdoor | indoor | project |
// flame | performance | ignore, or "" when unmapped.
func FeatureRole(name string) string { return featureRole[Norm(name)] }

// ProjectLabel is the display label of a project feature ("Marine(interior)"
// and "Marine (interior)" merge).
func ProjectLabel(name string) string {
	if Norm(name) == "marine (interior)" {
		return "Marine (interior)"
	}
	return Decode(name)
}

// ---------------------------------------------------------------------------
// Material families (#207 L66784-66966)
// ---------------------------------------------------------------------------

var materialFamilies = []kwFamily{
	{"wood", []string{"wood", "oak", "ash", "beech", "birch", "pine", "acacia", "walnut", "teak", "mdf", "plywood", "veneer", "veneered", "timber", "mahogany", "maple", "elm", "whitewood", "hdf", "cherry", "cedar", "poplar", "eucalyptus", "paulownia", "sheesham", "rubberwood", "larch", "spruce", "fir", "wooden"}},
	{"metal", []string{"metal", "iron", "steel", "stainless", "aluminium", "aluminum", "alu", "brass", "bronze", "copper", "zinc", "chrome", "nickel", "titanium", "alloy", "powdercoated", "powder coated", "galvanized", "galvanised", "wrought", "gold", "silver", "metalic"}},
	{"stone", []string{"marble", "travertine", "sandstone", "granite", "stone", "terrazzo", "alabaster", "onyx", "quartz", "quartzite", "slate", "limestone", "basalt", "calacatta", "pietra", "marmo", "soapstone", "travertin"}},
	{"glass", []string{"glass", "crystal", "mirror", "mirrored"}},
	{"ceramic", []string{"ceramic", "porcelain", "stoneware", "terracotta", "earthenware", "clay", "bone china", "terracota", "brick", "tile"}},
	{"concrete", []string{"concrete", "cement"}},
	{"fabric", []string{"fabric", "cotton", "linen", "polyester", "viscose", "acrylic", "wool", "silk", "nylon", "polyamide", "rayon", "velvet", "chenille", "boucle", "bouclé", "jacquard", "canvas", "cloth", "textile", "cashmere", "mohair", "lyocell", "modal", "pes", "pan", "cationic", "microfiber", "microfibre", "tencel", "elastane", "spandex", "lycra", "faux fur", "fur", "pl", "pa", "wo", "wv", "co", "lin", "ny", "vi", "cot", "poly", "pac", "rco", "trevira", "olyna", "olefin", "polyolefin", "acetate", "lurex", "fibranne", "rope", "textilene", "felt", "yarn"}},
	{"leather", []string{"leather", "hide", "suede", "nubuck", "nappa", "aniline", "grain", "nabuk", "buffed", "embossed", "pigmented", "corrected", "cottonrrected", "linenghtly"}},
	{"plastic", []string{"plastic", "abs", "pvc", "pu", "polyurethane", "polypropylene", "pp", "polyethylene", "resin", "polyresin", "acetal", "rubber", "silicone", "epoxy", "fiberglass", "fibreglass", "polyvinyl", "polyvinylchloride", "polycarbonate", "pmma", "polystyrene", "pet", "rpet", "hpl", "melamine", "frp"}},
	{"natural", []string{"rattan", "wicker", "cane", "jute", "seagrass", "sea grass", "raffia", "sisal", "abaca", "hemp", "straw", "bamboo", "water hyacinth", "banana", "cork"}},
}

// Each family is tested on its own (a term can be several families).
var materialScanners = func() []*kwScanner {
	out := make([]*kwScanner, len(materialFamilies))
	for i, f := range materialFamilies {
		out[i] = newScanner(true, f)
	}
	return out
}()

var materialIgnore = map[string]bool{"n/a": true, "none": true, "0": true, "-": true}

var materialStrip = []string{"chrome tanned", "brass-free", "brass free"}

// Floor Coverings: exact name map, nothing else (#207 L66895-920).
var floorCoveringsMaterial = func() map[string]string {
	raw := map[string]string{
		"Wool": "wool", "Premium Wool": "wool", "Decolan": "decolan", "Premium Decolan": "decolan",
		"Polyester": "polyester", "PES": "polyester", "Premium PES": "polyester",
		"Polyester (rPET)": "recycled-polyester", "Olyna®": "olyna", "Viscose": "viscose", "Rayon": "viscose",
		"Bamboo Silk": "bamboo-silk", "Artificial Silk": "artificial-silk", "Jute": "jute",
		"Hemp Indo-Nepalese": "hemp", "Cotton Chenille": "cotton-chenille", "Recyclable Plastic": "recyclable-plastic",
	}
	m := map[string]string{}
	for k, v := range raw {
		m[Norm(k)] = v
	}
	return m
}()

// Fabrics: fibre tokens, a blend counts under every fibre (#211 L69459-505).
var fabricFibres = []struct {
	key   string
	words []string
}{
	{"polyester", []string{"polyester", "pes", "pl", "trevira", "rpet"}},
	{"cotton", []string{"cotton", "co", "cot"}},
	{"viscose", []string{"viscose", "cv", "rayon", "fibranne"}},
	{"acrylic", []string{"acrylic", "pac"}},
	{"polyamide", []string{"polyamide", "pa", "nylon"}},
	{"linen", []string{"linen", "li", "lin", "flax"}},
	{"polyvinyl-chloride", []string{"pvc", "polyvinyl"}},
	{"polypropylene", []string{"polypropylene", "pp", "olefin", "polyolefin"}},
	{"polyurethane", []string{"polyurethane", "pu"}},
	{"wool", []string{"wool", "wo"}},
	{"silk", []string{"silk"}},
}

var reNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

var materialLabels = map[string]string{
	"wood": "Wood", "metal": "Metal", "stone": "Stone", "glass": "Glass", "ceramic": "Ceramic",
	"concrete": "Concrete", "fabric": "Fabric", "leather": "Leather", "plastic": "Plastic & Resin",
	"natural": "Natural Fibers", "other": "Other",
	"cotton": "Cotton", "linen": "Linen", "acrylic": "Acrylic", "polyamide": "Polyamide", "silk": "Silk",
	"polyvinyl-chloride": "Polyvinyl Chloride", "polypropylene": "Polypropylene", "polyurethane": "Polyurethane",
}

// MaterialLabel is the display label of a material family key.
func MaterialLabel(key string) string {
	if l, ok := materialLabels[key]; ok {
		return l
	}
	return titleLabel(key)
}

// MaterialFamilies maps one pa_composition term to its families, using the
// rules of the product's top-level category: Fabrics fibres, the Floor
// Coverings exact map, or the keyword rules. nil = no family.
func MaterialFamilies(top, term string) []string {
	switch top {
	case "fabrics":
		return fabricFamilies(term)
	case "floor-coverings":
		n := Norm(term)
		if n == "" || materialIgnore[n] {
			return nil
		}
		if k, ok := floorCoveringsMaterial[n]; ok {
			return []string{k}
		}
		return []string{"other"}
	}
	n := Norm(term)
	if n == "" || materialIgnore[n] {
		return nil
	}
	for _, p := range materialStrip {
		n = strings.ReplaceAll(n, p, " ")
	}
	var out []string
	for i, sc := range materialScanners {
		if sc.matches(n) {
			out = append(out, materialFamilies[i].key)
		}
	}
	if len(out) == 0 {
		return []string{"other"}
	}
	return out
}

func fabricFamilies(term string) []string {
	name := strings.ToLower(strings.TrimSpace(html.UnescapeString(term)))
	if materialIgnore[name] {
		return nil
	}
	var tokens = map[string]bool{}
	for _, t := range reNonAlnum.Split(name, -1) {
		if t != "" {
			tokens[t] = true
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	var out []string
	for _, f := range fabricFibres {
		if f.key == "silk" && (tokens["artificial"] || tokens["faux"] || tokens["bamboo"]) {
			continue
		}
		for _, w := range f.words {
			if tokens[w] {
				out = append(out, f.key)
				break
			}
		}
	}
	if len(out) == 0 {
		return []string{"other"}
	}
	return out
}

// ---------------------------------------------------------------------------
// Colour families (#207 L66977-67130)
// ---------------------------------------------------------------------------

// ColorOrder is the display order of colour options (#206 L65657).
var ColorOrder = []string{"black", "white", "grey", "gray", "red", "yellow", "blue", "green", "brown",
	"pink", "orange", "beige", "purple", "metallic", "mixed", "other"}

// ColorSwatch is the CSS background of each family's dot.
var ColorSwatch = map[string]string{
	"black": "#1c1c1c", "white": "#ffffff", "grey": "#9a9a9a", "red": "#c0392b", "yellow": "#f1c40f",
	"blue": "#2e6da4", "green": "#3c8d5a", "brown": "#7b5233", "pink": "#e8a0b4", "orange": "#e67e22",
	"beige": "#d9c7a3", "purple": "#7d4e9e",
	"metallic": "linear-gradient(135deg,#e9e9e9,#a9a9a9 50%,#f5f5f5)",
	"mixed":    "conic-gradient(#c0392b,#f1c40f,#3c8d5a,#2e6da4,#7d4e9e,#c0392b)",
	"other":    "transparent",
}

var colorFamilies = []kwFamily{
	{"black", []string{"black", "ebony", "noir", "nero", "jet", "coal", "obsidian", "ink", "raven", "onyx"}},
	{"white", []string{"white", "snow", "alabaster", "bianco", "blanc", "pearl", "chalk"}},
	{"grey", []string{"grey", "gray", "charcoal", "anthracite", "slate", "graphite", "smoke", "fog", "cement", "concrete", "grigio", "gris", "ash"}},
	{"red", []string{"red", "burgundy", "wine", "crimson", "scarlet", "cherry", "rosso", "rouge", "ruby", "maroon", "bordeaux", "carmine", "cardinal", "vino", "vinaccia"}},
	{"yellow", []string{"yellow", "lemon", "mustard", "ochre", "ocher", "saffron", "giallo", "jaune", "sunflower", "canary", "curry", "mostaza"}},
	{"blue", []string{"blue", "navy", "azure", "cobalt", "teal", "turquoise", "aqua", "denim", "indigo", "sapphire", "cyan", "sky", "ocean", "marine", "petrol", "abyss", "midnight", "blu", "bleu", "aegean", "lapis", "océan", "jeans", "aquamarine", "acquamarina", "celeste"}},
	{"green", []string{"green", "olive", "sage", "moss", "mint", "emerald", "forest", "lime", "jade", "verde", "vert", "pistachio", "fern", "avocado", "agave", "aloe", "alfalfa", "eucalyptus", "pine", "celadon", "céladon"}},
	{"brown", []string{"brown", "chocolate", "coffee", "walnut", "cocoa", "mocha", "chestnut", "espresso", "tobacco", "cognac", "caramel", "camel", "tan", "marrone", "marron", "umber", "sepia", "mahogany", "hazel", "cinnamon", "toffee", "truffle", "tabac", "sienna", "siena", "trufa"}},
	{"pink", []string{"pink", "rose", "blush", "fuchsia", "magenta", "salmon", "rosa"}},
	{"orange", []string{"orange", "rust", "terracotta", "apricot", "peach", "coral", "tangerine", "amber", "arancio", "pumpkin", "terracota"}},
	{"beige", []string{"beige", "cream", "ivory", "ecru", "sand", "almond", "taupe", "nude", "champagne", "oat", "oatmeal", "linen", "natural", "buff", "biscuit", "bone", "sabbia", "crema", "creme", "parchment", "wheat", "flax", "latte", "khaki", "off white", "off-white", "offwhite"}},
	{"purple", []string{"purple", "violet", "lilac", "lavender", "plum", "aubergine", "mauve", "amethyst", "grape", "orchid", "viola", "mulberry", "eggplant"}},
	{"metallic", []string{"metallic", "rose gold", "gold", "silver", "bronze", "copper", "brass", "chrome", "aluminium", "aluminum", "alluminium", "platinum", "pewter", "gunmetal", "nickel", "titanium", "steel", "argento", "sterling", "rame"}},
	{"mixed", []string{"mixed", "mix", "multi", "multicolor", "multicolour", "multicolored", "multicoloured", "assorted", "various", "two tone", "two-tone", "tricolor"}},
}

var colorScanner = newScanner(false, colorFamilies...)

var colorExact = func() map[string]string {
	raw := map[string][]string{
		"grey":     {"stone", "limestone", "cobblestone", "granite", "quartz", "pumice stone", "pebble", "mist", "misty", "haze", "dove", "shark", "shadow", "storm", "gull", "eléphant", "elephant", "mouse", "chinchilla", "gargoyle", "cinder", "asphalt", "lava", "fossil", "rainy day", "lunar rock", "mineral"},
		"white":    {"ice", "icicle", "frost", "cloud", "glacier", "gardenia", "antarctica", "feather", "moonbeam", "eggshell", "milk", "coconut milk", "marshmallow", "pelican"},
		"beige":    {"mushroom", "sesame", "greige", "putty", "dust", "vanilla", "marzipan", "nata", "calico", "stucco", "pergamena", "cashmere", "canvas", "dune", "sahara", "desert", "tofu", "coconut", "pudding", "naturel", "raffia", "jute", "seagrass", "bamboo", "straw", "rattan", "sponge", "string", "castor", "antique", "driftwood", "earth tone light"},
		"brown":    {"brownie", "cacao", "cappuccino", "moka", "nut", "nougat", "russet", "auburn", "redwood", "rosewood", "cuoio", "avana", "teak", "praline", "toast", "pinecone", "mud", "earth", "earth tone dark", "wood", "brandy", "tiramisu", "palissandro"},
		"red":      {"brick", "strawberry", "marsala", "sangria", "passion", "fire", "watermelon"},
		"blue":     {"cadet", "peacock", "lagoon", "hydro", "water", "safira", "malibu", "duckegg"},
		"green":    {"celery", "kiwi", "lima", "lichen", "linden", "oasis", "avocat", "basil", "grass", "leaf", "garden", "cactus", "palm", "cypress", "seafoam", "apple", "hunter", "iguana"},
		"yellow":   {"honey", "sunshine", "corn", "banana", "sun", "forsithia", "primrose", "butterscotch", "butternut"},
		"orange":   {"autumn", "spice", "ginger", "sunset", "harvest", "mango", "terra", "corten"},
		"pink":     {"rosewater", "peony", "blossom", "petal", "honeysuckle", "sorbet", "raspberry", "cameo", "dahlia"},
		"purple":   {"iris", "crocus", "wisteria", "heather", "hyacinth", "lavanda"},
		"metallic": {"iron", "alloy", "zinc"},
		"black":    {"pepper"},
		"other":    {"grey oak"},
	}
	m := map[string]string{}
	for fam, names := range raw {
		for _, n := range names {
			m[Norm(n)] = fam
		}
	}
	return m
}()

var colorIgnoreExact = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{"n/a", "0", "-", "none", "refer to images for color finishes", "varies on design",
		"massai", "newlin", "boheme", "mouchete", "tribal", "patio"} {
		m[Norm(n)] = true
	}
	return m
}()

var (
	colorIgnoreRe = []*regexp.Regexp{
		regexp.MustCompile(`^category \d`),
		regexp.MustCompile(`fabric collection \d`),
		regexp.MustCompile(`^refer to images`),
	}
	reCodeOnly  = regexp.MustCompile(`^[\d\s.\-/#]*$`)
	reColorJoin = regexp.MustCompile(`[/&+,|]| and `)
)

// ColorFamily maps one pa_color term to a single family key, or "" when the
// term is ignored (no family at all).
func ColorFamily(term string) string {
	n := Norm(term)
	if n == "" || colorIgnoreExact[n] {
		return ""
	}
	for _, re := range colorIgnoreRe {
		if re.MatchString(n) {
			return ""
		}
	}
	if f, ok := colorExact[n]; ok {
		return f
	}
	if reCodeOnly.MatchString(n) {
		return "other"
	}
	hits := colorScanner.scan(n)
	if len(hits) == 0 {
		return "other"
	}
	fams := map[string]bool{}
	for _, h := range hits {
		if h.family == "mixed" {
			return "mixed"
		}
		fams[h.family] = true
	}
	if len(fams) > 1 && reColorJoin.MatchString(n) {
		return "mixed"
	}
	return hits[len(hits)-1].family // the last colour word wins ("Almond Green" -> green)
}

// ---------------------------------------------------------------------------
// Fabrics performance values (#207 L67424-515)
// ---------------------------------------------------------------------------

var (
	reTags     = regexp.MustCompile(`<[^>]*>`)
	reMartRes  = regexp.MustCompile(`(?i)Result\s*:\s*(?:>=?|\x{2265})?\s*([0-9][0-9.,]*)`)
	reMartRubs = regexp.MustCompile(`(?i)([0-9][0-9.,]{3,})\s*(?:martindale|cycles|rubs)`)
	reThousand = regexp.MustCompile(`^\d{1,3}([.,]\d{3})+$`)
	reLightRes = regexp.MustCompile(`(?i)Result\s*:\s*(?:>=?|\x{2265})?\s*([1-8](?:[.,][05])?)`)
	reLightArt = regexp.MustCompile(`(?i)Artificial\s+Light\s*:?\s*(?:>=?|\x{2265})?\s*([1-8](?:[.,][05])?)`)
)

func perfClean(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
	if strings.EqualFold(s, "n/a") {
		return ""
	}
	return s
}

// Martindale parses the rub count from the `durability` meta, 0 when unknown.
func Martindale(meta string) int {
	s := perfClean(meta)
	if s == "" {
		return 0
	}
	m := reMartRes.FindStringSubmatch(s)
	if m == nil {
		m = reMartRubs.FindStringSubmatch(s)
	}
	if m == nil {
		return 0
	}
	v := strings.TrimRight(m[1], ".,")
	if reThousand.MatchString(v) {
		v = strings.NewReplacer(".", "", ",", "").Replace(v)
	} else {
		v = strings.ReplaceAll(v, ",", ".")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 1000 || f > 5_000_000 {
		return 0
	}
	return int(math.Round(f))
}

// Lightfastness parses the blue-wool grade from the `color_resistance` meta.
func Lightfastness(meta string) float64 {
	s := perfClean(meta)
	if s == "" {
		return 0
	}
	m := reLightRes.FindStringSubmatch(s)
	if m == nil {
		m = reLightArt.FindStringSubmatch(s)
	}
	if m == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
	return f
}

// FireStandards in display order.
var FireStandards = []struct{ Key, Label string }{
	{"en-1021", "EN 1021"}, {"bs-5852", "BS 5852"}, {"cal-117", "Cal Bull 117"}, {"nfpa-701", "NFPA 701"},
	{"imo", "IMO"}, {"din-4102", "DIN 4102"}, {"en-13773", "EN 13773 / UNI 9176"}, {"other", "Other flame retardant"},
}

var fireRes = []struct {
	key string
	re  *regexp.Regexp
}{
	{"en-1021", regexp.MustCompile(`(?i)EN\s*1021`)},
	{"bs-5852", regexp.MustCompile(`(?i)BS\s*5852`)},
	{"cal-117", regexp.MustCompile(`(?i)Cal(?:ifornia)?\.?\s*(?:Tech\.?\s*)?Bull(?:etin|\.)?\s*(?:No\.?\s*)?117|TB[\s-]?117`)},
	{"nfpa-701", regexp.MustCompile(`(?i)NFPA\s*701`)},
	{"imo", regexp.MustCompile(`(?i)\bIMO\b|FTP\s*Code`)},
	{"din-4102", regexp.MustCompile(`(?i)DIN\s*4102`)},
	{"en-13773", regexp.MustCompile(`(?i)EN\s*13773|UNI\s*9176`)},
}

var (
	reFireOther1 = regexp.MustCompile(`(?i)flame|fire|\bFR\b|retardant`)
	reFireOther2 = regexp.MustCompile(`(?i)BS\s*5867|NF\s*P\s*92|California\s+Test`)
)

// FireStandardKeys parses the `flame_retardant` meta into standard keys.
func FireStandardKeys(meta string) []string {
	s := perfClean(meta)
	if s == "" {
		return nil
	}
	var out []string
	for _, f := range fireRes {
		if f.re.MatchString(s) {
			out = append(out, f.key)
		}
	}
	if len(out) == 0 && (reFireOther1.MatchString(s) || reFireOther2.MatchString(s)) {
		out = append(out, "other")
	}
	return out
}
