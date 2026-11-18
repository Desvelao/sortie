package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sortie/internal/copier"
	"sortie/internal/destination"
	"sortie/internal/fileinfo"
	"sortie/internal/tags"
)

func names(a *Application) []string {
	var out []string
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, i := range a.view {
		out = append(out, a.files[i].Name)
	}
	return out
}

func eq(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestFilterTaggedUntaggedAndStickyCurrent(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "a_2020.txt", "b_2020.txt", "c_2020.txt", "d_2020.txt")
	a.Next()
	a.ToggleByShortcut("1") // b tagged
	a.Last()
	a.ToggleByShortcut("1") // d tagged

	a.SetFilter(FilterTagged)
	if !eq(names(a), "b_2020.txt", "d_2020.txt") {
		t.Fatalf("%v", names(a))
	}
	if p, n := a.Position(); p != 2 || n != 2 { // current (d) is in the view
		t.Fatal(p, n)
	}
	a.SetFilter(FilterUntagged)
	if !eq(names(a), "a_2020.txt", "c_2020.txt") {
		t.Fatalf("%v", names(a))
	}
	if f, _ := a.Current(); f.Name != "c_2020.txt" { // nearest untagged to d
		t.Fatalf("current %s", f.Name)
	}
	if a.Total() != 4 || a.Count() != 2 {
		t.Fatal(a.Total(), a.Count())
	}

	// tagging the current file keeps it visible until we move away
	a.ToggleByShortcut("2")
	if f, ok := a.Current(); !ok || f.Name != "c_2020.txt" {
		t.Fatal("current vanished")
	}
	if !eq(names(a), "a_2020.txt", "c_2020.txt") {
		t.Fatalf("pinned view: %v", names(a))
	}
	a.Prev()
	if f, _ := a.Current(); f.Name != "a_2020.txt" {
		t.Fatal(f.Name)
	}
	if !eq(names(a), "a_2020.txt") {
		t.Fatalf("after moving away, c must be gone: %v", names(a))
	}
	a.Next() // nothing after a that is untagged
	if f, _ := a.Current(); f.Name != "a_2020.txt" {
		t.Fatal(f.Name)
	}
	// toggling a tag off elsewhere restores it: navigate and verify TaggedCount
	if a.TaggedCount() != 3 {
		t.Fatal(a.TaggedCount())
	}
}

func TestFilterUnresolvedAndEmptyView(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "a_2020.txt", "b.txt", "c.txt")
	a.SetFilter(FilterUnresolved)
	if !eq(names(a), "b.txt", "c.txt") {
		t.Fatalf("%v", names(a))
	}
	f, _ := a.Current()
	a.SetManualVar(f, "year", "2001") // resolves b: stays pinned, disappears after moving
	a.Next()
	if !eq(names(a), "c.txt") {
		t.Fatalf("%v", names(a))
	}
	f, _ = a.Current()
	a.SetManualVar(f, "year", "2002")
	a.SetFilter(FilterUnresolved)
	if a.Count() != 0 {
		t.Fatalf("%v", names(a))
	}
	if _, ok := a.Current(); ok {
		t.Fatal("empty view must have no current file")
	}
	if h, _ := a.ToggleByShortcut("1"); h {
		t.Fatal("tagging with an empty view")
	}
	a.Next() // must not panic
	a.SetFilter(FilterAll)
	if a.Count() != 3 {
		t.Fatal(a.Count())
	}
	if _, ok := a.Current(); !ok {
		t.Fatal("current should be back")
	}
}

