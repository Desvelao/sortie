package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sortie/internal/copier"
	"sortie/internal/database"
	"sortie/internal/destination"
	"sortie/internal/fileinfo"
	"sortie/internal/profile"
	"sortie/internal/tags"
	"sortie/internal/vars"
)

func files(names ...string) []fileinfo.FileInfo {
	var out []fileinfo.FileInfo
	for _, n := range names {
		out = append(out, fileinfo.FileInfo{Path: "/x/" + n, Name: n, ModTime: time.Unix(1700000000, 0)})
	}
	return out
}

func load(t *testing.T, a *Application, names ...string) {
	t.Helper()
	if _, err := a.ApplyScan("/x", files(names...), false); err != nil {
		t.Fatal(err)
	}
}

// withTags applies the starter profile: nine tags on keys 1-9 and a "year"
// variable (manual > filename > metadata) with the {year}/{tag}/{filename}
// layout.
func withTags(t *testing.T, a *Application) {
	t.Helper()
	p := profile.Example("test")
	p.Recursive = false
	if err := a.ApplyProfile("test", p, ""); err != nil {
		t.Fatal(err)
	}
}

// yearOf returns the resolved "year" variable of f.
func yearOf(a *Application, f fileinfo.FileInfo) VarInfo {
	for _, v := range a.VarsOf(f) {
		if v.Name == "year" {
			return v
		}
	}
	return VarInfo{}
}

func TestEmpty(t *testing.T) {
	a := New()
	a.Next()
	a.Last()
	if _, ok := a.Current(); ok {
		t.Fatal("unexpected current")
	}
	if p, n := a.Position(); p != 0 || n != 0 {
		t.Fatal(p, n)
	}
	if h, err := a.ToggleByShortcut("1"); h || err != nil {
		t.Fatal(h, err)
	}
}

func TestNavigationClamps(t *testing.T) {
	a := New()
	load(t, a, "a", "b", "c")
	a.Prev()
	if p, _ := a.Position(); p != 1 {
		t.Fatal(p)
	}
	a.Next()
	a.Next()
	a.Next()
	if p, n := a.Position(); p != 3 || n != 3 {
		t.Fatal(p, n)
	}
	a.First()
	if f, _ := a.Current(); f.Name != "a" {
		t.Fatal(f.Name)
	}
	a.Last()
	if f, _ := a.Current(); f.Name != "c" {
		t.Fatal(f.Name)
	}
}

func TestRescanKeepsSelection(t *testing.T) {
	a := New()
	load(t, a, "a", "b", "c")
	a.Next()
	load(t, a, "a", "b", "c", "d")
	if f, _ := a.Current(); f.Name != "b" {
		t.Fatal(f.Name)
	}
	load(t, a, "x")
	if f, _ := a.Current(); f.Name != "x" {
		t.Fatal(f.Name)
	}
}

func TestMultipleTagsAndToggle(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "Concert_2019.mp3", "other.txt")
	f, _ := a.Current()
	for _, k := range []string{"5", "3", "8"} { // Music, Events, Important
		if h, err := a.ToggleByShortcut(k); !h || err != nil {
			t.Fatal(k, h, err)
		}
	}
	if got := a.TagsOf(f); len(got) != 3 || got[0] != 3 || got[1] != 5 || got[2] != 8 {
		t.Fatalf("%v", got)
	}
	a.ToggleByShortcut("3") // off; others stay
	if got := a.TagsOf(f); len(got) != 2 || a.HasTag(f, 3) {
		t.Fatalf("%v", got)
	}
	if a.TaggedCount() != 1 {
		t.Fatal(a.TaggedCount())
	}
	if h, _ := a.ToggleByShortcut("Q"); h {
		t.Fatal("unbound key handled")
	}
}

