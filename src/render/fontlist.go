package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/fontscan"
)

// FontCatalog is a best-effort list of font families. Discovery failures leave
// the other sources available and are reported in Warnings.
type FontCatalog struct {
	Fonts    []FontFamily `json:"fonts"`
	Warnings []string     `json:"warnings,omitempty"`
}

// FontFamily describes a family, not its individual weights or optical sizes.
// Sources contains stable identifiers: bundled, system, and google, in that order.
// Cached means at least one actual Google font file is cached, not just metadata.
// AvailableOffline means the family is bundled, installed, or has at least one
// cached Google instance; it does not promise every weight or size is cached.
type FontFamily struct {
	Family           string   `json:"family"`
	Sources          []string `json:"sources"`
	Cached           bool     `json:"cached"`
	AvailableOffline bool     `json:"available_offline"`
}

// ListFonts lists bundled, installed, Google metadata, and cached Google font
// families. It honors YAML2VIDEO_SYSTEM_FONTS=off and YAML2VIDEO_OFFLINE, and
// never downloads font files. Online, Google family metadata may be fetched.
func ListFonts() FontCatalog {
	return listFonts(installedFontList)
}

// Inject discovery per call so tests need neither host fonts nor global hooks.
func listFonts(system func() ([]string, error)) FontCatalog {
	catalog := FontCatalog{Fonts: []FontFamily{}}
	families := map[string]*FontFamily{}
	add := func(name, source string, cached, offline bool) {
		name = strings.TrimSpace(name)
		key := font.NormalizeFamily(name)
		if key == "" {
			return
		}
		f := families[key]
		if f == nil {
			f = &FontFamily{Family: name, Sources: []string{}}
			families[key] = f
		}
		if !slices.Contains(f.Sources, source) {
			f.Sources = append(f.Sources, source)
		}
		f.Cached = f.Cached || cached
		f.AvailableOffline = f.AvailableOffline || offline
	}
	add("Go", "bundled", false, true)
	add("Go Mono", "bundled", false, true)
	if os.Getenv("YAML2VIDEO_SYSTEM_FONTS") != "off" {
		names, err := system()
		for _, name := range names {
			add(name, "system", false, true)
		}
		if err != nil {
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("system fonts: %v", err))
		}
	}

	// Do not use googleFamilies: it may belong to a different cache directory
	// or have been populated before the caller switched to offline mode.
	metadata, err := loadGoogleList()
	if err != nil && !errors.Is(err, errOffline) {
		catalog.Warnings = append(catalog.Warnings, err.Error())
	}
	byFile := map[string]string{}
	byName := map[string]string{}
	for _, f := range metadata {
		add(f.Family, "google", false, false)
		byName[font.NormalizeFamily(f.Family)] = f.Family
		byFile[strings.ToLower(strings.ReplaceAll(f.Family, " ", "-"))] = f.Family
	}
	dir := fontCacheDir("google")
	entries, err := os.ReadDir(dir)
	if err != nil {
		catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("cached Google fonts: %v", err))
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".ttf") {
			continue
		}
		names, err := fontListFileFamilies(filepath.Join(dir, entry.Name()))
		if err != nil {
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("cached Google font %s: %v", entry.Name(), err))
			continue
		}
		fileMatch := cachedFontFile.FindStringSubmatch(entry.Name())
		for _, name := range names {
			if match := cachedFontFamily.FindStringSubmatch(name); match != nil {
				name = match[1] // retain spaces and real hyphens from the name table
			} else if fileMatch != nil && byFile[strings.ToLower(fileMatch[1])] != "" {
				name = byFile[strings.ToLower(fileMatch[1])]
			} else if name == "" && fileMatch != nil {
				// Older cache files without a usable name table are ambiguous:
				// filename hyphens may originally have been spaces.
				name = strings.ReplaceAll(fileMatch[1], "-", " ")
			}
			if canonical := byName[font.NormalizeFamily(name)]; canonical != "" {
				name = canonical
			}
			add(name, "google", true, true)
		}
	}
	for _, f := range families {
		catalog.Fonts = append(catalog.Fonts, *f)
	}
	slices.SortFunc(catalog.Fonts, func(a, b FontFamily) int {
		if order := strings.Compare(strings.ToLower(a.Family), strings.ToLower(b.Family)); order != 0 {
			return order
		}
		return strings.Compare(a.Family, b.Family)
	})
	return catalog
}

var (
	cachedFontFamily = regexp.MustCompile(`(?i)^(.+) y2v [0-9]+(?: o[0-9]+)?$`)
	cachedFontFile   = regexp.MustCompile(`(?i)^(.+)-[0-9]+(?:-o[0-9]+)?\.ttf$`)
)

// Read display names from the files: fontscan footprints normalize away spaces.
func installedFontList() ([]string, error) {
	prints, scanErr := fontscan.SystemFonts(log.New(io.Discard, "", 0), fontCacheDir("index"))
	return installedFontNames(prints, scanErr)
}

func installedFontNames(prints []fontscan.Footprint, scanErr error) ([]string, error) {
	var names []string
	errs := []error{scanErr}
	seen := map[string]bool{}
	for _, fp := range prints {
		path := fp.Location.File
		if seen[path] {
			continue
		}
		seen[path] = true
		families, err := fontListFileFamilies(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		names = append(names, families...)
	}
	return names, errors.Join(errs...)
}

func fontListFileFamilies(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	loaders, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ld := range loaders {
		if _, err := font.NewFont(ld); err != nil {
			return names, err
		}
		name := familyName(ld)
		if desc, err := font.Describe(ld, nil); err == nil && desc.Family != "" {
			name = desc.Family
		}
		names = append(names, name)
	}
	return names, nil
}
