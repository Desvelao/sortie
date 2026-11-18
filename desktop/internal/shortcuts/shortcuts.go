// Package shortcuts defines the configurable application shortcuts and
// parses/validates them. It has no GUI dependency; the UI maps bindings to
// Fyne key names.
package shortcuts

import (
	"fmt"
	"strings"
)

// Action is something a shortcut can trigger.
type Action struct{ ID, Label, Default string }

// Actions lists all configurable actions with their defaults (PLAN §9).
var Actions = []Action{
	{"next", "Next file", "Right"},
	{"prev", "Previous file", "Left"},
	{"first", "First file", "Home"},
	{"last", "Last file", "End"},
	{"open_dir", "Select input directory", "Ctrl+O"},
	{"settings", "Open settings", "Ctrl+,"},
	{"copy_all", "Copy all classified files", "Ctrl+Shift+C"},
	{"copy_current", "Copy current file", "Ctrl+Enter"},
	{"focus_year", "Edit manual variable value", "Ctrl+Y"},
	{"play", "Play / pause audio", "Space"},
}

// Defaults returns action id → default binding text.
func Defaults() map[string]string {
	m := make(map[string]string, len(Actions))
	for _, a := range Actions {
		m[a.ID] = a.Default
	}
	return m
}

// Binding is a parsed shortcut: a key with optional modifiers.
type Binding struct {
	Ctrl, Alt, Shift bool
	Key              string // canonical key name
}

// Plain reports whether the binding has no modifiers. Plain bindings are
// delivered as ordinary key presses (and therefore conflict with tag keys).
func (b Binding) Plain() bool { return !b.Ctrl && !b.Alt && !b.Shift }

func (b Binding) String() string {
	var p []string
	if b.Ctrl {
		p = append(p, "Ctrl")
	}
	if b.Alt {
		p = append(p, "Alt")
	}
	if b.Shift {
		p = append(p, "Shift")
	}
	return strings.Join(append(p, b.Key), "+")
}

var namedKeys = map[string]string{
	"left": "Left", "right": "Right", "up": "Up", "down": "Down", "home": "Home", "end": "End",
	"pageup": "PageUp", "pagedown": "PageDown", "space": "Space", "return": "Return", "enter": "Return",
	"comma": "Comma", ",": "Comma", "period": "Period", ".": "Period", "slash": "Slash", "/": "Slash",
	"tab": "Tab", "backspace": "BackSpace", "delete": "Delete", "insert": "Insert",
	"plus": "Plus", "+": "Plus", "minus": "Minus", "-": "Minus",
}

// FyneKey maps the canonical key to the name Fyne uses.
func (b Binding) FyneKey() string {
	switch b.Key {
	case "Comma":
		return ","
	case "Period":
		return "."
	case "Slash":
		return "/"
	case "Plus":
		return "+"
	case "Minus":
		return "-"
	case "PageUp":
		return "Prior"
	case "PageDown":
		return "Next"
	case "BackSpace":
		return "BackSpace"
	}
	return b.Key
}

func canonicalKey(k string) (string, error) {
	lk := strings.ToLower(k)
	if v, ok := namedKeys[lk]; ok {
		return v, nil
	}
	if len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= 'A' && k[0] <= 'Z') {
		return strings.ToUpper(k), nil
	}
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		return k, nil
	}
	if len(lk) >= 2 && lk[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(lk[1:], "%d", &n); err == nil && n >= 1 && n <= 12 && fmt.Sprint(n) == lk[1:] {
			return fmt.Sprintf("F%d", n), nil
		}
	}
	if lk == "escape" || lk == "esc" {
		return "", fmt.Errorf("Escape is reserved for closing dialogs")
	}
	return "", fmt.Errorf("unknown key %q", k)
}