func TestFilesByTags(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "a", "b", "c")
	names := func(fs []fileinfo.FileInfo) string {
		var s string
		for _, f := range fs {
			s += f.Name
		}
		return s
	}
	// a: Music(5); b: Music(5)+Events(3); c: untagged
	a.First()
	a.ToggleByShortcut("5")
	a.Next()
	a.ToggleByShortcut("5")
	a.ToggleByShortcut("3")
	if got := names(a.FilesByTags([]int{5}, false)); got != "ab" {
		t.Fatal(got)
	}
	if got := names(a.FilesByTags([]int{3}, false)); got != "b" {
		t.Fatal(got)
	}
	if got := names(a.FilesByTags([]int{3, 5}, false)); got != "ab" {
		t.Fatal("any:", got)
	}
	if got := names(a.FilesByTags([]int{3, 5}, true)); got != "b" {
		t.Fatal("all:", got)
	}
	if got := a.FilesByTags(nil, false); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestDisabledTagCannotToggle(t *testing.T) {
	a := New()
	ts := tags.Defaults()
	ts[0].Enabled = false
	a.SetTags(ts)
	load(t, a, "a")
	if h, _ := a.ToggleByShortcut("1"); h {
		t.Fatal("disabled tag handled")
	}
	if _, err := a.ToggleTag(1); err == nil {
		t.Fatal("expected error")
	}
}

func TestRemovingTagDropsAssignment(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "a")
	a.ToggleByShortcut("1")
	f, _ := a.Current()
	if err := a.SetTags(tags.Defaults()[1:]); err != nil {
		t.Fatal(err)
	}
	if len(a.TagsOf(f)) != 0 || a.TaggedCount() != 0 {
		t.Fatal("assignment should be gone")
	}
	if err := a.SetTags([]tags.Tag{{ID: 1, Name: "x", Destination: "../up", Enabled: true}}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSurvivesRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db.sqlite")
	open := func() *Application {
		db, err := database.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		a := NewWithDB(db)
		withTags(t, a)
		return a
	}
	a := open()
	load(t, a, "a.txt", "b_2020.txt", "c.txt")
	a.Next()
	a.ToggleByShortcut("2")
	a.ToggleByShortcut("9")
	f, _ := a.Current()
	if err := a.SetManualVar(f, "year", "2001"); err != nil {
		t.Fatal(err)
	}
	a.Next() // session now points at c.txt
	a.db.Close()

	b := open() // "restart"
	load(t, b, "a.txt", "b_2020.txt", "c.txt")
	if f, _ := b.Current(); f.Name != "c.txt" {
		t.Fatalf("session not restored: %s", f.Name)
	}
	b.Prev()
	f, _ = b.Current()
	if got := b.TagsOf(f); len(got) != 2 || got[0] != 2 || got[1] != 9 {
		t.Fatalf("tags lost: %v", got)
	}
	if v := yearOf(b, f); v.Value.Value != "2001" || !v.Manual {
		t.Fatalf("manual year lost: %+v", v)
	}
}

func TestVariableHandling(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "Invoice_2020.pdf")
	f, _ := a.Current()
	if v := yearOf(a, f); v.Value.Value != "2020" || v.Manual {
		t.Fatal(v)
	}
	if err := a.SetManualVar(f, "year", "2015"); err != nil {
		t.Fatal(err)
	}
	f, _ = a.Current()
	if v := yearOf(a, f); v.Value.Value != "2015" || !v.Manual {
		t.Fatal(v)
	}
	if err := a.SetManualVar(f, "year", "1200"); err == nil {
		t.Fatal("expected range error")
	}
	if err := a.SetManualVar(f, "nope", "1"); err == nil {
		t.Fatal("expected unknown variable error")
	}
	a.ClearManualVar(f, "year")
	f, _ = a.Current()
	if v := yearOf(a, f); v.Value.Value != "2020" {
		t.Fatal(v)
	}
	// a blank value clears, too
	a.SetManualVar(f, "year", "2015")
	a.SetManualVar(f, "year", "  ")
	if v := yearOf(a, f); v.Value.Value != "2020" {
		t.Fatal(v)
	}
}

func TestProfileChangeLive(t *testing.T) {
	a := New()
	withTags(t, a)
	f := fileinfo.FileInfo{Path: "/x/a_1995.txt", Name: "a_1995.txt"}
	if v := yearOf(a, f); v.Value.OK() {
		t.Fatal(v) // 1995 is not a 20xx year
	}
	if err := a.UpdateProfile(func(p *profile.Profile) {
		p.Variables[0].Sources[1].Pattern = `(^|[^0-9])(19[0-9]{2})([^0-9]|$)`
	}); err != nil {
		t.Fatal(err)
	}
	if v := yearOf(a, f); v.Value.Value != "1995" {
		t.Fatal(v)
	}
	if err := a.UpdateProfile(func(p *profile.Profile) { p.Variables[0].Sources[1].Pattern = "([" }); err == nil {
		t.Fatal("expected error")
	}
	if v := yearOf(a, f); v.Value.Value != "1995" {
		t.Fatal("profile should be unchanged after a failed update")
	}
	// the caller's copy of the profile is independent of the active one
	got := a.Profile()
	got.Variables[0].Name = "changed"
	if a.Profile().Variables[0].Name != "year" {
		t.Fatal("Profile() leaked internal state")
	}
}

