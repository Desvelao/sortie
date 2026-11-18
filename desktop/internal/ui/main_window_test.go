package ui

import (
	"fmt"
	"image"
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
	"sortie/internal/preview"
	"sortie/internal/profile"
	"sortie/internal/tags"
	"sortie/internal/vars"
)

// newCore returns an application with the starter profile (nine tags on keys
// 1-9, a "year" variable, {year}/{tag}/{filename}) and the default config.
func newCore(t *testing.T, root string) (*app.Application, config.Config) {
	t.Helper()
	core := app.New()
	p := profile.Example("test")
	p.Recursive, p.DestinationRoot = false, root
	if err := core.ApplyProfile("test", p, ""); err != nil {
		t.Fatal(err)
	}
	return core, config.Default()
}

func typeKey(w fyne.Window, name fyne.KeyName) {
	w.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: name})
}

// The whole classification workflow must work from the keyboard alone.
func TestKeyboardClassificationWorkflow(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	core, cfg := newCore(t, "")
	mw := NewMainWindow(a, core, cfg, filepath.Join(t.TempDir(), "config.json"))
	files := []fileinfo.FileInfo{
		{Path: "/x/a_2020.txt", Name: "a_2020.txt", ModTime: time.Unix(1, 0)},
		{Path: "/x/b.bin", Name: "b.bin", ModTime: time.Unix(1, 0)},
	}
	if _, err := core.ApplyScan("/x", files, false); err != nil {
		t.Fatal(err)
	}
	mw.update()
	w := mw.Window()

	typeKey(w, "1") // Family
	typeKey(w, "5") // Music
	typeKey(w, "5") // toggled off again
	typeKey(w, "8") // Important
	f, _ := core.Current()
	if got := core.TagsOf(f); len(got) != 2 || got[0] != 1 || got[1] != 8 {
		t.Fatalf("tags: %v", got)
	}
	typeKey(w, fyne.KeyRight)
	typeKey(w, "2")
	f, _ = core.Current()
	if f.Name != "b.bin" || len(core.TagsOf(f)) != 1 {
		t.Fatalf("%s %v", f.Name, core.TagsOf(f))
	}
	typeKey(w, fyne.KeyHome)
	if f, _ = core.Current(); f.Name != "a_2020.txt" || core.TaggedCount() != 2 {
		t.Fatal(f.Name, core.TaggedCount())
	}
	typeKey(w, "Q") // unbound key: ignored
}