// Parse reads text such as "Ctrl+Shift+C", "Right" or "ctrl+,".
func Parse(s string) (Binding, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Binding{}, fmt.Errorf("shortcut is empty")
	}
	// a trailing "+" key ("Ctrl++") or "," need no special handling: split on "+" only
	parts := strings.Split(s, "+")
	if strings.HasSuffix(s, "++") {
		parts = append(parts[:len(parts)-2], "+")
	} else if s == "+" {
		parts = []string{"+"}
	}
	var b Binding
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "ctrl", "control":
			b.Ctrl = true
		case "alt":
			b.Alt = true
		case "shift":
			b.Shift = true
		default:
			return Binding{}, fmt.Errorf("unknown modifier %q in %q", p, s)
		}
	}
	key, err := canonicalKey(strings.TrimSpace(parts[len(parts)-1]))
	if err != nil {
		return Binding{}, err
	}
	b.Key = key
	if b.Shift && !b.Ctrl && !b.Alt {
		return Binding{}, fmt.Errorf("Shift must be combined with Ctrl or Alt (%q)", s)
	}
	if b.Plain() && b.Key == "Tab" {
		return Binding{}, fmt.Errorf("Tab alone is reserved for moving between controls (%q)", s)
	}
	return b, nil
}

// Resolve parses every action's binding (missing actions use the default),
// rejects unknown actions, duplicates, and plain keys that collide with tag
// shortcuts (tagKeys, canonical upper-case like "1" or "F5").
func Resolve(m map[string]string, tagKeys []string) (map[string]Binding, error) {
	known := map[string]bool{}
	for _, a := range Actions {
		known[a.ID] = true
	}
	for id := range m {
		if !known[id] {
			return nil, fmt.Errorf("unknown shortcut action %q", id)
		}
	}
	out := map[string]Binding{}
	used := map[string]string{}
	tagSet := map[string]bool{}
	for _, k := range tagKeys {
		if k != "" {
			tagSet[strings.ToUpper(k)] = true
		}
	}
	for _, a := range Actions {
		text, ok := m[a.ID]
		if !ok || strings.TrimSpace(text) == "" {
			text = a.Default
		}
		b, err := Parse(text)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Label, err)
		}
		if other, dup := used[b.String()]; dup {
			return nil, fmt.Errorf("%s is used by both %q and %q", b, other, a.Label)
		}
		used[b.String()] = a.Label
		if b.Plain() && tagSet[strings.ToUpper(b.Key)] {
			return nil, fmt.Errorf("%s: key %s is already a tag shortcut", a.Label, b.Key)
		}
		out[a.ID] = b
	}
	return out, nil
}

// ResolveLenient is Resolve for runtime use: instead of failing as a whole, an
// action whose configured binding is invalid or clashes (with another action
// or a tag key) falls back to its default binding, and is left unbound if that
// clashes too. One bad entry therefore never resets the others. The returned
// problems describe every deviation; unknown action ids are ignored.
func ResolveLenient(m map[string]string, tagKeys []string) (map[string]Binding, []error) {
	var problems []error
	known := map[string]bool{}
	for _, a := range Actions {
		known[a.ID] = true
	}
	for id := range m {
		if !known[id] {
			problems = append(problems, fmt.Errorf("unknown shortcut action %q ignored", id))
		}
	}
	tagSet := map[string]bool{}
	for _, k := range tagKeys {
		if k != "" {
			tagSet[strings.ToUpper(k)] = true
		}
	}
	out := map[string]Binding{}
	used := map[string]string{}
	try := func(a Action, text string) (Binding, error) {
		b, err := Parse(text)
		if err != nil {
			return b, err
		}
		if other, dup := used[b.String()]; dup {
			return b, fmt.Errorf("%s is already used by %q", b, other)
		}
		if b.Plain() && tagSet[strings.ToUpper(b.Key)] {
			return b, fmt.Errorf("key %s is already a tag shortcut", b.Key)
		}
		return b, nil
	}
	for _, a := range Actions {
		text, custom := m[a.ID]
		if strings.TrimSpace(text) == "" {
			text, custom = a.Default, false
		}
		b, err := try(a, text)
		if err != nil && custom {
			problems = append(problems, fmt.Errorf("%s: %w; using the default %s", a.Label, err, a.Default))
			b, err = try(a, a.Default)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w; action left unbound", a.Label, err))
			continue
		}
		used[b.String()] = a.Label
		out[a.ID] = b
	}
	return out, problems
}

// Validate is Resolve without the result.
func Validate(m map[string]string, tagKeys []string) error {
	_, err := Resolve(m, tagKeys)
	return err
}
