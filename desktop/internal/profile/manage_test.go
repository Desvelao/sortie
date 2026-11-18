package profile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "sortie/internal/profile"
)

func TestSlugAndUniqueID(t *testing.T) {
	for in, want := range map[string]string{"Family photos": "family-photos", "  --Ñandú 2020!! ": "and-2020", "": "profile", "///": "profile", "A_B": "a-b"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q want %q", in, got, want)
		}
	}
	dir := t.TempDir()
	if id := UniqueID(dir, "work"); id != "work" {
		t.Fatal(id)
	}
	os.WriteFile(filepath.Join(dir, "work.yaml"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "work-2.yml"), nil, 0o644)
	if id := UniqueID(dir, "work"); id != "work-3" {
		t.Fatal(id)
	}
}

func TestCreateDuplicateRename(t *testing.T) {
	dir := t.TempDir()
	id, err := Create(dir, "My Photos")
	if err != nil || id != "my-photos" {
		t.Fatal(id, err)
	}
	id2, err := Create(dir, "My Photos") // same name: never overwrites
	if err != nil || id2 != "my-photos-2" {
		t.Fatal(id2, err)
	}
	if _, err := Create(dir, "  "); err == nil {
		t.Fatal("empty name accepted")
	}

	// Duplicate keeps hand-written comments and only changes the name
	src := PathFor(dir, "commented")
	text := "# my notes about this profile\nversion: 1\nname: Original # shown in the selector\nrecursive: true\ntags:\n  - {name: A, shortcut: \"1\"}\n"
	os.WriteFile(src, []byte(text), 0o644)
	cid, err := Duplicate(dir, "commented", "Copy of it")
	if err != nil || cid != "copy-of-it" {
		t.Fatal(cid, err)
	}
	b, _ := os.ReadFile(PathFor(dir, cid))
	if !strings.Contains(string(b), "# my notes about this profile") || !strings.Contains(string(b), "name: Copy of it") {
		t.Fatalf("%s", b)
	}
	if o, _ := os.ReadFile(src); string(o) != text {
		t.Fatal("original changed")
	}

	// Rename changes the name but not the id (file)
	if err := Rename(dir, "commented", "Renamed"); err != nil {
		t.Fatal(err)
	}
	p, err := Load(src)
	if err != nil || p.Name != "Renamed" {
		t.Fatal(p.Name, err)
	}
	b, _ = os.ReadFile(src)
	if !strings.Contains(string(b), "# my notes about this profile") {
		t.Fatalf("comment lost on rename:\n%s", b)
	}
	if Rename(dir, "nope", "X") == nil || Rename(dir, "commented", "") == nil || Rename(dir, "../x", "X") == nil {
		t.Fatal("bad rename accepted")
	}
}

func TestDeleteIsRecoverableAndInvisible(t *testing.T) {
	dir := t.TempDir()
	Create(dir, "Gone")
	dst, err := Delete(dir, "gone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); err != nil || !strings.Contains(dst, ".trash") {
		t.Fatal(dst, err)
	}
	if es, _ := List(dir); len(es) != 0 {
		t.Fatalf("deleted profile still listed: %v", es)
	}
	if _, err := Delete(dir, "gone"); err == nil {
		t.Fatal("deleting twice succeeded")
	}
	// a restored file is simply listed again
	restored := PathFor(dir, "gone")
	os.Rename(dst, restored)
	if es, _ := List(dir); len(es) != 1 {
		t.Fatal(es)
	}
}

func TestImportExport(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	good := filepath.Join(other, "Shared Profile.yaml")
	os.WriteFile(good, []byte(good0), 0o644)
	id, err := Import(dir, good)
	if err != nil || id != "shared-profile" {
		t.Fatal(id, err)
	}
	id2, _ := Import(dir, good) // second import never replaces the first
	if id2 != "shared-profile-2" {
		t.Fatal(id2)
	}
	if b, _ := os.ReadFile(PathFor(dir, id)); string(b) != good0 {
		t.Fatal("import altered the file")
	}
	bad := filepath.Join(other, "bad.yaml")
	os.WriteFile(bad, []byte("version: 1\nname: X\nbogus: 1\ntags: [{name: A}]\n"), 0o644)
	if _, err := Import(dir, bad); err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Fatal(err)
	}
	if es, _ := List(dir); len(es) != 2 {
		t.Fatalf("a rejected import left a file: %v", es)
	}
	out := filepath.Join(other, "export.yaml")
	if err := Export(dir, id, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != good0 {
		t.Fatal("export differs")
	}
	if Export(dir, "missing", out) == nil {
		t.Fatal("exported a missing profile")
	}
}