// A file whose preview fails (or has no handler) must stay fully classifiable.
func TestBrokenPreviewDoesNotBlockClassification(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	dir := t.TempDir()
	mk := func(name, content string) fileinfo.FileInfo {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(p)
		return fileinfo.FileInfo{Path: p, Name: name, Ext: filepath.Ext(name), MIMEType: "image/png", Size: st.Size(), ModTime: st.ModTime()}
	}
	files := []fileinfo.FileInfo{mk("a_broken.png", "not a png"), mk("b.xyz", "\x00\x01"), mk("c.txt", "hello")}
	files[1].MIMEType, files[2].MIMEType = "application/octet-stream", "text/plain"

	core, cfg := newCore(t, "")
	mw := NewMainWindow(a, core, cfg, filepath.Join(dir, "cfg.json"))
	if _, err := core.ApplyScan(dir, files, false); err != nil {
		t.Fatal(err)
	}
	mw.update()
	w := mw.Window()
	for i := 0; i < 3; i++ {
		typeKey(w, "1")
		typeKey(w, fyne.KeyRight)
	}
	if core.TaggedCount() != 3 {
		t.Fatalf("tagged %d of 3", core.TaggedCount())
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, f := range files {
		key := preview.Key(f.Path, f.Size, f.ModTime)
		var p preview.Preview
		ok := false
		for !ok && time.Now().Before(deadline) {
			p, ok = mw.pcache.Get(key)
			time.Sleep(20 * time.Millisecond)
		}
		_ = p
	}
	if p, ok := mw.pcache.Get(preview.Key(files[0].Path, files[0].Size, files[0].ModTime)); ok && p.Kind != preview.KindGeneric {
		t.Fatalf("broken png produced %s preview", p.Kind)
	}
}

// Tagging shows the resolved destination and its status in the tag panel.
func TestDestinationStatusShownAfterTagging(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	dir, root := t.TempDir(), t.TempDir()
	src := filepath.Join(dir, "Concert_2019.mp3")
	os.WriteFile(src, []byte("AAAA"), 0o644)
	st, _ := os.Stat(src)
	f := fileinfo.FileInfo{Path: src, Name: "Concert_2019.mp3", Size: st.Size(), ModTime: st.ModTime()}

	core, cfg := newCore(t, root)
	mw := NewMainWindow(a, core, cfg, filepath.Join(dir, "cfg.json"))
	if _, err := core.ApplyScan(dir, []fileinfo.FileInfo{f}, false); err != nil {
		t.Fatal(err)
	}
	mw.update()
	typeKey(mw.Window(), "5") // Music

	want := filepath.Join(root, "2019", "Music", "Concert_2019.mp3")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if panelHas(mw.tagPanel, "READY") && panelHas(mw.tagPanel, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("destination status not displayed")
}

// "Copy current" is disabled while every destination of the current file is
// invalid (here: the destination root does not exist) and comes back once the
// problem is fixed.
func TestCopyCurrentDisabledWhileDestinationInvalid(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	dir := t.TempDir()
	root := filepath.Join(t.TempDir(), "missing")
	src := filepath.Join(dir, "Concert_2019.mp3")
	os.WriteFile(src, []byte("AAAA"), 0o644)
	st, _ := os.Stat(src)
	f := fileinfo.FileInfo{Path: src, Name: "Concert_2019.mp3", Size: st.Size(), ModTime: st.ModTime()}

	core, cfg := newCore(t, root)
	mw := NewMainWindow(a, core, cfg, filepath.Join(dir, "cfg.json"))
	if _, err := core.ApplyScan(dir, []fileinfo.FileInfo{f}, false); err != nil {
		t.Fatal(err)
	}
	mw.update()
	if mw.copyOne.Disabled() {
		t.Fatal("nothing is tagged yet: the button must not be disabled")
	}
	typeKey(mw.Window(), "5") // Music
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(what)
	}
	waitFor("Copy current not disabled for a missing root", func() bool { return mw.copyOne.Disabled() && panelHas(mw.tagPanel, "not available") })

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mw.update()
	waitFor("Copy current not enabled again once the root exists", func() bool { return !mw.copyOne.Disabled() && panelHas(mw.tagPanel, "READY") })
}

func panelHas(p *TagPanel, text string) bool {
	for _, o := range p.checks.Objects {
		if l, ok := o.(*widget.Label); ok && strings.Contains(l.Text, text) {
			return true
		}
	}
	return false
}

// The copy dialog opens, analyses, and closes with Escape without side effects.
func TestCopyDialogOpensAndEscapeCloses(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	dir, root := t.TempDir(), t.TempDir()
	src := filepath.Join(dir, "Concert_2019.mp3")
	os.WriteFile(src, []byte("AAAA"), 0o644)
	st, _ := os.Stat(src)
	f := fileinfo.FileInfo{Path: src, Name: "Concert_2019.mp3", Size: st.Size(), ModTime: st.ModTime()}

	core, cfg := newCore(t, root)
	mw := NewMainWindow(a, core, cfg, filepath.Join(dir, "cfg.json"))
	core.ApplyScan(dir, []fileinfo.FileInfo{f}, false)
	mw.update()
	typeKey(mw.Window(), "5")

	mw.openCopyDialog(true)
	if !mw.dialogOpen {
		t.Fatal("dialog did not open")
	}
	time.Sleep(300 * time.Millisecond) // let the analysis finish
	typeKey(mw.Window(), fyne.KeyEscape)
	if mw.dialogOpen {
		t.Fatal("Escape did not close the dialog")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("analysis wrote to the destination: %v", entries)
	}
}

func newWindowWithFiles(t *testing.T, names ...string) (*MainWindow, *app.Application) {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	core, cfg := newCore(t, "")
	mw := NewMainWindow(a, core, cfg, filepath.Join(t.TempDir(), "cfg.json"))
	var files []fileinfo.FileInfo
	for _, n := range names {
		files = append(files, fileinfo.FileInfo{Path: "/x/" + n, Name: n, Ext: filepath.Ext(n), ModTime: time.Unix(1, 0)})
	}
	if _, err := core.ApplyScan("/x", files, false); err != nil {
		t.Fatal(err)
	}
	mw.update()
	return mw, core
}

func TestFilterSelectBrowsesOnlyMatchingFiles(t *testing.T) {
	mw, core := newWindowWithFiles(t, "a_2020.txt", "b_2020.txt", "c_2020.txt")
	typeKey(mw.Window(), "1") // a tagged
	mw.filter.SetSelected(app.FilterUntagged.String())
	if core.Count() != 2 || !strings.Contains(mw.countLabel.Text, "2 of 3") {
		t.Fatalf("%d %q", core.Count(), mw.countLabel.Text)
	}
	if f, _ := core.Current(); f.Name != "b_2020.txt" {
		t.Fatal(f.Name)
	}
	typeKey(mw.Window(), fyne.KeyRight)
	if f, _ := core.Current(); f.Name != "c_2020.txt" {
		t.Fatal(f.Name)
	}
	mw.filter.SetSelected(app.FilterTagged.String())
	if f, _ := core.Current(); f.Name != "a_2020.txt" || core.Count() != 1 {
		t.Fatal(f.Name, core.Count())
	}
	mw.filter.SetSelected(app.FilterAll.String())
	if core.Count() != 3 {
		t.Fatal(core.Count())
	}
}

func TestShortcutsAreConfigurableAtRuntime(t *testing.T) {
	mw, core := newWindowWithFiles(t, "a", "b", "c")
	mw.cfg.Shortcuts["next"] = "N"
	mw.cfg.Shortcuts["prev"] = "P"
	mw.applyShortcuts()
	typeKey(mw.Window(), "N")
	if f, _ := core.Current(); f.Name != "b" {
		t.Fatalf("custom key did not navigate: %s", f.Name)
	}
	typeKey(mw.Window(), fyne.KeyRight) // old default no longer bound
	if f, _ := core.Current(); f.Name != "b" {
		t.Fatalf("old key still bound: %s", f.Name)
	}
	typeKey(mw.Window(), "P")
	if f, _ := core.Current(); f.Name != "a" {
		t.Fatal(f.Name)
	}
	// invalid configuration falls back to defaults instead of breaking the keys
	mw.cfg.Shortcuts["next"] = "Shift+X"
	mw.applyShortcuts()
	typeKey(mw.Window(), fyne.KeyRight)
	if f, _ := core.Current(); f.Name != "b" {
		t.Fatalf("defaults not restored: %s", f.Name)
	}
}

func TestFilmstripShowsNeighboursAndJumps(t *testing.T) {
	mw, core := newWindowWithFiles(t, "a", "b", "c", "d", "e", "f")
	typeKey(mw.Window(), fyne.KeyRight)
	typeKey(mw.Window(), fyne.KeyRight) // current: c
	if n := len(mw.filmstrip.box.Objects); n != 6 {
		t.Fatalf("tiles: %d", n)
	}
	// the last tile is f; tapping it jumps there
	last := mw.filmstrip.box.Objects[5]
	var tl *tile
	switch o := last.(type) {
	case *tile:
		tl = o
	case *fyne.Container:
		tl = o.Objects[len(o.Objects)-1].(*tile)
	}
	test.Tap(tl)
	if f, _ := core.Current(); f.Name != "f" {
		t.Fatalf("jumped to %s", f.Name)
	}
	mw.cfg.ShowFilmstrip = false
	mw.update()
	if mw.filmstrip.Object().Visible() {
		t.Fatal("filmstrip should be hidden")
	}
}

// findObj returns the first object below o that satisfies pred.
func findObj(o fyne.CanvasObject, pred func(fyne.CanvasObject) bool) fyne.CanvasObject {
	if pred(o) {
		return o
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
		if r := findObj(k, pred); r != nil {
			return r
		}
	}
	return nil
}

// Escape while the "Replace existing files?" confirmation is showing must not
// close the copy dialog underneath it (the copy would then run unseen).
func TestEscapeIgnoredWhileReplaceConfirmShowing(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	dir, root := t.TempDir(), t.TempDir()
	src := filepath.Join(dir, "Concert_2019.mp3")
	os.WriteFile(src, []byte("AAAA"), 0o644)
	st, _ := os.Stat(src)
	f := fileinfo.FileInfo{Path: src, Name: "Concert_2019.mp3", Size: st.Size(), ModTime: st.ModTime()}
	core, cfg := newCore(t, root)
	mw := NewMainWindow(a, core, cfg, filepath.Join(dir, "cfg.json"))
	core.ApplyScan(dir, []fileinfo.FileInfo{f}, false)
	mw.update()
	typeKey(mw.Window(), "5")
	mw.openCopyDialog(true)
	time.Sleep(300 * time.Millisecond) // let the analysis finish

	var sel *widget.RadioGroup
	var copyBtn *widget.Button
	for _, ov := range mw.Window().Canvas().Overlays().List() {
		if o := findObj(ov, func(o fyne.CanvasObject) bool {
			s, ok := o.(*widget.RadioGroup)
			return ok && strings.Contains(strings.Join(s.Options, "|"), "Replace")
		}); o != nil {
			sel = o.(*widget.RadioGroup)
		}
		if o := findObj(ov, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == "Copy" }); o != nil {
			copyBtn = o.(*widget.Button)
		}
	}
	if sel == nil || copyBtn == nil {
		t.Fatalf("copy dialog widgets not found (select=%v button=%v)", sel != nil, copyBtn != nil)
	}
	for _, opt := range sel.Options {
		if strings.Contains(opt, "Replace") {
			sel.SetSelected(opt)
		}
	}
	test.Tap(copyBtn)
	typeKey(mw.Window(), fyne.KeyEscape)
	if !mw.dialogOpen {
		t.Fatal("Escape closed the copy dialog although the replace confirmation was showing")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("copy ran without confirmation: %v", entries)
	}
}

