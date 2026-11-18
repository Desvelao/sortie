// Package tags defines user tags and validates tag sets. No GUI or DB code.
package tags

import (
	"fmt"
	"image/color"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Tag is a user-defined category.
type Tag struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Shortcut    string `json:"shortcut"`    // single key: A-Z, 0-9, F1-F12; may be empty
	Destination string `json:"destination"` // relative directory; what {tag} expands to
	// DestinationTemplate, when set, replaces the global destination template
	// for this tag (see package pathtpl).
	DestinationTemplate string `json:"destination_template,omitempty"`
	Enabled             bool   `json:"enabled"`
	SortOrder           int    `json:"sort_order"`
	// Color is an optional "#RRGGBB" / "#RGB" display color; empty = automatic.
	Color string `json:"color,omitempty"`
}

// ParseColor parses "#RGB" or "#RRGGBB" (the leading # is optional).
func ParseColor(s string) (color.NRGBA, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return color.NRGBA{}, fmt.Errorf("color %q: expected #RRGGBB", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("color %q: expected #RRGGBB", s)
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

// Keys reserved for navigation/dialogs.
var reserved = map[string]bool{"LEFT": true, "RIGHT": true, "UP": true, "DOWN": true,
	"HOME": true, "END": true, "ESCAPE": true, "RETURN": true, "TAB": true, "SPACE": true}

// Defaults returns the starting tag set (shortcuts 1–9).
func Defaults() []Tag {
	names := []string{"Family", "Holidays", "Events", "Work", "Music", "Documents", "Personal", "Important", "Archive"}
	out := make([]Tag, len(names))
	for i, n := range names {
		out[i] = Tag{ID: i + 1, Name: n, Shortcut: fmt.Sprint(i + 1), Destination: n, Enabled: true, SortOrder: i}
	}
	return out
}

// NormalizeShortcut upper-cases and validates a shortcut ("" is allowed).
func NormalizeShortcut(s string) (string, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	if reserved[s] {
		return "", fmt.Errorf("shortcut %q is reserved for navigation", s)
	}
	if len(s) == 1 && (s[0] >= 'A' && s[0] <= 'Z' || s[0] >= '0' && s[0] <= '9') {
		return s, nil
	}
	if len(s) >= 2 && s[0] == 'F' {
		var n int
		if _, err := fmt.Sscanf(s[1:], "%d", &n); err == nil && n >= 1 && n <= 12 && fmt.Sprint(n) == s[1:] {
			return s, nil
		}
	}
	return "", fmt.Errorf("shortcut %q is not supported (use A–Z, 0–9 or F1–F12)", s)
}

// ValidateDestination requires a non-empty relative path without traversal.
func ValidateDestination(d string) error {
	d = strings.TrimSpace(d)
	if d == "" {
		return fmt.Errorf("destination is empty")
	}
	if strings.ContainsRune(d, 0) {
		return fmt.Errorf("destination contains an invalid character")
	}
	if strings.HasPrefix(d, "/") || strings.HasPrefix(d, `\`) || (len(d) > 1 && d[1] == ':') {
		return fmt.Errorf("destination %q must be relative", d)
	}
	for _, part := range strings.FieldsFunc(d, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("destination %q must not contain '..'", d)
		}
	}
	if c := path.Clean(strings.ReplaceAll(d, `\`, "/")); c == "." {
		return fmt.Errorf("destination %q is empty", d)
	}
	return nil
}

// Validate checks names, shortcuts, destinations and IDs of a whole set.
// Shortcuts must be unique among all tags, disabled ones included: the
// database enforces UNIQUE(profile, shortcut) regardless of the enabled flag,
// and a disabled tag can be re-enabled at any time.
func Validate(ts []Tag) error {
	ids := map[int]bool{}
	names := map[string]bool{}
	keys := map[string]string{}
	for _, t := range ts {
		label := t.Name
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("a tag has an empty name")
		}
		if t.ID <= 0 || ids[t.ID] {
			return fmt.Errorf("tag %q has an invalid or duplicate id", label)
		}
		ids[t.ID] = true
		ln := strings.ToLower(strings.TrimSpace(t.Name))
		if names[ln] {
			return fmt.Errorf("duplicate tag name %q", label)
		}
		names[ln] = true
		sc, err := NormalizeShortcut(t.Shortcut)
		if err != nil {
			return fmt.Errorf("tag %q: %w", label, err)
		}
		if sc != "" {
			if other, dup := keys[sc]; dup {
				return fmt.Errorf("shortcut %s is used by both %q and %q", sc, other, label)
			}
			keys[sc] = label
		}
		if err := ValidateDestination(t.Destination); err != nil {
			return fmt.Errorf("tag %q: %w", label, err)
		}
		if strings.TrimSpace(t.Color) != "" {
			if _, err := ParseColor(t.Color); err != nil {
				return fmt.Errorf("tag %q: %w", label, err)
			}
		}
	}
	return nil
}

// Normalize returns a cleaned copy: trimmed, upper-case shortcuts, sorted.
func Normalize(ts []Tag) []Tag {
	out := make([]Tag, len(ts))
	for i, t := range ts {
		t.Name = strings.TrimSpace(t.Name)
		t.Destination = strings.TrimSpace(t.Destination)
		t.DestinationTemplate = strings.TrimSpace(t.DestinationTemplate)
		t.Shortcut = strings.ToUpper(strings.TrimSpace(t.Shortcut))
		t.Color = strings.TrimSpace(t.Color)
		out[i] = t
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out
}

// NextID returns an id larger than every id in the given sets.
func NextID(sets ...[]Tag) int {
	m := 0
	for _, s := range sets {
		for _, t := range s {
			if t.ID > m {
				m = t.ID
			}
		}
	}
	return m + 1
}

// ByShortcut finds the enabled tag bound to key.
func ByShortcut(ts []Tag, key string) (Tag, bool) {
	key = strings.ToUpper(key)
	for _, t := range ts {
		if t.Enabled && t.Shortcut != "" && t.Shortcut == key {
			return t, true
		}
	}
	return Tag{}, false
}
