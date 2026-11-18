package tags

import (
	"fmt"
	"testing"
)

func TestDefaultsValid(t *testing.T) {
	if err := Validate(Defaults()); err != nil {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	mk := func(mod func(ts []Tag)) []Tag { ts := Defaults(); mod(ts); return ts }
	bad := map[string][]Tag{
		"dup name":      mk(func(ts []Tag) { ts[1].Name = "family" }),
		"dup shortcut":  mk(func(ts []Tag) { ts[1].Shortcut = "1" }),
		"reserved":      mk(func(ts []Tag) { ts[0].Shortcut = "Left" }),
		"bad shortcut":  mk(func(ts []Tag) { ts[0].Shortcut = "Ctrl+1" }),
		"F13":           mk(func(ts []Tag) { ts[0].Shortcut = "F13" }),
		"empty name":    mk(func(ts []Tag) { ts[0].Name = "  " }),
		"traversal":     mk(func(ts []Tag) { ts[0].Destination = "../etc" }),
		"traversal mid": mk(func(ts []Tag) { ts[0].Destination = "a/../../b" }),
		"absolute":      mk(func(ts []Tag) { ts[0].Destination = "/etc" }),
		"empty dest":    mk(func(ts []Tag) { ts[0].Destination = "" }),
		"dot dest":      mk(func(ts []Tag) { ts[0].Destination = "." }),
		"dup id":        mk(func(ts []Tag) { ts[1].ID = 1 }),
	}
	for n, ts := range bad {
		if Validate(ts) == nil {
			t.Errorf("%s: expected error", n)
		}
	}
	ok := mk(func(ts []Tag) {
		ts[0].Shortcut = "f5"
		ts[1].Shortcut = ""
		ts[2].Shortcut = ""
		ts[0].Destination = "Music/Live Sets"
	})
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
}

func TestByShortcutAndNextID(t *testing.T) {
	ts := Defaults()
	ts[2].Enabled = false
	if tg, ok := ByShortcut(ts, "1"); !ok || tg.Name != "Family" {
		t.Fatal(tg, ok)
	}
	if _, ok := ByShortcut(ts, "3"); ok {
		t.Fatal("disabled tag matched")
	}
	if NextID(ts, []Tag{{ID: 50}}) != 51 {
		t.Fatal("nextid")
	}
}

func TestParseColorAndShortcutEdgeCases(t *testing.T) {
	for in, want := range map[string]string{"#ff8800": "ff8800", "FF8800": "ff8800", "#f80": "ff8800", " #000 ": "000000"} {
		c, err := ParseColor(in)
		if err != nil || fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B) != want {
			t.Errorf("ParseColor(%q) = %v %v", in, c, err)
		}
	}
	for _, bad := range []string{"", "#", "#12", "#1234", "#gggggg", "red", "#1234567"} {
		if _, err := ParseColor(bad); err == nil {
			t.Errorf("ParseColor(%q) accepted", bad)
		}
	}
	for in, want := range map[string]string{"": "", " a ": "A", "f12": "F12", "7": "7"} {
		if got, err := NormalizeShortcut(in); err != nil || got != want {
			t.Errorf("NormalizeShortcut(%q) = %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"F13", "F0", "F01", "F-1", "AB", "tab", "Space", "ñ", "?"} {
		if _, err := NormalizeShortcut(bad); err == nil {
			t.Errorf("NormalizeShortcut(%q) accepted", bad)
		}
	}
	for _, bad := range []string{`C:\x`, "c:/x", `\abs`, "/abs", "a/../b", `a\..\b`, ".", "./", "a\x00b"} {
		if err := ValidateDestination(bad); err == nil {
			t.Errorf("ValidateDestination(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"Work", "Work/Invoices", `Work\Invoices`, "a b/c", "..hidden"} {
		if err := ValidateDestination(ok); err != nil {
			t.Errorf("ValidateDestination(%q): %v", ok, err)
		}
	}
}
