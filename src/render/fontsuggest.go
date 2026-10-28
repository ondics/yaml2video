package render

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-text/typesetting/font"
)

// missingFontError uses only discovered families, not a hardcoded list of
// popular fonts. Google metadata may be unavailable; local choices still help.
func (b *fontBook) missingFontError(family string, cause error) error {
	candidates := map[string]string{}
	add := func(name, source string) {
		key := font.NormalizeFamily(name)
		if _, exists := candidates[key]; !exists {
			candidates[key] = fmt.Sprintf("%q (%s)", name, source)
		}
	}
	for _, fp := range b.project {
		add(fp.Family, "project")
	}
	for _, fp := range b.systemFonts() {
		add(fp.Family, "system")
	}
	add("Go", "bundled")
	add("Go Mono", "bundled")
	// googleFont has already attempted to load the catalog online. Reuse it
	// without retrying a failed network request. Offline, a cached catalog is OK.
	googleMu.Lock()
	for _, f := range googleFamilies {
		add(f.Family, "Google Fonts")
	}
	haveCatalog := googleFamilies != nil
	googleMu.Unlock()
	if !haveCatalog {
		if data, err := os.ReadFile(filepath.Join(fontCacheDir("google"), "families.json")); err == nil {
			if families, err := parseGoogleList(data); err == nil {
				for _, f := range families {
					add(f.Family, "Google Fonts; cached catalog")
				}
			}
		}
	}

	type match struct {
		name     string
		distance int
	}
	want := font.NormalizeFamily(family)
	matches := make([]match, 0, len(candidates))
	for name := range candidates {
		matches = append(matches, match{name, fontNameDistance(want, name)})
	}
	slices.SortFunc(matches, func(a, b match) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return strings.Compare(a.name, b.name)
	})
	choices := make([]string, 0, 8)
	for _, m := range matches[:min(8, len(matches))] {
		choices = append(choices, candidates[m.name])
	}
	return fmt.Errorf("font %q could not be resolved from project fonts, the system, or Google Fonts: %w; available fonts (closest names first): %s. Install the requested font, add its file to the project's fonts directory, or choose an available family", family, cause, strings.Join(choices, ", "))
}

// Levenshtein distance ranks misspellings deterministically, including Unicode.
func fontNameDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i, r := range x {
		prev := row[0]
		row[0] = i + 1
		for j, s := range y {
			old := row[j+1]
			cost := 0
			if r != s {
				cost = 1
			}
			row[j+1] = min(row[j]+1, old+1, prev+cost)
			prev = old
		}
	}
	return row[len(y)]
}
