package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-text/typesetting/font"
)

// Any Google Fonts family can be used by name. Google's font list says which
// weights and axes a family has; the CSS API then serves a static TrueType
// instance at exactly the requested axis values: the weight (nearest the
// family offers) and, for families with optical sizes, the text's size, as
// browsers pick it. Each instance gets a family name of its own, so libass
// draws exactly the instance that was measured, even beside others of the
// same family.

// Google Fonts downloads can be turned off with YAML2VIDEO_OFFLINE=1.
var (
	errOffline     = fmt.Errorf("downloads are off")
	errNotOnGoogle = fmt.Errorf("not on Google Fonts")
	googleCSS      = "https://fonts.googleapis.com/css2"
	googleList     = "https://fonts.google.com/metadata/fonts"
	googleClient   = &http.Client{Timeout: 30 * time.Second}
	cssFontURL     = regexp.MustCompile(`url\((https?://[^)]+)\)`)
)

// googleListTTL is how long the cached family list is used before it is
// fetched again; a stale list is still used when fetching fails.
const googleListTTL = 7 * 24 * time.Hour

type googleAxis struct {
	Tag     string  `json:"tag"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"defaultValue"`
}

type googleFamily struct {
	Family string                     `json:"family"`
	Axes   []googleAxis               `json:"axes"`
	Fonts  map[string]json.RawMessage `json:"fonts"` // "400", "700i", ...
}

func (f googleFamily) axis(tag string) (googleAxis, bool) {
	for _, a := range f.Axes {
		if a.Tag == tag {
			return a, true
		}
	}
	return googleAxis{}, false
}

// weight is the weight the family offers closest to want: within its wght
// axis, or among its upright static weights.
func (f googleFamily) weight(want int) int {
	if a, ok := f.axis("wght"); ok {
		return int(math.Round(math.Max(a.Min, math.Min(a.Max, float64(want)))))
	}
	best := -1
	for style := range f.Fonts {
		w, err := strconv.Atoi(style) // italics ("400i") don't parse
		if err == nil && (best < 0 || abs(w-want) < abs(best-want) || abs(w-want) == abs(best-want) && w < best) {
			best = w
		}
	}
	if best < 0 {
		return 400
	}
	return best
}

func abs(n int) int { return max(n, -n) }

// googleListRetry is how long a failure to get the family list is reused
// before fetching again, so a missing font doesn't wait on the network for
// every lookup.
const googleListRetry = time.Minute

var (
	googleMu       sync.Mutex
	googleFamilies map[string]googleFamily // by normalized name
	googleListErr  error
	googleListAt   time.Time // when googleListErr happened
)

// googleFamilyNamed looks a family up in Google's font list.
func googleFamilyNamed(name string) (googleFamily, error) {
	googleMu.Lock()
	defer googleMu.Unlock()
	if googleFamilies == nil {
		if googleListErr != nil && time.Since(googleListAt) < googleListRetry {
			return googleFamily{}, googleListErr
		}
		list, err := loadGoogleList()
		if err != nil {
			googleListErr, googleListAt = err, time.Now()
			return googleFamily{}, err
		}
		googleListErr = nil
		googleFamilies = map[string]googleFamily{}
		for _, f := range list {
			googleFamilies[font.NormalizeFamily(f.Family)] = f
		}
	}
	f, ok := googleFamilies[font.NormalizeFamily(name)]
	if !ok {
		return googleFamily{}, errNotOnGoogle
	}
	return f, nil
}

// loadGoogleList reads the family list from the cache, fetching it when it is
// missing or old.
func loadGoogleList() ([]googleFamily, error) {
	path := filepath.Join(fontCacheDir("google"), "families.json")
	cached, err := os.ReadFile(path)
	info, statErr := os.Stat(path)
	if err == nil && statErr == nil && time.Since(info.ModTime()) < googleListTTL {
		if list, err := parseGoogleList(cached); err == nil {
			return list, nil
		}
	}
	data, status, fetchErr := googleGet(googleList)
	if fetchErr == nil && status != http.StatusOK {
		fetchErr = fmt.Errorf("HTTP %d", status)
	}
	if fetchErr == nil {
		list, err := parseGoogleList(data)
		if err == nil {
			_ = writeAtomic(path, data)
			return list, nil
		}
		fetchErr = err
	}
	if list, err := parseGoogleList(cached); err == nil {
		return list, nil // stale, but better than nothing
	}
	return nil, fmt.Errorf("Google Fonts family list: %w", fetchErr)
}

func parseGoogleList(data []byte) ([]googleFamily, error) {
	// The list starts with an anti-JSON-hijacking prefix.
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte(")]}'"))
	var doc struct {
		Families []googleFamily `json:"familyMetadataList"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Families) == 0 {
		return nil, fmt.Errorf("no families")
	}
	return doc.Families, nil
}

func googleGet(u string) ([]byte, int, error) {
	resp, err := googleClient.Get(u)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return data, resp.StatusCode, err
}

// googleFont returns a cached or freshly downloaded instance of family for
// weight and, where the family has optical sizes, for text of size pixels.
func googleFont(family string, weight int, size float64) (string, error) {
	if os.Getenv("YAML2VIDEO_OFFLINE") != "" {
		return "", errOffline
	}
	fam, err := googleFamilyNamed(family)
	if err != nil {
		return "", err
	}
	w := fam.weight(weight)
	tags, values := []string{"wght"}, []string{strconv.Itoa(w)}
	name := fmt.Sprintf("%s y2v %d", fam.Family, w)
	file := fmt.Sprintf("%s-%d", strings.ReplaceAll(fam.Family, " ", "-"), w)
	if a, ok := fam.axis("opsz"); ok && size > 0 {
		o := int(math.Round(math.Max(a.Min, math.Min(a.Max, size))))
		// Axes go in alphabetical order.
		tags, values = []string{"opsz", "wght"}, []string{strconv.Itoa(o), strconv.Itoa(w)}
		name += fmt.Sprintf(" o%d", o)
		file += fmt.Sprintf("-o%d", o)
	}
	path := filepath.Join(fontCacheDir("google"), file+".ttf")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	query := fam.Family + ":" + strings.Join(tags, ",") + "@" + strings.Join(values, ",")
	data, err := fetchGoogleFont(query)
	if err != nil {
		return "", err
	}
	if data, err = renameFont(data, name); err != nil {
		return "", fmt.Errorf("Google Fonts instance %s: %w", query, err)
	}
	if err := writeAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

func fetchGoogleFont(query string) ([]byte, error) {
	// Without a browser user agent the API serves whole TrueType files.
	css, status, err := googleGet(googleCSS + "?family=" + url.QueryEscape(query))
	if err != nil {
		return nil, fmt.Errorf("Google Fonts: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Google Fonts: HTTP %d for %s", status, query)
	}
	m := cssFontURL.FindSubmatch(css)
	if m == nil {
		return nil, fmt.Errorf("Google Fonts: no font file for %s", query)
	}
	data, status, err := googleGet(string(m[1]))
	if err != nil {
		return nil, fmt.Errorf("Google Fonts: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Google Fonts: HTTP %d for %s", status, m[1])
	}
	return data, nil
}