// A failing config write must be shown, not only logged.
func TestSaveConfigFailureIsShown(t *testing.T) {
	mw, _ := newWindowWithFiles(t, "a.txt")
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	mw.cfgPath = filepath.Join(blocker, "sub", "config.json") // parent is a regular file
	before := len(mw.Window().Canvas().Overlays().List())
	mw.saveConfig()
	if after := len(mw.Window().Canvas().Overlays().List()); after != before+1 {
		t.Fatalf("overlays %d → %d: no error dialog shown", before, after)
	}
	mw.cfgPath = filepath.Join(t.TempDir(), "config.json")
	n := len(mw.Window().Canvas().Overlays().List())
	mw.saveConfig()
	if len(mw.Window().Canvas().Overlays().List()) != n {
		t.Error("a successful save must not show a dialog")
	}
}

func TestPreviewReloadsWhenFileChangesUnderSamePath(t *testing.T) {
	mw, _ := newWindowWithFiles(t, "a.txt")
	f := fileinfo.FileInfo{Path: "/x/a.txt", Name: "a.txt", Ext: ".txt", Size: 5, ModTime: time.Unix(10, 0)}
	mw.refreshPreview(f, true)
	gen := mw.previewGen.Load()
	mw.refreshPreview(f, true) // e.g. a tag was toggled: nothing to reload
	if mw.previewGen.Load() != gen {
		t.Fatal("unchanged file must not reload the preview (audio would restart)")
	}
	g := f
	g.Size, g.ModTime = 6, time.Unix(11, 0) // edited in place, then rescanned
	mw.refreshPreview(g, true)
	if mw.previewGen.Load() == gen {
		t.Fatal("changed file kept its stale preview")
	}
}

