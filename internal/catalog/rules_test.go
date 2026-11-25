package catalog

import (
	"strings"
	"testing"
)

// Fixtures are the live snippets' own self-tests (#207 L67711-20, L67727-35).

func TestMaterialFamilies(t *testing.T) {
	cases := map[string]string{
		"Aluminium and Teak":      "metal,wood",
		"Alabaster & Glass":       "stone,glass",
		"Chrome Tanned Raw Hides": "leather",
		"n/a":                     "",
		"Legs: Wooden":            "wood",
		"Metalic Mineral":         "metal",
		"Noce Travertin":          "stone",
		"8.Lin":                   "fabric",
		"Foam":                    "other",
		"Solid Oak Woods":         "wood",
	}
	for in, want := range cases {
		got := MaterialFamilies("furniture", in)
		if !sameSet(got, want) {
			t.Errorf("MaterialFamilies(%q) = %v, want %s", in, got, want)
		}
	}
}

func TestFloorCoveringsMaterial(t *testing.T) {
	cases := map[string]string{"Premium Wool": "wool", "Polyester (rPET)": "recycled-polyester", "Olyna®": "olyna", "Sisal": "other", "n/a": ""}
	for in, want := range cases {
		if got := MaterialFamilies("floor-coverings", in); !sameSet(got, want) {
			t.Errorf("floor %q = %v, want %s", in, got, want)
		}
	}
}

func TestFabricFamilies(t *testing.T) {
	cases := map[string]string{
		"70% PES 30% CO":  "polyester,cotton",
		"Bamboo Silk":     "other",
		"100% Silk":       "silk",
		"Polyvinyl (PVC)": "polyvinyl-chloride",
		"-":               "",
	}
	for in, want := range cases {
		if got := MaterialFamilies("fabrics", in); !sameSet(got, want) {
			t.Errorf("fabrics %q = %v, want %s", in, got, want)
		}
	}
}

func TestColorFamily(t *testing.T) {
	cases := map[string]string{
		"Black & White": "mixed",
		"Silver Grey":   "grey",
		"Rose Gold":     "metallic",
		"Off White":     "beige",
		"Almond Green":  "green",
		"White | Bronze Emboss | Champagne Emboss": "mixed",
		"Lavender Mist": "purple",
		"Mist":          "grey",
		"Category 3":    "",
		"01":            "other",
		"PHI":           "other",
		"05 Matt Black": "black",
		"48 Rich Gold":  "metallic",
	}
	for in, want := range cases {
		if got := ColorFamily(in); got != want {
			t.Errorf("ColorFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPerformance(t *testing.T) {
	if got := Martindale("Abrasion Resistance: EN ISO 12947-2 Result: > 100,000 cycles"); got != 100000 {
		t.Errorf("martindale = %d", got)
	}
	if got := Martindale("45.000 rubs"); got != 45000 {
		t.Errorf("martindale rubs = %d", got)
	}
	if got := Lightfastness("Light fastness ISO 105 B02 Result: 4-5"); got != 4 {
		t.Errorf("lightfastness = %v", got)
	}
	if got := strings.Join(FireStandardKeys("EN 1021-1 / 2, BS 5852 crib 5"), ","); got != "en-1021,bs-5852" {
		t.Errorf("fire = %s", got)
	}
	if got := strings.Join(FireStandardKeys("Inherently flame retardant"), ","); got != "other" {
		t.Errorf("fire other = %s", got)
	}
}

func TestNatCmp(t *testing.T) {
	if !natLess("item2", "item10") || natLess("B", "a") == natLess("a", "B") && natCmp("a", "A") != 0 {
		t.Error("natural compare")
	}
}

func sameSet(got []string, want string) bool {
	if want == "" {
		return len(got) == 0
	}
	w := strings.Split(want, ",")
	if len(w) != len(got) {
		return false
	}
	m := map[string]bool{}
	for _, g := range got {
		m[g] = true
	}
	for _, x := range w {
		if !m[x] {
			return false
		}
	}
	return true
}