const good0 = "# shared with the team\nversion: 1\nname: Shared\ntags:\n  - name: A\n"

// Saving from the app must keep what the user wrote by hand.
func TestSaveKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.yaml")
	text := `# Photos from the family archive.
# Edit freely.
version: 1
name: Family # display name
recursive: true
destination_template: "{year}/{tag}/{filename}"
variables:
  # the year a photo was taken
  - name: year
    sources:
      - kind: manual
      - kind: filename # first choice
        pattern: '(20[0-9]{2})'
        group: 1
tags:
  # most used
  - name: Family
    shortcut: "1"
  - name: Work
    shortcut: "2"
`
	os.WriteFile(path, []byte(text), 0o644)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p.Recursive = false // a change made in the app
	p.Tags = append([]Tag{{Name: "New", Shortcut: "3"}}, p.Tags...)
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	out := string(b)
	for _, want := range []string{"# Photos from the family archive.", "# Edit freely.", "# display name", "# the year a photo was taken", "# first choice", "# most used"} {
		if !strings.Contains(out, want) {
			t.Errorf("comment %q lost:\n%s", want, out)
		}
	}
	got, err := Load(path)
	if err != nil || got.Recursive || len(got.Tags) != 3 || got.Tags[0].Name != "New" {
		t.Fatalf("%+v %v", got, err)
	}
	// "# most used" stays with the tag it described, although it moved down
	i, k := strings.Index(out, "# most used"), strings.Index(out, "name: Family\n    shortcut")
	if i < 0 || k < i || strings.Index(out, "name: New") > i {
		t.Errorf("comment not attached to its tag:\n%s", out)
	}
}

func TestResolvePathKeepsYmlFiles(t *testing.T) {
	dir := t.TempDir()
	yml := filepath.Join(dir, "old.yml")
	p := New("Old")
	p.Tags = []Tag{{Name: "A"}}
	if err := Save(yml, p); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePath(dir, "old"); got != yml {
		t.Fatalf("%s", got)
	}
	if got := ResolvePath(dir, "fresh"); got != filepath.Join(dir, "fresh.yaml") {
		t.Fatalf("%s", got)
	}
}

func TestSavedProfilesAreShareable(t *testing.T) {
	dir := t.TempDir()
	path := PathFor(dir, "p")
	p := New("P")
	p.Tags = []Tag{{Name: "A"}}
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Fatalf("new file mode %v", st.Mode().Perm())
	}
	// a stricter mode chosen by the user is kept on later saves
	os.Chmod(path, 0o600)
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
}

func TestDeleteTwiceInOneSecondKeepsBothBackups(t *testing.T) {
	dir := t.TempDir()
	Create(dir, "Dup")
	first, err := Delete(dir, "dup")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(first, []byte("first backup"), 0o644) // mark it
	Create(dir, "Dup")
	second, err := Delete(dir, "dup")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both deletes used %s", first)
	}
	if b, _ := os.ReadFile(first); string(b) != "first backup" {
		t.Fatal("the first trashed profile was overwritten")
	}
}

func TestListFlagsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "Same"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(PathFor(dir, "same"))
	os.WriteFile(filepath.Join(dir, "same.yml"), b, 0o644)
	es, err := List(dir)
	if err != nil || len(es) != 2 {
		t.Fatalf("%v %v", es, err)
	}
	var good, bad int
	for _, e := range es {
		if e.Err == nil {
			good++
		} else if strings.Contains(e.Err.Error(), "same.yaml") {
			bad++
		}
	}
	if good != 1 || bad != 1 {
		t.Fatalf("exactly one entry (the .yml) must be flagged: %+v", es)
	}
}
