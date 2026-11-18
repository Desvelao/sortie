package profile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "sortie/internal/profile"
	"sortie/internal/vars"
)

const good = `version: 1
name: Family photos
input_directory: ~/Pictures/inbox
recursive: true
destination_root: /mnt/archive
destination_template: "{year}/{tag}/{filename}"
variables:
  - name: year
    sources:
      - {kind: manual}
      - {kind: filename, pattern: '(^|[^0-9])(20[0-9]{2})([^0-9]|$)', group: 2}
    validate: {min: 1970, max: 2100}
    fallback: unknown
tags:
  - {name: Family, shortcut: "1"}
  - {name: Work, shortcut: "2", destination: Jobs, destination_template: "{tag}/{year}/{filename}"}
  - {name: Old, enabled: false}
`

func TestParseGood(t *testing.T) {
	p, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	ts := p.ToTags(map[string]int{"Work": 5})
	if len(ts) != 3 || ts[0].Destination != "Family" || ts[1].Destination != "Jobs" || ts[1].ID != 5 ||
		!ts[0].Enabled || ts[2].Enabled || ts[2].SortOrder != 2 {
		t.Fatalf("%+v", ts)
	}
	if p.TemplateFor(p.Tags[0]) != "{year}/{tag}/{filename}" || p.TemplateFor(p.Tags[1]) != "{tag}/{year}/{filename}" {
		t.Fatal("template selection")
	}
	set, _ := p.VarSet()
	if v, _ := set.Resolve("year", vars.FileContext{Name: "x_2020.pdf"}); v.Value != "2020" {
		t.Fatalf("%+v", v)
	}
}

func TestErrorsCarryLines(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"unknown key", "version: 1\nname: X\ntags: [{name: A}]\ncolour: red\n", "line 4"},
		{"wrong type", "version: 1\nname: X\nrecursive: maybe\ntags: [{name: A}]\n", "line 3"},
		{"bad template", "version: 1\nname: X\ndestination_template: \"{nope}/{filename}\"\ntags: [{name: A}]\n", "line 3"},
		{"bad tag template", "version: 1\nname: X\ntags:\n  - {name: A}\n  - {name: B, destination_template: \"{bad}/{filename}\"}\n", "line 5"},
		{"bad variable", "version: 1\nname: X\nvariables:\n  - name: a\n    sources: [{kind: manual}]\n  - name: b\n    sources: [{kind: filename, pattern: '(unclosed'}]\ntags: [{name: A}]\n", "line 6"},
		{"duplicate tag", "version: 1\nname: X\ntags:\n  - {name: A}\n  - {name: a}\n", "duplicate tag"},
		{"dup shortcut", "version: 1\nname: X\ntags:\n  - {name: A, shortcut: \"1\"}\n  - {name: B, shortcut: \"1\"}\n", "shortcut 1"},
		{"traversal", "version: 1\nname: X\ntags:\n  - {name: A, destination: \"../x\"}\n", "line 4"},
		{"no tags", "version: 1\nname: X\ntags: []\n", "at least one tag"},
		{"newer version", "version: 9\nname: X\ntags: [{name: A}]\n", "newer"},
		{"conflict", "version: 1\nname: X\nconflict_policy: overwrite\ntags: [{name: A}]\n", "conflict_policy"},
		{"empty", "", "empty"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.yaml))
		if err == nil {
			t.Errorf("%s: expected error", c.name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, err, c.want)
		}
	}
	_, err := Parse([]byte("version: 1\nname: X\ndestination_template: \"{nope}/{filename}\"\ntags: [{name: A}]\n"))
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Line != 3 || ve.Path != "destination_template" {
		t.Fatalf("%#v", err)
	}
}

func TestRoundTripAndSave(t *testing.T) {
	p := Example("Round trip")
	path := filepath.Join(t.TempDir(), "sub", "p.yaml")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Marshal(p)
	b, _ := Marshal(got)
	if string(a) != string(b) {
		t.Fatalf("round trip differs:\n%s\n---\n%s", a, b)
	}
	// an invalid profile must not replace the saved file
	bad := p
	bad.DestinationTemplate = "{nope}/{filename}"
	if err := Save(path, bad); err == nil {
		t.Fatal("invalid profile saved")
	}
	if again, _ := Load(path); again.DestinationTemplate != p.DestinationTemplate {
		t.Fatal("file changed by failed save")
	}
}

func TestResolvedPaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	p := Profile{InputDirectory: "~/in", DestinationRoot: "out/../archive"}.Resolved("/proj")
	if p.InputDirectory != filepath.Join(home, "in") || p.DestinationRoot != "/proj/archive" {
		t.Fatalf("%+v", p)
	}
	if (Profile{}).Resolved("/proj").InputDirectory != "" {
		t.Fatal("empty path must stay empty")
	}
}

func TestListAndIDs(t *testing.T) {
	dir := t.TempDir()
	Save(PathFor(dir, "zeta"), New2("Zeta"))
	Save(PathFor(dir, "alpha"), New2("alpha"))
	os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: [\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden.yaml"), []byte("x"), 0o644)
	es, err := List(dir)
	if err != nil || len(es) != 3 {
		t.Fatalf("%v %v", es, err)
	}
	if es[0].Name != "alpha" || es[1].ID != "broken" || es[1].Err == nil || es[2].Name != "Zeta" {
		t.Fatalf("%+v", es)
	}
	if es, err := List(filepath.Join(dir, "missing")); err != nil || len(es) != 0 {
		t.Fatalf("%v %v", es, err)
	}
	for _, bad := range []string{"", "..", "a/b", ".x"} {
		if ValidID(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func New2(name string) Profile {
	p := New(name)
	p.Tags = []Tag{{Name: "A"}}
	return p
}

func TestEnsureDefaultAndActivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	made, err := EnsureDefault(dir)
	if err != nil || !made {
		t.Fatal(made, err)
	}
	id, p, warn := Activate(dir, "default")
	if id != "default" || warn != nil || len(p.Tags) == 0 || len(p.Variables) == 0 {
		t.Fatalf("%s %+v %v", id, p, warn)
	}
	// a user's edits survive a second start
	edited, _ := Load(PathFor(dir, DefaultID))
	edited.Name = "Mine"
	Save(PathFor(dir, DefaultID), edited)
	// a folder that already has files is left alone, valid or not
	if made, _ := EnsureDefault(dir); made {
		t.Fatal("wrote into a populated folder")
	}
	if p, _ := Load(PathFor(dir, DefaultID)); p.Name != "Mine" {
		t.Fatal("profile overwritten")
	}
	// preferred profile broken → fall back to a valid one and say so
	os.WriteFile(filepath.Join(dir, "work.yaml"), []byte("name: [\n"), 0o644)
	id, _, warn = Activate(dir, "work")
	if id != "default" || warn == nil || !strings.Contains(warn.Error(), "work") {
		t.Fatalf("%s %v", id, warn)
	}
	// nothing valid → unsaved starter, files untouched
	os.Remove(PathFor(dir, "default"))
	id, p, warn = Activate(dir, "work")
	if id != RecoveryID || warn == nil || len(p.Tags) == 0 {
		t.Fatalf("%s %v", id, warn)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "work.yaml")); string(b) != "name: [\n" {
		t.Fatal("broken profile was modified")
	}
	if _, err := os.Stat(PathFor(dir, RecoveryID)); err == nil {
		t.Fatal("recovery profile must stay in memory")
	}
}
