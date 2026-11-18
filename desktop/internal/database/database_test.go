package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sortie/internal/fileinfo"
	"sortie/internal/tags"
)

func fi(path string, size int64) fileinfo.FileInfo {
	return fileinfo.FileInfo{Path: path, Name: filepath.Base(path), Size: size, ModTime: time.Unix(1700000000, 0)}
}

// manualYear returns the manual "year" value stored for the first file in
// profile "p" (identity of files, and thus their manual values, must survive
// rescans and renames).
func manualYear(db *DB) string {
	v, _ := db.LoadFileVars("p")
	for _, m := range v {
		return m["year"]
	}
	return ""
}

func TestSyncFilesAddModifyRemove(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files := []fileinfo.FileInfo{fi("/in/a.txt", 1), fi("/in/ü.bin", 2), fi("/in/sub/c.txt", 3)}
	res, err := db.SyncFiles("/in", files[:2], false)
	if err != nil || res.Added != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	idA := files[0].ID
	if idA == 0 {
		t.Fatal("id not set")
	}
	if err := db.SetFileVar(idA, "p", "year", "2012"); err != nil {
		t.Fatal(err)
	}
	// rescan: a unchanged, ü modified, one new, nothing removed
	files2 := []fileinfo.FileInfo{fi("/in/a.txt", 1), fi("/in/ü.bin", 99), fi("/in/new.txt", 5)}
	res, err = db.SyncFiles("/in", files2, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Modified != 1 || res.Removed != 0 {
		t.Fatalf("%+v", res)
	}
	if files2[0].ID != idA || manualYear(db) != "2012" {
		t.Fatalf("identity lost: %+v", files2[0])
	}
	// remove a.txt; non-recursive scan must not touch sub/ rows
	_, _ = db.SyncFiles("/in", append(files2[:3:3], files[2]), true)
	res, _ = db.SyncFiles("/in", []fileinfo.FileInfo{fi("/in/ü.bin", 99), fi("/in/new.txt", 5)}, false)
	var subStatus string
	db.sql.QueryRow(`SELECT status FROM files WHERE source_path='/in/sub/c.txt'`).Scan(&subStatus)
	if subStatus != "PENDING" {
		t.Fatalf("sub/ row touched by non-recursive scan: %s", subStatus)
	}
	if res.Removed != 1 {
		t.Fatalf("%+v", res)
	}
	// reappears: classification (manual year) intact
	back := []fileinfo.FileInfo{fi("/in/a.txt", 1)}
	db.SyncFiles("/in", back, false)
	if back[0].ID != idA || manualYear(db) != "2012" {
		t.Fatalf("%+v", back[0])
	}
}

func TestTagsToggleAndSync(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	files := []fileinfo.FileInfo{fi("/in/a.txt", 1)}
	db.SyncFiles("/in", files, false)
	ts := tags.Defaults()
	if err := db.SyncTags(ts); err != nil {
		t.Fatal(err)
	}
	id := files[0].ID
	for _, tg := range []int{1, 5} {
		if on, err := db.ToggleTag(id, tg); err != nil || !on {
			t.Fatal(on, err)
		}
	}
	if on, _ := db.ToggleTag(id, 1); on {
		t.Fatal("toggle off failed")
	}
	a, _ := db.LoadAssignments()
	if len(a[id]) != 1 || a[id][0] != 5 {
		t.Fatalf("%v", a)
	}
	// swap shortcuts/names, remove tag 5: assignment of 5 is dropped, no UNIQUE errors
	ts[0].Shortcut, ts[1].Shortcut = ts[1].Shortcut, ts[0].Shortcut
	ts[0].Name, ts[1].Name = ts[1].Name, ts[0].Name
	if err := db.SyncTags(append(ts[:4:4], ts[5:]...)); err != nil {
		t.Fatal(err)
	}
	a, _ = db.LoadAssignments()
	if len(a[id]) != 0 {
		t.Fatalf("%v", a)
	}
	if _, err := db.ToggleTag(id, 5); err == nil {
		t.Fatal("FK should reject unknown tag")
	}
	if err := db.SyncTags(nil); err != nil {
		t.Fatal(err)
	}
}

func TestPersistenceAndSession(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x", "db.sqlite")
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	files := []fileinfo.FileInfo{fi("/in/a.txt", 1)}
	db.SyncFiles("/in", files, false)
	db.SyncTags(tags.Defaults())
	db.ToggleTag(files[0].ID, 2)
	db.SaveSession("/in", files[0].ID)
	db.Close()

	db, err = Open(p) // reopen: schema creation is idempotent, data intact
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, _ := db.LoadAssignments()
	if len(a[files[0].ID]) != 1 {
		t.Fatalf("%v", a)
	}
	dir, id, ok, err := db.LoadSession()
	if err != nil || !ok || dir != "/in" || id != files[0].ID {
		t.Fatal(dir, id, ok, err)
	}
}

func TestCopyHistoryLifecycle(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	files := []fileinfo.FileInfo{fi("/in/a.txt", 1)}
	db.SyncFiles("/in", files, false)
	db.SyncTags(tags.Defaults())
	id := files[0].ID

	h1, err := db.BeginCopy(id, 2, "Holidays", "/in/a.txt", "/out/2020/Holidays/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishCopy(h1, "COPIED", "abc", "abc", ""); err != nil {
		t.Fatal(err)
	}
	h2, _ := db.BeginCopy(id, 3, "Events", "/in/a.txt", "/out/2020/Events/a.txt")
	db.NoteCopy(h2, "/out/2020/Events/.sortie-123.tmp")

	// simulated crash: h2 never finished
	n, err := db.MarkInterrupted()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	hist, _ := db.History(id)
	if len(hist) != 2 || hist[0].Status != CopyInterrupted || !strings.Contains(hist[0].ErrorMsg, ".sortie-123.tmp") ||
		hist[1].Status != "COPIED" || hist[1].SourceHash != "abc" || hist[1].CopiedAt.IsZero() {
		t.Fatalf("%+v", hist)
	}
	if n, _ := db.MarkInterrupted(); n != 0 {
		t.Fatal("second call must find nothing")
	}

	// removing a tag keeps its history (tag id becomes NULL, name stays)
	if err := db.SyncTags(tags.Defaults()[:1]); err != nil {
		t.Fatal(err)
	}
	hist, _ = db.History(id)
	if len(hist) != 2 || hist[1].TagID != 0 || hist[1].TagName != "Holidays" {
		t.Fatalf("%+v", hist)
	}
}

func TestRenameDetectionKeepsClassification(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	db.SyncTags(tags.Defaults())
	old := []fileinfo.FileInfo{fi("/in/IMG_0001.jpg", 111), fi("/in/same1.txt", 5), fi("/in/same2.txt", 5), fi("/in/keep.txt", 9)}
	db.SyncFiles("/in", old, false)
	idRen := old[0].ID
	db.SetFileVar(idRen, "p", "year", "2011")
	db.ToggleTag(idRen, 1)

	// IMG_0001.jpg renamed; same1/same2 renamed but ambiguous (identical size+mtime); keep.txt untouched
	now := []fileinfo.FileInfo{fi("/in/holiday.jpg", 111), fi("/in/x1.txt", 5), fi("/in/x2.txt", 5), fi("/in/keep.txt", 9)}
	res, err := db.SyncFiles("/in", now, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != 1 || res.Added != 2 || res.Removed != 2 {
		t.Fatalf("%+v", res)
	}
	if now[0].ID != idRen || manualYear(db) != "2011" {
		t.Fatalf("identity lost on rename: %+v", now[0])
	}
	a, _ := db.LoadAssignments()
	if len(a[idRen]) != 1 {
		t.Fatalf("tags lost on rename: %v", a)
	}
	// a rescan changes nothing
	res, _ = db.SyncFiles("/in", now, false)
	if res.Renamed+res.Added+res.Removed+res.Modified != 0 {
		t.Fatalf("%+v", res)
	}
	// empty files are never matched by size
	e1 := []fileinfo.FileInfo{fi("/in/empty1", 0)}
	db.SyncFiles("/in", append(now, e1...), false)
	e2 := []fileinfo.FileInfo{fi("/in/empty2", 0)}
	res, _ = db.SyncFiles("/in", append(append([]fileinfo.FileInfo{}, now...), e2...), false)
	if res.Renamed != 0 {
		t.Fatalf("empty files matched as rename: %+v", res)
	}
}

func TestBeginCopyWithoutTagStoresNull(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	files := []fileinfo.FileInfo{fi("/in/a.txt", 1)}
	db.SyncFiles("/in", files, false)
	id, err := db.BeginCopy(files[0].ID, 0, "Removed tag", "/in/a.txt", "/out/a.txt")
	if err != nil {
		t.Fatalf("tag 0 must not violate the tags foreign key: %v", err)
	}
	if err := db.FinishCopy(id, "COPIED", "h", "h", ""); err != nil {
		t.Fatal(err)
	}
	hist, _ := db.History(files[0].ID)
	if len(hist) != 1 || hist[0].TagID != 0 || hist[0].TagName != "Removed tag" {
		t.Fatalf("%+v", hist)
	}
}

func TestPurgeMissing(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	ids, _ := db.SyncProfileTags(DefaultProfile, tags.Defaults())
	var tagID int
	for _, id := range ids {
		tagID = id
		break
	}
	a, b, c, keep := fi("/in/a.txt", 1), fi("/in/b.txt", 2), fi("/other/c.txt", 3), fi("/in/keep.txt", 4)
	first := []fileinfo.FileInfo{a, b, keep}
	db.SyncFiles("/in", first, false)
	other := []fileinfo.FileInfo{c}
	db.SyncFiles("/other", other, false)
	db.ToggleTag(first[0].ID, tagID)
	db.SetFileVar(first[0].ID, DefaultProfile, "year", "2020")
	hid, _ := db.BeginCopy(first[2].ID, 0, "x", "/in/keep.txt", "/out/keep.txt") // has history
	db.FinishCopy(hid, "COPIED", "h", "h", "")

	// everything in /in and /other vanishes except b
	db.SyncFiles("/in", []fileinfo.FileInfo{b}, false)
	db.SyncFiles("/other", nil, false)

	n, err := db.PurgeMissing("/in")
	if err != nil || n != 1 { // a only: b is present, keep has history
		t.Fatalf("purged %d, %v", n, err)
	}
	var cnt int
	db.sql.QueryRow(`SELECT count(*) FROM files WHERE source_path='/in/a.txt'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("a.txt should be gone")
	}
	if got, _ := db.LoadAssignments(); len(got[first[0].ID]) != 0 {
		t.Fatalf("assignments of a purged file remain: %v", got)
	}
	if v, _ := db.LoadFileVars(DefaultProfile); len(v[first[0].ID]) != 0 {
		t.Fatalf("variables of a purged file remain: %v", v)
	}
	db.sql.QueryRow(`SELECT count(*) FROM files WHERE source_path IN ('/in/b.txt','/in/keep.txt','/other/c.txt')`).Scan(&cnt)
	if cnt != 3 {
		t.Fatalf("present, history-bearing and out-of-scope rows must stay, have %d", cnt)
	}
	if n, err := db.PurgeMissing(""); err != nil || n != 1 { // now c, everywhere
		t.Fatalf("purged %d, %v", n, err)
	}
}

func TestHistoryUpdatesOfUnknownIDFail(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	if err := db.FinishCopy(12345, "COPIED", "h", "h", ""); err == nil {
		t.Fatal("FinishCopy of an unknown id must fail")
	}
	if err := db.NoteCopy(12345, "x"); err == nil {
		t.Fatal("NoteCopy of an unknown id must fail")
	}
}

func TestSyncFilesNonRecursiveLeavesSubdirectoriesAlone(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	all := []fileinfo.FileInfo{fi("/in/a.txt", 1), fi("/in/sub/b.txt", 2)}
	if _, err := db.SyncFiles("/in", all, true); err != nil {
		t.Fatal(err)
	}
	// a non-recursive scan only sees /in/a.txt; /in/sub/b.txt must not become MISSING
	res, err := db.SyncFiles("/in", []fileinfo.FileInfo{fi("/in/a.txt", 1)}, false)
	if err != nil || res.Removed != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	var status string
	db.sql.QueryRow(`SELECT status FROM files WHERE source_path='/in/sub/b.txt'`).Scan(&status)
	if status != "PENDING" {
		t.Fatalf("sub-directory row status %q", status)
	}
}

func TestSyncFilesAmbiguousRenameIsAddPlusRemove(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	if _, err := db.SyncFiles("/in", []fileinfo.FileInfo{fi("/in/a.txt", 5), fi("/in/b.txt", 5)}, false); err != nil {
		t.Fatal(err)
	}
	// both vanish, two new files with the identical size and mtime appear:
	// there is no way to tell which is which, so nothing is treated as renamed
	res, err := db.SyncFiles("/in", []fileinfo.FileInfo{fi("/in/c.txt", 5), fi("/in/d.txt", 5)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != 0 || res.Added != 2 || res.Removed != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestFreshDatabaseHasForeignKeysOn(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	var fk int
	db.sql.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign keys must be on")
	}
}

func TestDatabaseFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil { // too permissive on purpose
		t.Fatal(err)
	}
	path := filepath.Join(dir, "database.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("data dir: %v %v, want 0700", st, err)
	}
	for _, f := range []string{path, path + "-wal"} {
		if st, err := os.Stat(f); err == nil && st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is accessible to others: %v", f, st.Mode().Perm())
		}
	}
}
