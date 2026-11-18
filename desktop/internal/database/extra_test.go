package database

import (
	"testing"

	"sortie/internal/fileinfo"
	"sortie/internal/tags"
)

// PurgeMissing removes only MISSING rows below dir (not siblings such as
// /a/bc for /a/b) and keeps rows that have copy history.
func TestPurgeMissingPrefixAndHistory(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tag := []tags.Tag{{ID: 1, Name: "T", Destination: "t", Enabled: true}}
	if err := db.SyncTags(tag); err != nil {
		t.Fatal(err)
	}
	ids, err := db.SyncProfileTags(DefaultProfile, tag)
	if err != nil {
		t.Fatal(err)
	}
	inB := []fileinfo.FileInfo{fi("/a/b/x.txt", 1), fi("/a/b/kept.txt", 2)}
	inBC := []fileinfo.FileInfo{fi("/a/bc/y.txt", 3)}
	for dir, fs := range map[string][]fileinfo.FileInfo{"/a/b": inB, "/a/bc": inBC} {
		if _, err := db.SyncFiles(dir, fs, true); err != nil {
			t.Fatal(err)
		}
	}
	// a copy record keeps /a/b/kept.txt
	if _, err := db.BeginCopy(inB[1].ID, ids["T"], "T", "/a/b/kept.txt", "/out/kept.txt"); err != nil {
		t.Fatal(err)
	}
	// everything disappears
	db.SyncFiles("/a/b", nil, true)
	db.SyncFiles("/a/bc", nil, true)

	n, err := db.PurgeMissing("/a/b")
	if err != nil || n != 1 {
		t.Fatalf("purged %d (%v), want 1: only x.txt, not kept.txt (history) or /a/bc/y.txt (sibling)", n, err)
	}
	var left int
	db.sql.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&left)
	if left != 2 {
		t.Fatalf("%d rows left, want 2", left)
	}
	if n, _ := db.PurgeMissing(""); n != 1 { // y.txt; kept.txt still has history
		t.Fatalf("global purge removed %d, want 1", n)
	}
}

// Two tags can swap their (UNIQUE) shortcuts in a single sync.
func TestSyncProfileTagsSwapsShortcuts(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := []tags.Tag{
		{ID: 1, Name: "A", Shortcut: "1", Destination: "a", Enabled: true},
		{ID: 2, Name: "B", Shortcut: "2", Destination: "b", Enabled: true, SortOrder: 1},
	}
	if _, err := db.SyncProfileTags("p", ts); err != nil {
		t.Fatal(err)
	}
	ts[0].Shortcut, ts[1].Shortcut = "2", "1"
	if _, err := db.SyncProfileTags("p", ts); err != nil {
		t.Fatalf("swap failed: %v", err)
	}
	var sc string
	db.sql.QueryRow(`SELECT shortcut FROM tags WHERE profile='p' AND name='A'`).Scan(&sc)
	if sc != "2" {
		t.Errorf("tag A shortcut = %q, want 2", sc)
	}
}

// tags.Validate treats names case-insensitively, stricter than the database's
// case-sensitive UNIQUE(profile, name), so such a set never reaches the database.
func TestValidateRejectsCaseInsensitiveDuplicateNames(t *testing.T) {
	err := tags.Validate([]tags.Tag{
		{ID: 1, Name: "Work", Destination: "a", Enabled: true},
		{ID: 2, Name: "work", Destination: "b", Enabled: true},
	})
	if err == nil {
		t.Fatal("expected a duplicate-name error")
	}
}
