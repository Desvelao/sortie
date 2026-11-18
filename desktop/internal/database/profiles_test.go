package database

import (
	"database/sql"
	"testing"

	"sortie/internal/tags"
)

func tg(name, key string) tags.Tag {
	return tags.Tag{Name: name, Shortcut: key, Destination: name, Enabled: true}
}

func TestProfileTagsIsolatedAndKeyedByName(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	a, err := db.SyncProfileTags("a", []tags.Tag{tg("Work", "1"), tg("Home", "2")})
	if err != nil {
		t.Fatal(err)
	}
	// same names and shortcuts in another profile must not collide
	b, err := db.SyncProfileTags("b", []tags.Tag{tg("Work", "1")})
	if err != nil {
		t.Fatal(err)
	}
	if a["Work"] == b["Work"] {
		t.Fatal("tag ids shared between profiles")
	}
	if _, err := db.sql.Exec(`INSERT INTO files(source_path, filename) VALUES('/x','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ToggleTag(1, a["Work"]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ToggleTag(1, b["Work"]); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.LoadProfileAssignments("a"); len(got[1]) != 1 || got[1][0] != a["Work"] {
		t.Fatalf("a: %v", got)
	}
	if got, _ := db.LoadProfileAssignments("b"); len(got[1]) != 1 || got[1][0] != b["Work"] {
		t.Fatalf("b: %v", got)
	}

	// resync keeps ids, swaps shortcuts, drops removed tags with their assignments
	a2, err := db.SyncProfileTags("a", []tags.Tag{tg("Work", "2")})
	if err != nil || a2["Work"] != a["Work"] {
		t.Fatalf("%v %v", a2, err)
	}
	if got, _ := db.LoadProfileAssignments("a"); len(got[1]) != 1 {
		t.Fatalf("assignment lost: %v", got)
	}
	var n int
	db.sql.QueryRow(`SELECT count(*) FROM tags WHERE profile='a'`).Scan(&n)
	if n != 1 {
		t.Fatalf("removed tag still present: %d", n)
	}
	// profile b untouched
	if got, _ := db.LoadProfileAssignments("b"); len(got[1]) != 1 {
		t.Fatalf("b changed: %v", got)
	}
	// a rename is a new tag: assignment starts over
	a3, _ := db.SyncProfileTags("a", []tags.Tag{tg("Job", "2")})
	if a3["Job"] == a["Work"] {
		t.Fatal("renamed tag kept its id")
	}
	if got, _ := db.LoadProfileAssignments("a"); len(got[1]) != 0 {
		t.Fatalf("assignment survived rename: %v", got)
	}
}

func TestFileVars(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	db.sql.Exec(`INSERT INTO files(source_path, filename) VALUES('/x','x')`)
	if err := db.SetFileVar(1, "p", "year", " 2020 "); err != nil {
		t.Fatal(err)
	}
	db.SetFileVar(1, "p", "year", "2021") // update
	db.SetFileVar(1, "p", "client", "acme")
	db.SetFileVar(1, "q", "year", "1999")
	got, _ := db.LoadFileVars("p")
	if got[1]["year"] != "2021" || got[1]["client"] != "acme" || len(got[1]) != 2 {
		t.Fatalf("%v", got)
	}
	if q, _ := db.LoadFileVars("q"); q[1]["year"] != "1999" {
		t.Fatalf("%v", q)
	}
	if err := db.SetFileVar(999, "p", "year", "1"); err == nil {
		t.Fatal("expected error for unknown file")
	}
	db.SetFileVar(1, "p", "client", "  ") // clears
	db.PruneFileVars("p", []string{"year"})
	if got, _ := db.LoadFileVars("p"); len(got[1]) != 1 {
		t.Fatalf("%v", got)
	}
	db.PruneFileVars("p", nil)
	if got, _ := db.LoadFileVars("p"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestDeleteProfile(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	a, _ := db.SyncProfileTags("a", []tags.Tag{tg("Work", "1")})
	b, _ := db.SyncProfileTags("b", []tags.Tag{tg("Work", "1")})
	db.sql.Exec(`INSERT INTO files(source_path, filename) VALUES('/x','x')`)
	db.ToggleTag(1, a["Work"])
	db.ToggleTag(1, b["Work"])
	db.SetFileVar(1, "a", "year", "2020")
	db.SetFileVar(1, "b", "year", "2021")
	db.sql.Exec(`INSERT INTO copy_history(file_id,tag_id,tag_name,source_path,destination_path,status) VALUES(1,?,'Work','/x','/d/x','COPIED')`, a["Work"])

	if err := db.DeleteProfile("a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.LoadProfileAssignments("a"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if v, _ := db.LoadFileVars("a"); len(v) != 0 {
		t.Fatalf("%v", v)
	}
	// the other profile and the copy history survive
	if got, _ := db.LoadProfileAssignments("b"); len(got[1]) != 1 {
		t.Fatalf("%v", got)
	}
	if v, _ := db.LoadFileVars("b"); v[1]["year"] != "2021" {
		t.Fatalf("%v", v)
	}
	var n int
	var tagID sql.NullInt64
	db.sql.QueryRow(`SELECT count(*), max(tag_id) FROM copy_history WHERE tag_name='Work'`).Scan(&n, &tagID)
	if n != 1 || tagID.Valid {
		t.Fatalf("history rows=%d tag_id=%v", n, tagID)
	}
}

func TestCountTaggedFiles(t *testing.T) {
	db, _ := OpenMemory()
	defer db.Close()
	a, _ := db.SyncProfileTags("a", []tags.Tag{tg("Work", "1"), tg("Home", "2")})
	b, _ := db.SyncProfileTags("b", []tags.Tag{tg("Work", "1")})
	db.sql.Exec(`INSERT INTO files(source_path, filename) VALUES('/x','x'),('/y','y')`)
	db.ToggleTag(1, a["Work"])
	db.ToggleTag(1, a["Home"]) // same file, two tags: counted once
	db.ToggleTag(2, b["Work"])
	if n, err := db.CountTaggedFiles("a"); err != nil || n != 1 {
		t.Fatalf("a: %d %v", n, err)
	}
	if n, _ := db.CountTaggedFiles("none"); n != 0 {
		t.Fatalf("none: %d", n)
	}
}
