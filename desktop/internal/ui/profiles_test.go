package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/app"
	"sortie/internal/config"
	"sortie/internal/fileinfo"
	"sortie/internal/profile"
	"sortie/internal/vars"
)

// profileWindow returns a window whose profiles live in a temp folder: "a"
// (starter profile, active) and "b" (one tag, no variables).
func profileWindow(t *testing.T) (*MainWindow, *app.Application, string) {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	dir := filepath.Join(t.TempDir(), "profiles")
	pa := profile.Example("Alpha")
	pa.Recursive = false
	pb := profile.New("Beta")
	pb.Tags = []profile.Tag{{Name: "Only", Shortcut: "1"}}
	for id, p := range map[string]profile.Profile{"a": pa, "b": pb} {
		if err := profile.Save(profile.PathFor(dir, id), p); err != nil {
			t.Fatal(err)
		}
	}
	core := app.New()
	if err := core.ApplyProfile("a", pa, dir); err != nil {
		t.Fatal(err)
	}
	mw := NewMainWindow(a, core, config.Default(), filepath.Join(t.TempDir(), "config.json"))
	mw.SetProfileDir(dir)
	mw.refreshProfiles()
	mw.rememberProfileFile()
	return mw, core, dir
}

// touch rewrites a profile file as an external editor would.
func touch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(5 * time.Second)
	os.Chtimes(path, later, later)
}

func TestProfileSelectorSwitchesAndKeepsTaggingSeparate(t *testing.T) {
	mw, core, _ := profileWindow(t)
	if len(mw.profSelect.Options) != 2 || mw.profSelect.Selected != "Alpha" {
		t.Fatalf("%v %q", mw.profSelect.Options, mw.profSelect.Selected)
	}
	files := []fileinfo.FileInfo{{Path: "/x/a.txt", Name: "a.txt", ModTime: time.Unix(1, 0)}}
	core.ApplyScan("/x", files, false)
	typeKey(mw.Window(), "1") // Alpha: Family

	mw.profSelect.SetSelected("Beta")
	if core.ProfileID() != "b" || mw.cfg.ActiveProfile != "b" || len(core.Tags()) != 1 {
		t.Fatalf("not switched: %s %q", core.ProfileID(), mw.cfg.ActiveProfile)
	}
	core.ApplyScan("/x", files, false)
	if core.TaggedCount() != 0 {
		t.Fatal("tagging leaked into the other profile")
	}
	mw.profSelect.SetSelected("Alpha")
	core.ApplyScan("/x", files, false)
	if core.ProfileID() != "a" || core.TaggedCount() != 1 {
		t.Fatalf("%s %d", core.ProfileID(), core.TaggedCount())
	}
}

func TestSwitchingToInvalidProfileIsRefused(t *testing.T) {
	mw, core, dir := profileWindow(t)
	touch(t, profile.PathFor(dir, "broken"), "version: 1\nname: B\nbogus: 1\ntags: [{name: A}]\n")
	mw.refreshProfiles()
	var bad string
	for _, o := range mw.profSelect.Options {
		if strings.Contains(o, "invalid") {
			bad = o
		}
	}
	if bad == "" {
		t.Fatalf("invalid profile not listed: %v", mw.profSelect.Options)
	}
	mw.profSelect.SetSelected(bad)
	if core.ProfileID() != "a" || mw.profSelect.Selected != "Alpha" {
		t.Fatalf("%s %q", core.ProfileID(), mw.profSelect.Selected)
	}
}

func TestExternalEditIsPickedUp(t *testing.T) {
	mw, core, dir := profileWindow(t)
	path := profile.PathFor(dir, "a")
	mw.checkProfileFile() // unchanged: nothing happens
	if len(core.Tags()) != 9 {
		t.Fatal("changed without an edit")
	}
	touch(t, path, "version: 1\nname: Alpha edited\nrecursive: true\ntags:\n  - {name: Solo, shortcut: \"1\"}\n")
	mw.checkProfileFile()
	if got := core.Profile(); got.Name != "Alpha edited" || len(core.Tags()) != 1 || !got.Recursive {
		t.Fatalf("%+v", got)
	}
	if !mw.recursive.Checked {
		t.Fatal("Recursive checkbox not updated")
	}
	if mw.profSelect.Selected != "Alpha edited" {
		t.Fatalf("selector shows %q", mw.profSelect.Selected)
	}
}

func TestBrokenExternalEditKeepsPreviousProfile(t *testing.T) {
	mw, core, dir := profileWindow(t)
	path := profile.PathFor(dir, "a")
	touch(t, path, "version: 1\nname: [\n")
	mw.checkProfileFile()
	if core.Profile().Name != "Alpha" || len(core.Tags()) != 9 {
		t.Fatalf("profile in use changed: %+v", core.Profile().Name)
	}
	if !strings.Contains(mw.statusLabel.Text, "errors") {
		t.Fatalf("status: %q", mw.statusLabel.Text)
	}
	// the same broken version is not reported again on the next tick
	mw.statusLabel.SetText("")
	mw.checkProfileFile()
	if mw.statusLabel.Text != "" {
		t.Fatal("retried an unchanged broken file")
	}
	// fixing it is picked up
	touch(t, path, "version: 1\nname: Fixed\ntags: [{name: T}]\n")
	mw.checkProfileFile()
	if core.Profile().Name != "Fixed" {
		t.Fatal("fix not applied")
	}
}

func TestNoReloadWhileSettingsAreOpen(t *testing.T) {
	mw, core, dir := profileWindow(t)
	touch(t, profile.PathFor(dir, "a"), "version: 1\nname: Changed\ntags: [{name: T}]\n")
	mw.dialogOpen = true
	mw.checkProfileFile()
	if core.Profile().Name != "Alpha" {
		t.Fatal("reloaded under an open dialog")
	}
	mw.dialogOpen = false
	mw.checkProfileFile()
	if core.Profile().Name != "Changed" {
		t.Fatal("change lost after the dialog closed")
	}
}