func TestFilmstripRetiresThumbnailLoadsOfRebuiltTiles(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	fs := NewFilmstrip(func(string) {})
	var shown []func() bool
	load := func(_ fileinfo.FileInfo, _ func(image.Image), still func() bool) { shown = append(shown, still) }
	f1 := fileinfo.FileInfo{Path: "/x/1.jpg", Name: "1.jpg", Ext: ".jpg"}
	f2 := fileinfo.FileInfo{Path: "/x/2.jpg", Name: "2.jpg", Ext: ".jpg"}

	fs.Set([]fileinfo.FileInfo{f1}, 0, nil, load)
	fs.Set([]fileinfo.FileInfo{f1}, 0, nil, load) // identical: no rebuild, no new loads
	if len(shown) != 1 || !shown[0]() {
		t.Fatalf("after an identical Set: %d loads, first still shown = %v", len(shown), len(shown) > 0 && shown[0]())
	}
	fs.Set([]fileinfo.FileInfo{f2}, 0, nil, load) // skimmed on: tile 1 is gone
	if len(shown) != 2 || shown[0]() || !shown[1]() {
		t.Errorf("stale load must be retired and the new one live: old=%v new=%v", shown[0](), shown[1]())
	}
}

func TestFilmstripCentresCurrentTile(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	fs := NewFilmstrip(func(string) {})
	fs.scroll.Resize(fyne.NewSize(400, 120))
	var files []fileinfo.FileInfo
	for i := 0; i < 9; i++ {
		files = append(files, fileinfo.FileInfo{Path: fmt.Sprintf("/x/%d.txt", i), Name: fmt.Sprintf("%d.txt", i), Ext: ".txt"})
	}
	fs.Set(files, 4, nil, nil)
	first := fs.scroll.Offset.X
	if first <= 0 {
		t.Fatalf("current tile (middle of 9, ~1000px strip, 400px view) not scrolled into view: offset %v", first)
	}
	fs.Set(files, 0, nil, nil)
	if fs.scroll.Offset.X != 0 {
		t.Errorf("first tile: offset %v, want 0", fs.scroll.Offset.X)
	}
}

func TestFreeVarNameAndEmptyDestinationLabel(t *testing.T) {
	defs := []vars.Definition{{Name: "var1"}, {Name: "var3"}}
	if got := freeVarName(defs); got != "var2" {
		t.Errorf("freeVarName = %q, want var2", got)
	}
	if got := freeVarName(append(defs, vars.Definition{Name: "var2"})); got != "var4" {
		t.Errorf("freeVarName = %q, want var4", got)
	}

	a := test.NewApp()
	defer a.Quit()
	p := NewTagPanel(func(int) {})
	p.Refresh([]tags.Tag{{ID: 1, Name: "Keep", Shortcut: "K", Enabled: true}, {ID: 2, Name: "Work", Shortcut: "W", Destination: "Work/Docs", Enabled: true}},
		func(int) bool { return false }, true)
	var texts []string
	for _, o := range p.box.Objects {
		if b, ok := findObj(o, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Button); return ok }).(*widget.Button); ok {
			texts = append(texts, b.Text)
		}
	}
	if len(texts) != 2 || strings.Contains(texts[0], "→") || !strings.HasSuffix(texts[1], "→ Work/Docs") {
		t.Errorf("labels: %q", texts)
	}
}