func TestStatusFilters(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	mk := func(name, content string) fileinfo.FileInfo {
		p := filepath.Join(in, name)
		os.WriteFile(p, []byte(content), 0o644)
		st, _ := os.Stat(p)
		return fileinfo.FileInfo{Path: p, Name: name, Size: st.Size(), ModTime: st.ModTime()}
	}
	a := New()
	withTags(t, a)
	a.SetDestination(root, "{year}/{tag}/{filename}")
	files := []fileinfo.FileInfo{mk("ready_2020.txt", "R"), mk("done_2020.txt", "D"), mk("clash_2020.txt", "NEW"), mk("untagged_2020.txt", "U")}
	if _, err := a.ApplyScan(in, files, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		a.ToggleByShortcut("1")
		a.Next()
	}
	// destination state: done is identical, clash differs
	os.MkdirAll(filepath.Join(root, "2020", "Family"), 0o755)
	os.WriteFile(filepath.Join(root, "2020", "Family", "done_2020.txt"), []byte("D"), 0o644)
	os.WriteFile(filepath.Join(root, "2020", "Family", "clash_2020.txt"), []byte("OLD"), 0o644)

	a.SetFilter(FilterReady)
	if a.StatusesKnown() || a.Count() != 0 {
		t.Fatal("status filters are empty until statuses are computed")
	}
	if err := a.RefreshStatuses(context.Background(), nil); err != nil || !a.StatusesKnown() {
		t.Fatal(err)
	}
	check := func(f Filter, want ...string) {
		t.Helper()
		a.SetFilter(f)
		if !eq(names(a), want...) {
			t.Fatalf("%s: %v want %v", f, names(a), want)
		}
	}
	check(FilterReady, "ready_2020.txt")
	check(FilterAlreadyCopied, "done_2020.txt")
	check(FilterConflicts, "clash_2020.txt")
	check(FilterErrors)

	// copying updates the statuses: ready becomes already-copied
	jobs := a.CopyJobs(true)
	sum, err := a.RunCopy(context.Background(), jobs, copier.Options{})
	if err != nil || sum.Copied != 1 || sum.ConflictsSkipped != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	check(FilterReady)
	check(FilterAlreadyCopied, "ready_2020.txt", "done_2020.txt")
	check(FilterConflicts, "clash_2020.txt")

	// changing the destination invalidates the statuses
	a.SetDestination(t.TempDir(), "{year}/{tag}/{filename}")
	if a.StatusesKnown() {
		t.Fatal("statuses must be invalidated")
	}
	if FilterByLabel(FilterConflicts.String()) != FilterConflicts || !FilterReady.NeedsStatus() || FilterTagged.NeedsStatus() {
		t.Fatal("filter helpers")
	}
}

func TestNeighborsAndGoTo(t *testing.T) {
	a := New()
	load(t, a, "a", "b", "c", "d", "e")
	a.Next()
	a.Next()
	ns, cur := a.Neighbors(1, 1)
	if len(ns) != 3 || ns[cur].Name != "c" || ns[0].Name != "b" {
		t.Fatalf("%v %d", ns, cur)
	}
	ns, cur = a.Neighbors(5, 5)
	if len(ns) != 5 || cur != 2 {
		t.Fatal(len(ns), cur)
	}
	if !a.GoTo("/x/e") {
		t.Fatal("GoTo failed")
	}
	if f, _ := a.Current(); f.Name != "e" {
		t.Fatal(f.Name)
	}
	if a.GoTo("/x/zzz") {
		t.Fatal("GoTo unknown")
	}
}

func TestRecordChecksDropsStaleGeneration(t *testing.T) {
	a := New()
	withTags(t, a)
	load(t, a, "a_2020.txt")
	a.ToggleByShortcut("1")
	f, _ := a.Current()
	a.mu.RLock()
	gen := a.statusGen
	assigned := append([]tags.Tag(nil), a.tagList[:1]...)
	a.mu.RUnlock()
	checks := []destination.Check{{Status: destination.StatusReady}}

	a.mu.Lock()
	a.invalidateStatusesLocked() // e.g. a profile switch while the check ran
	a.mu.Unlock()
	a.recordChecks(gen, f.ID, assigned, checks)
	a.mu.RLock()
	_, stored := a.statuses[f.ID]
	a.mu.RUnlock()
	if stored {
		t.Fatal("a result computed under an older generation must be dropped")
	}

	a.mu.RLock()
	gen = a.statusGen
	a.mu.RUnlock()
	a.recordChecks(gen, f.ID, assigned, checks)
	a.mu.RLock()
	_, stored = a.statuses[f.ID]
	a.mu.RUnlock()
	if !stored {
		t.Fatal("a current-generation result must be stored")
	}
}
