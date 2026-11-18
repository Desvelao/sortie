package profile_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sortie/internal/pathtpl"
	. "sortie/internal/profile"
	"sortie/internal/vars"
)

// The example profiles shipped in docs/profiles must stay valid and behave as
// their comments claim.
func exampleDir() string { return filepath.Join("..", "..", "..", "docs", "profiles") }

func TestShippedExamplesAreValid(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(exampleDir(), "*.yaml"))
	if len(files) < 4 {
		t.Fatalf("expected the example profiles, found %v", files)
	}
	for _, f := range files {
		p, err := Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if err := p.Resolved(exampleDir()).ValidateResolved(); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		// every tag must resolve to a destination for a fully resolved file
		set, _ := p.VarSet()
		for _, tg := range p.ToTags(nil) {
			if err := pathtpl.Validate(p.TemplateFor(p.Tags[tg.SortOrder]), set.Names()); err != nil {
				t.Errorf("%s: tag %s: %v", f, tg.Name, err)
			}
		}
	}
}

// render resolves the destination (relative) of a file under a profile.
func render(t *testing.T, p Profile, tag int, name, relDir string, fields map[string]string, mtime time.Time) (string, error) {
	t.Helper()
	set, err := p.VarSet()
	if err != nil {
		t.Fatal(err)
	}
	vals := set.ResolveAll(vars.FileContext{Name: name, RelDir: relDir, ModTime: mtime, Fields: func() map[string]string { return fields }})
	ts := p.ToTags(nil)
	parts, err := pathtpl.Render(p.TemplateFor(p.Tags[tag]), func(n string) (string, bool) {
		switch n {
		case "tag":
			return ts[tag].Destination, true
		case "filename":
			return name, true
		case "name":
			return strings.TrimSuffix(name, filepath.Ext(name)), true
		case "ext":
			return filepath.Ext(name), true
		}
		v, ok := vals[n]
		return v.Value, ok && v.OK()
	})
	return strings.Join(parts, "/"), err
}

func TestExampleProfilesBehaveAsDocumented(t *testing.T) {
	load := func(n string) Profile {
		p, err := Load(filepath.Join(exampleDir(), n))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	mt := time.Date(2018, 7, 1, 0, 0, 0, 0, time.UTC)

	fam := load("family-photos.yaml")
	for _, c := range []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"IMG_2019_0517.jpg", nil, "2019/Family/IMG_2019_0517.jpg"},
		{"photo.jpg", map[string]string{"year": "2015"}, "2015/Family/photo.jpg"},
		{"photo.jpg", nil, "2018/Family/photo.jpg"}, // falls through to mtime
	} {
		if got, err := render(t, fam, 0, c.name, "", c.fields, mt); err != nil || got != c.want {
			t.Errorf("family %s: %q %v want %q", c.name, got, err, c.want)
		}
	}

	music := load("music-library.yaml")
	if got, _ := render(t, music, 0, "a.mp3", "", map[string]string{"artist": "Daft Punk", "album": "Discovery"}, mt); got != "Daft Punk/Discovery/a.mp3" {
		t.Errorf("music: %q", got)
	}
	if got, _ := render(t, music, 0, "a.mp3", "", nil, mt); got != "Unknown artist/Unknown album/a.mp3" {
		t.Errorf("music fallback: %q", got)
	}
	if got, _ := render(t, music, 1, "a.mp3", "", map[string]string{"artist": "Daft Punk"}, mt); got != "Favourites/Daft Punk/a.mp3" {
		t.Errorf("music favourite: %q", got)
	}

	docs := load("client-documents.yaml")
	if docs.ConflictPolicy != ConflictRename {
		t.Errorf("conflict policy %q", docs.ConflictPolicy)
	}
	if got, _ := render(t, docs, 0, "INV-0042.pdf", "clients/Acme/scans", nil, mt); got != "Acme/2018/Invoices/INV-0042.pdf" {
		t.Errorf("client: %q", got)
	}
	if got, _ := render(t, docs, 0, "x_2021.pdf", "loose", nil, mt); got != "unassigned/2021/Invoices/x_2021.pdf" {
		t.Errorf("client fallback: %q", got)
	}
	if got, _ := render(t, docs, 3, "x_2021.pdf", "clients/Acme", nil, mt); got != "Tax/2021/Acme/x_2021.pdf" {
		t.Errorf("tax: %q", got)
	}

	inv := load("invoices.yaml")
	if got, _ := render(t, inv, 0, "INV-0042_scan.pdf", "", map[string]string{"date": "2021-03-09T10:00:00Z"}, mt); got != "2021-03/Invoices/INV-0042/INV-0042_scan.pdf" {
		t.Errorf("invoice: %q", got)
	}
	if got, _ := render(t, inv, 0, "scan.pdf", "", nil, mt); got != "2018-07/Invoices/no-number/scan.pdf" {
		t.Errorf("unnumbered: %q", got)
	}
}
