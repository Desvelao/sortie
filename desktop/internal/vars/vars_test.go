package vars

import (
	"strings"
	"testing"
	"time"
)

func ip(i int) *int { return &i }

func yearDef() Definition {
	return Definition{
		Name: "year",
		Sources: []Source{
			{Kind: KindManual},
			{Kind: KindFilename, Pattern: `(^|[^0-9])(20[0-9]{2})([^0-9]|$)`, Group: 2},
			{Kind: KindMetadata, Field: "date", Format: "2006"},
		},
		Validate: Validation{Min: ip(1970), Max: ip(2100)},
	}
}

func mustSet(t *testing.T, defs ...Definition) *Set {
	t.Helper()
	s, err := New(defs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Parity with the former year extractor's filename cases.
func TestYearPresetFilename(t *testing.T) {
	s := mustSet(t, yearDef())
	cases := map[string]string{
		"IMG_20210315.jpg":    "",
		"Invoice_2020.pdf":    "2020",
		"2023-05-17 trip.jpg": "2023",
		"unknown_file.pdf":    "",
		"a_1999_b_2005.txt":   "2005",
		"2019-2020.txt":       "2019",
		"ñandú_2018_日本.pdf":   "2018",
		"x2024":               "2024",
		"2150.txt":            "",
	}
	for name, want := range cases {
		v, _ := s.Resolve("year", FileContext{Name: name})
		if v.Value != want {
			t.Errorf("%s: got %q (%s) want %q", name, v.Value, v.Detail, want)
		}
	}
}

func TestInvalidAndMultipleCandidates(t *testing.T) {
	d := yearDef()
	d.Sources[1].Pattern, d.Sources[1].Group = `(^|[^0-9])([0-9]{4})([^0-9]|$)`, 2
	s := mustSet(t, d)
	if v, _ := s.Resolve("year", FileContext{Name: "scan_1850_then_2004.pdf"}); v.Value != "2004" {
		t.Fatalf("%+v", v)
	}
	v, _ := s.Resolve("year", FileContext{Name: "scan_1850.pdf"})
	if v.Status != StatusInvalid || !strings.Contains(v.Detail, "1850") {
		t.Fatalf("%+v", v)
	}
}

func TestPriorityChain(t *testing.T) {
	s := mustSet(t, yearDef())
	meta := func() map[string]string { return map[string]string{"date": "2011:05:01 10:00:00"} }
	bad := func() map[string]string { return map[string]string{"date": "1800-01-01"} }

	if v, _ := s.Resolve("year", FileContext{Name: "x_2020.txt", Manual: map[string]string{"year": "1999"}, Fields: meta}); v.Value != "1999" || v.Source != KindManual {
		t.Fatalf("%+v", v)
	}
	if v, _ := s.Resolve("year", FileContext{Name: "x_2020.txt", Fields: meta}); v.Value != "2020" || v.Source != KindFilename {
		t.Fatalf("%+v", v)
	}
	if v, _ := s.Resolve("year", FileContext{Name: "none.txt", Fields: meta}); v.Value != "2011" || v.Source != KindMetadata {
		t.Fatalf("%+v", v)
	}
	if v, _ := s.Resolve("year", FileContext{Name: "none.txt", Fields: bad}); v.OK() {
		t.Fatalf("%+v", v)
	}
}

func TestMetadataLazy(t *testing.T) {
	s := mustSet(t, yearDef())
	called := false
	s.ResolveAll(FileContext{Name: "a_2020.txt", Fields: func() map[string]string { called = true; return nil }})
	if called {
		t.Fatal("metadata read although filename resolved")
	}
}

func TestPathMTimeConstantFallback(t *testing.T) {
	s := mustSet(t,
		Definition{Name: "client", Sources: []Source{{Kind: KindPath, Pattern: `^clients/([^/]+)`}}, Fallback: "misc"},
		Definition{Name: "month", Sources: []Source{{Kind: KindMTime, Format: "01"}}},
		Definition{Name: "kind", Sources: []Source{{Kind: KindConstant, Value: "scan"}}},
	)
	fc := FileContext{Name: "a.pdf", RelDir: "clients/acme/2020", ModTime: time.Date(2022, 3, 4, 0, 0, 0, 0, time.UTC)}
	got := s.ResolveAll(fc)
	if got["client"].Value != "acme" || got["month"].Value != "03" || got["kind"].Value != "scan" {
		t.Fatalf("%+v", got)
	}
	fc.RelDir = "other"
	if v := s.ResolveAll(fc)["client"]; v.Value != "misc" || !v.OK() {
		t.Fatalf("%+v", v)
	}
}

func TestValidateRegexAndConfigErrors(t *testing.T) {
	s := mustSet(t, Definition{Name: "code", Sources: []Source{{Kind: KindFilename, Pattern: `([A-Z]+-\d+)`}}, Validate: Validation{Regex: `^ABC-`}})
	if v, _ := s.Resolve("code", FileContext{Name: "XYZ-1 ABC-22.txt"}); v.Value != "ABC-22" {
		t.Fatalf("%+v", v)
	}
	bad := []Definition{
		{Name: "1x", Sources: []Source{{Kind: KindManual}}},
		{Name: "tag", Sources: []Source{{Kind: KindManual}}},
		{Name: "a"},
		{Name: "a", Sources: []Source{{Kind: "nope"}}},
		{Name: "a", Sources: []Source{{Kind: KindFilename, Pattern: "(unclosed"}}},
		{Name: "a", Sources: []Source{{Kind: KindFilename, Pattern: `(\d)`, Group: 2}}},
		{Name: "a", Sources: []Source{{Kind: KindFilename}}},
		{Name: "a", Sources: []Source{{Kind: KindMetadata}}},
		{Name: "a", Sources: []Source{{Kind: KindManual}}, Validate: Validation{Min: ip(5), Max: ip(1)}},
	}
	for i, d := range bad {
		if _, err := New([]Definition{d}); err == nil {
			t.Errorf("case %d: expected error", i)
		} else if strings.Contains(err.Error(), "error parsing regexp") {
			t.Errorf("unfriendly message: %v", err)
		}
	}
	if _, err := New([]Definition{yearDef(), yearDef()}); err == nil {
		t.Error("duplicate names accepted")
	}
}

// A pattern without capture groups uses the whole match.
func TestFilenameWholeMatchWithoutGroup(t *testing.T) {
	s := mustSet(t, Definition{Name: "y", Sources: []Source{{Kind: KindFilename, Pattern: `20[0-9]{2}`}}})
	if v, _ := s.Resolve("y", FileContext{Name: "trip_2021.jpg"}); v.Value != "2021" {
		t.Errorf("got %q (%s) want 2021", v.Value, v.Detail)
	}
	// a pattern with groups still defaults to group 1
	s = mustSet(t, Definition{Name: "c", Sources: []Source{{Kind: KindFilename, Pattern: `x_(\d+)`}}})
	if v, _ := s.Resolve("c", FileContext{Name: "x_42.jpg"}); v.Value != "42" {
		t.Errorf("got %q want 42", v.Value)
	}
}
