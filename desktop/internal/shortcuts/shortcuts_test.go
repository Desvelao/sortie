package shortcuts

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]string{
		"Right": "Right", "ctrl+shift+c": "Ctrl+Shift+C", "Ctrl+,": "Ctrl+Comma", "ctrl+enter": "Ctrl+Return",
		"f5": "F5", "Alt+Left": "Alt+Left", "space": "Space", "7": "7", "Ctrl+.": "Ctrl+Period",
		"Ctrl++": "Ctrl+Plus", "Ctrl+-": "Ctrl+Minus", "ctrl+plus": "Ctrl+Plus", "+": "Plus", "Ctrl+Tab": "Ctrl+Tab",
	}
	for in, want := range ok {
		b, err := Parse(in)
		if err != nil || b.String() != want {
			t.Errorf("%q → %v %v (want %s)", in, b, err, want)
		}
	}
	for _, bad := range []string{"", "Ctrl+", "Hyper+A", "Shift+A", "Shift+Right", "Escape", "Ctrl+Esc", "F13", "Ctrl+Foo", "ab", "Tab"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if b, _ := Parse("Ctrl+,"); b.FyneKey() != "," || b.Plain() {
		t.Error("fyne key / plain")
	}
	if b, _ := Parse("Ctrl++"); b.FyneKey() != "+" {
		t.Error("plus fyne key")
	}
	if b, _ := Parse("PageDown"); b.FyneKey() != "Next" || !b.Plain() {
		t.Error("PageDown")
	}
}

func TestResolveDefaultsAndPartialOverrides(t *testing.T) {
	r, err := Resolve(nil, []string{"1", "2", "F5"})
	if err != nil || len(r) != len(Actions) || r["next"].String() != "Right" {
		t.Fatal(r, err)
	}
	r, err = Resolve(map[string]string{"next": "N", "prev": "P"}, nil)
	if err != nil || r["next"].Key != "N" || r["first"].String() != "Home" {
		t.Fatal(r, err)
	}
}

func TestResolveRejectsConflicts(t *testing.T) {
	bad := map[string]map[string]string{
		"duplicate":    {"next": "Left"},
		"unknown act":  {"fly": "F1"},
		"bad binding":  {"next": "Shift+X"},
		"tag conflict": {"next": "1"},
	}
	for n, m := range bad {
		if _, err := Resolve(m, []string{"1"}); err == nil {
			t.Errorf("%s accepted", n)
		}
	}
	// a modified binding never conflicts with a plain tag key
	if _, err := Resolve(map[string]string{"next": "Ctrl+1"}, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Defaults(), []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLenientKeepsOtherCustomBindings(t *testing.T) {
	// "next" clashes with tag key 1, "prev" is fine: only "next" falls back
	r, problems := ResolveLenient(map[string]string{"next": "1", "prev": "P", "first": "Shift+X"}, []string{"1"})
	if r["next"].String() != "Right" || r["prev"].Key != "P" || r["first"].String() != "Home" {
		t.Fatalf("%v", r)
	}
	if len(problems) != 2 {
		t.Fatalf("want 2 problems, got %v", problems)
	}
	if len(r) != len(Actions) {
		t.Fatalf("every action should still be bound: %d of %d", len(r), len(Actions))
	}
	// the default itself clashing leaves the action unbound instead of failing
	r, problems = ResolveLenient(map[string]string{"next": "1"}, []string{"1", "RIGHT"})
	if _, ok := r["next"]; ok || len(problems) == 0 {
		t.Fatalf("%v %v", r, problems)
	}
	// duplicates between custom bindings: the first action wins
	r, problems = ResolveLenient(map[string]string{"next": "N", "prev": "N"}, nil)
	if r["next"].Key != "N" || r["prev"].String() != "Left" || len(problems) != 1 {
		t.Fatalf("%v %v", r, problems)
	}
	// unknown actions are ignored with a note
	if _, problems := ResolveLenient(map[string]string{"fly": "F1"}, nil); len(problems) != 1 {
		t.Fatal(problems)
	}
	if _, problems := ResolveLenient(nil, []string{"1"}); len(problems) != 0 {
		t.Fatal(problems)
	}
}