func TestVariablesFromPathAndMetadata(t *testing.T) {
	a := New()
	a.fieldsFn = func(path string) map[string]string {
		if strings.HasSuffix(path, "photo.jpg") {
			return map[string]string{"camera": "Canon EOS"}
		}
		return nil
	}
	p := profile.New("v")
	p.Tags = []profile.Tag{{Name: "A", Shortcut: "1"}}
	p.DestinationTemplate = "{client}/{camera|none}/{tag}/{filename}"
	p.Variables = []vars.Definition{
		{Name: "client", Sources: []vars.Source{{Kind: vars.KindPath, Pattern: `^clients/([^/]+)`}}, Fallback: "misc"},
		{Name: "camera", Sources: []vars.Source{{Kind: vars.KindMetadata, Field: "camera"}}},
	}
	if err := a.ApplyProfile("v", p, ""); err != nil {
		t.Fatal(err)
	}
	fs := []fileinfo.FileInfo{
		{Path: "/in/clients/acme/2020/photo.jpg", Name: "photo.jpg", Size: 1, ModTime: time.Unix(1, 0)},
		{Path: "/in/loose.txt", Name: "loose.txt", Size: 1, ModTime: time.Unix(1, 0)},
	}
	if _, err := a.ApplyScan("/in", fs, true); err != nil {
		t.Fatal(err)
	}
	f, _ := a.Current()
	got := map[string]string{}
	for _, v := range a.VarsOf(f) {
		got[v.Name] = v.Value.Value
	}
	if got["client"] != "acme" || got["camera"] != "Canon EOS" {
		t.Fatalf("%v", got)
	}
	a.Next()
	f, _ = a.Current()
	for _, v := range a.VarsOf(f) {
		got[v.Name] = v.Value.Value
		if v.Name == "camera" && v.OK() {
			t.Fatal("camera should be unresolved")
		}
	}
	if got["client"] != "misc" {
		t.Fatalf("%v", got)
	}
}