func TestOwnSaveIsNotTreatedAsExternalEdit(t *testing.T) {
	mw, core, dir := profileWindow(t)
	// a hand-written comment must survive a change made in the app
	path := profile.PathFor(dir, "a")
	b, _ := os.ReadFile(path)
	touch(t, path, "# my notes\n"+string(b))
	mw.checkProfileFile()
	if !mw.updateProfile(func(p *profile.Profile) { p.Recursive = true }) {
		t.Fatal("update failed")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "# my notes") || !strings.Contains(string(got), "recursive: true") {
		t.Fatalf("%s", got)
	}
	mw.statusLabel.SetText("")
	mw.checkProfileFile()
	if mw.statusLabel.Text != "" || !core.Profile().Recursive {
		t.Fatalf("own write reloaded: %q", mw.statusLabel.Text)
	}
}

func TestVariablesEditorRoundTrip(t *testing.T) {
	mw, core, _ := profileWindow(t)
	_, collect, names, _ := mw.buildVariablesTab()
	got := collect()
	want := core.Profile().Variables
	if len(got) != 1 || got[0].Name != "year" || len(got[0].Sources) != len(want[0].Sources) ||
		*got[0].Validate.Min != 1970 || *got[0].Validate.Max != 2100 {
		t.Fatalf("%+v", got)
	}
	if n := names(); len(n) != 1 || n[0] != "year" {
		t.Fatalf("%v", n)
	}
	// the editor works on a copy: collected edits do not touch the profile in use
	got[0].Name = "changed"
	got[0].Sources[0].Kind = vars.KindConstant
	if core.Profile().Variables[0].Name != "year" || core.Profile().Variables[0].Sources[0].Kind != vars.KindManual {
		t.Fatal("editor leaked into the active profile")
	}
	if _, err := vars.New(collect()); err != nil {
		t.Fatal(err)
	}
}

func TestParseOptInt(t *testing.T) {
	if v, err := parseOptInt("  "); v != nil || err != nil {
		t.Fatal(v, err)
	}
	if v, err := parseOptInt(" 42 "); err != nil || *v != 42 {
		t.Fatal(v, err)
	}
	if _, err := parseOptInt("4x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestConflictPolicyPreselect(t *testing.T) {
	for in, want := range map[string]string{
		"": policySkipLabel, "skip": policySkipLabel, "bogus": policySkipLabel,
		"rename": policyRenameLabel, "replace": policyReplaceLabel,
	} {
		if got := policyLabelFor(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestSetInputDirectoryStoresItInTheProfile(t *testing.T) {
	mw, core, dir := profileWindow(t)
	in := t.TempDir()
	mw.setInputDirectory(in)
	if got := core.InputDir(); got != in {
		t.Fatalf("active profile input dir %q, want %q", got, in)
	}
	saved, err := profile.Load(profile.PathFor(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.InputDirectory != in {
		t.Fatalf("saved profile input dir %q, want %q", saved.InputDirectory, in)
	}
}

func TestSwitchProfileReappliesShortcuts(t *testing.T) {
	mw, _, _ := profileWindow(t)
	mw.cfg.Shortcuts["next"] = "9" // clashes with a tag key of profile "a"
	mw.applyShortcuts()
	if mw.plainKeys["9"] != "" {
		t.Fatal("the clashing binding should have been rejected under profile a")
	}
	if !mw.switchProfile("b") { // profile b has no tag on key 9
		t.Fatal("switch failed")
	}
	if mw.plainKeys["9"] != "next" {
		t.Fatalf("shortcuts were not re-resolved after the switch: %v", mw.plainKeys)
	}
}

// entryByHint finds the entry with the given placeholder anywhere below o.
func entryByHint(o fyne.CanvasObject, hint string) *widget.Entry {
	if e, ok := o.(*widget.Entry); ok && e.PlaceHolder == hint {
		return e
	}
	var kids []fyne.CanvasObject
	switch c := o.(type) {
	case *fyne.Container:
		kids = c.Objects
	case *container.Scroll:
		kids = []fyne.CanvasObject{c.Content}
	case fyne.Widget:
		kids = test.WidgetRenderer(c).Objects()
	}
	for _, k := range kids {
		if e := entryByHint(k, hint); e != nil {
			return e
		}
	}
	return nil
}

// An unparsable min/max used to be stored as "no limit" on Save.
func TestVariablesTabReportsUnparsableNumbers(t *testing.T) {
	mw, _, _ := profileWindow(t)
	tab, collect, _, pending := mw.buildVariablesTab()
	if err := pending(); err != nil {
		t.Fatalf("clean tab reports %v", err)
	}
	minE := entryByHint(tab, "min (numeric)")
	if minE == nil {
		t.Fatal("min entry not found")
	}
	minE.SetText("19x0")
	if err := pending(); err == nil || !strings.Contains(err.Error(), "19x0") {
		t.Fatalf("pending = %v, want the unparsable input reported", err)
	}
	if got := collect()[0].Validate.Min; got != nil {
		t.Errorf("unparsable input must not yield a value, got %d", *got)
	}
	minE.SetText("1970")
	if err := pending(); err != nil {
		t.Errorf("fixed input still reported: %v", err)
	}
	grp := entryByHint(tab, "group")
	if grp == nil {
		t.Skip("example profile has no regex source")
	}
	grp.SetText("two")
	if err := pending(); err == nil {
		t.Error("non-numeric capture group must be reported")
	}
}