func TestDestinationChecks(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	write := func(p, c string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(name, content string) fileinfo.FileInfo {
		p := filepath.Join(in, name)
		write(p, content)
		st, _ := os.Stat(p)
		return fileinfo.FileInfo{Path: p, Name: name, Size: st.Size(), ModTime: st.ModTime()}
	}
	a := New()
	withTags(t, a)
	files := []fileinfo.FileInfo{mk("Concert_2019.mp3", "AAAA"), mk("unknown_file.pdf", "x")}
	if _, err := a.ApplyScan(in, files, false); err != nil {
		t.Fatal(err)
	}
	if err := a.SetDestination(root, "{year}/{tag}/{filename}"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	f, _ := a.Current() // Concert_2019.mp3
	if len(a.Checks(ctx, f)) != 0 {
		t.Fatal("untagged file has no destinations")
	}
	for _, k := range []string{"5", "3", "8"} { // Music, Events, Important
		a.ToggleByShortcut(k)
	}
	write(filepath.Join(root, "2019", "Events", "Concert_2019.mp3"), "AAAA")
	write(filepath.Join(root, "2019", "Important", "Concert_2019.mp3"), "BBBB")
	cs := a.Checks(ctx, f)
	if len(cs) != 3 {
		t.Fatalf("%+v", cs)
	}
	got := map[string]destination.Status{}
	for _, c := range cs {
		got[c.TagName] = c.Status
	}
	if got["Music"] != destination.StatusReady || got["Events"] != destination.StatusAlreadyCopied || got["Important"] != destination.StatusConflict {
		t.Fatalf("%v", got)
	}
	if cs[0].Path != filepath.Join(root, "2019", "Events", "Concert_2019.mp3") { // display order: Events before Music
		t.Fatalf("order/path: %+v", cs[0])
	}

	// a manual year changes the destination immediately
	a.SetManualVar(f, "year", "2001")
	f, _ = a.Current()
	for _, c := range a.Checks(ctx, f) {
		if c.Status != destination.StatusReady || !strings.Contains(c.Path, "2001") {
			t.Fatalf("%+v", c)
		}
	}

	// unresolved variable → UNRESOLVED_VAR, never copyable
	a.Next()
	a.ToggleByShortcut("1")
	f, _ = a.Current()
	cs = a.Checks(ctx, f)
	if len(cs) != 1 || cs[0].Status != destination.StatusUnresolvedVar || cs[0].CanCopy() {
		t.Fatalf("%+v", cs)
	}
	// an explicit default in the template makes it copyable
	a.SetDestination(root, "{year|Unknown}/{tag}/{filename}")
	if cs = a.Checks(ctx, f); cs[0].Status != destination.StatusReady || !strings.Contains(cs[0].Path, "Unknown") {
		t.Fatalf("%+v", cs)
	}
	// untouched sources
	if b, _ := os.ReadFile(files[0].Path); string(b) != "AAAA" {
		t.Fatal("source modified")
	}
}

func TestSetDestinationValidates(t *testing.T) {
	a := New()
	withTags(t, a)
	for _, bad := range [][2]string{
		{"relative", "{year}/{filename}"},
		{"/ok", "../{filename}"},
		{"/ok", "{year}/{tag}"},
		{"/ok", "{nope}/{filename}"},
	} {
		if a.SetDestination(bad[0], bad[1]) == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if a.Resolver().Root != "" {
		t.Fatal("failed update changed the destination")
	}
	if err := a.SetDestination("/ok", "{year}/{filename}"); err != nil {
		t.Fatal(err)
	}
	if a.Resolver().Root != "/ok" {
		t.Fatal("not applied")
	}
}

func TestProfilesKeepSeparateTagging(t *testing.T) {
	a := New()
	withTags(t, a) // profile "test"
	load(t, a, "a.txt")
	a.ToggleByShortcut("1")
	f, _ := a.Current()
	if len(a.TagsOf(f)) != 1 {
		t.Fatal("tag not set")
	}
	other := profile.New("Other")
	other.Tags = []profile.Tag{{Name: "Family", Shortcut: "1"}} // same name and key as in "test"
	if err := a.ApplyProfile("other", other, ""); err != nil {
		t.Fatal(err)
	}
	load(t, a, "a.txt")
	f, _ = a.Current()
	if len(a.TagsOf(f)) != 0 || a.TaggedCount() != 0 {
		t.Fatal("tagging leaked between profiles")
	}
	a.ToggleByShortcut("1")
	// back to the first profile: its tagging is still there
	p := profile.Example("test")
	p.Recursive = false
	if err := a.ApplyProfile("test", p, ""); err != nil {
		t.Fatal(err)
	}
	load(t, a, "a.txt")
	f, _ = a.Current()
	if got := a.TagsOf(f); len(got) != 1 || a.ProfileID() != "test" {
		t.Fatalf("%v %s", got, a.ProfileID())
	}
}

func TestCopyEndToEndKeepsClassification(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	mk := func(name, content string) fileinfo.FileInfo {
		p := filepath.Join(in, name)
		os.WriteFile(p, []byte(content), 0o644)
		st, _ := os.Stat(p)
		return fileinfo.FileInfo{Path: p, Name: name, Size: st.Size(), ModTime: st.ModTime()}
	}
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	a := NewWithDB(db)
	withTags(t, a)
	a.SetDestination(root, "{year}/{tag}/{filename}")
	files := []fileinfo.FileInfo{mk("Concert_2019.mp3", "AAAA"), mk("unknown.pdf", "PDF"), mk("untagged_2020.txt", "U")}
	if _, err := a.ApplyScan(in, files, false); err != nil {
		t.Fatal(err)
	}
	a.ToggleByShortcut("5") // Music
	a.ToggleByShortcut("3") // Events
	a.Next()
	a.ToggleByShortcut("6") // Documents, but no year

	jobs := a.CopyJobs(true)
	if len(jobs) != 3 {
		t.Fatalf("jobs: %+v", jobs)
	}
	if cur := a.CopyJobs(false); len(cur) != 1 || cur[0].Name != "unknown.pdf" {
		t.Fatalf("current-file jobs: %+v", cur)
	}
	sum, err := a.RunCopy(context.Background(), jobs, copier.Options{})
	if err != nil || sum.Copied != 2 || sum.Invalid != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	for _, tg := range []string{"Music", "Events"} {
		if b, _ := os.ReadFile(filepath.Join(root, "2019", tg, "Concert_2019.mp3")); string(b) != "AAAA" {
			t.Errorf("%s not copied", tg)
		}
	}
	// classification untouched; history persisted
	f, _ := a.Current()
	if len(a.TagsOf(f)) != 1 || a.TaggedCount() != 2 {
		t.Fatal("tags changed by copying")
	}
	a.First()
	f, _ = a.Current()
	h, _ := a.History(f)
	if len(h) != 2 || h[0].Status != "COPIED" || h[0].DestHash == "" {
		t.Fatalf("%+v", h)
	}
	// a second run is a no-op
	if sum, _ = a.RunCopy(context.Background(), jobs, copier.Options{}); sum.AlreadyCopied != 2 || sum.Copied != 0 {
		t.Fatalf("%+v", sum)
	}
	// a crash leaves IN_PROGRESS rows; the next start reports them
	hid, _ := db.BeginCopy(f.ID, 5, "Music", f.Path, "/x/y")
	_ = hid
	db.Close()
	db2, _ := database.Open(dbPath)
	defer db2.Close()
	if n := NewWithDB(db2).InterruptedAtStart(); n != 1 {
		t.Fatalf("interrupted: %d", n)
	}
}

func TestConcurrentUpdateProfileKeepsBothChanges(t *testing.T) {
	a := New()
	withTags(t, a)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		a.UpdateProfile(func(p *profile.Profile) {
			time.Sleep(20 * time.Millisecond) // widen the read-modify-write window
			p.Recursive = true
		})
	}()
	go func() {
		defer wg.Done()
		a.UpdateProfile(func(p *profile.Profile) {
			time.Sleep(20 * time.Millisecond)
			p.DestinationTemplate = "{tag}/{filename}"
		})
	}()
	wg.Wait()
	got := a.Profile()
	if !got.Recursive || got.DestinationTemplate != "{tag}/{filename}" {
		t.Fatalf("an update was lost: recursive=%v template=%q", got.Recursive, got.DestinationTemplate)
	}
}

func TestParentTemplateVariable(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	dir := filepath.Join(in, "Holiday")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "a_2020.txt")
	os.WriteFile(p, []byte("x"), 0o644)
	st, _ := os.Stat(p)
	a := New()
	withTags(t, a)
	if _, err := a.ApplyScan(in, []fileinfo.FileInfo{{Path: p, Name: "a_2020.txt", Size: st.Size(), ModTime: st.ModTime()}}, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetDestination(root, "{parent}/{tag}/{filename}"); err != nil {
		t.Fatal(err)
	}
	a.ToggleByShortcut("1")
	f, _ := a.Current()
	checks := a.Checks(context.Background(), f)
	if len(checks) != 1 || checks[0].Status != destination.StatusReady {
		t.Fatalf("%+v", checks)
	}
	if want := filepath.Join(root, "Holiday"); !strings.HasPrefix(checks[0].Path, want+string(filepath.Separator)) {
		t.Fatalf("destination %q does not start with %q", checks[0].Path, want)
	}
}

func TestFieldsCachePrunedOnScanAndReadsAreShared(t *testing.T) {
	a := New()
	var calls atomic.Int32
	gate := make(chan struct{})
	a.fieldsFn = func(path string) map[string]string {
		calls.Add(1)
		<-gate
		return map[string]string{"k": path}
	}
	f1 := fileinfo.FileInfo{Path: "/in/a.txt", Name: "a.txt", Size: 1, ModTime: time.Unix(1, 0)}
	f2 := fileinfo.FileInfo{Path: "/old/b.txt", Name: "b.txt", Size: 1, ModTime: time.Unix(1, 0)}

	// several concurrent misses for one file version read it once
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.fieldsFor(f1) }()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("fieldsFn called %d times for concurrent misses, want 1", n)
	}
	a.fieldsFor(f2)
	if len(a.fcache) != 2 {
		t.Fatalf("cache has %d entries, want 2", len(a.fcache))
	}

	// a scan of another directory evicts entries of files that are gone
	a.pruneFieldsCache([]fileinfo.FileInfo{f1})
	if _, ok := a.fcache[f2.Path]; ok || len(a.fcache) != 1 {
		t.Errorf("stale entry not pruned: %v", a.fcache)
	}
}
