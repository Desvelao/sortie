package destination

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sortie/internal/tags"
)

func tag(name, dest string) tags.Tag {
	return tags.Tag{ID: 1, Name: name, Destination: dest, Enabled: true}
}

// yres is a resolver using the classic year/tag layout.
func yres(root string) Resolver {
	return Resolver{Root: root, Template: "{year}/{tag}/{filename}", VarNames: []string{"year"}}
}

// yr builds the variable map of a file whose year is n (0 = unresolved).
func yr(n int) map[string]string {
	if n == 0 {
		return nil
	}
	return map[string]string{"year": strconv.Itoa(n)}
}

func TestResolveDefaultTemplate(t *testing.T) {
	root := t.TempDir()
	r := yres(root)
	got, err := r.Resolve("IMG_2021.jpg", yr(2021), tag("Family", "Family"))
	if err != nil || got != filepath.Join(root, "2021", "Family", "IMG_2021.jpg") {
		t.Fatal(got, err)
	}
	// each tag gets its own independent destination, nested destinations and Unicode work
	a, _ := r.Resolve("ñandú 日本.pdf", yr(2019), tag("Music", "Music/Live Sets"))
	b, _ := r.Resolve("ñandú 日本.pdf", yr(2019), tag("Events", "Events"))
	if a == b || !strings.HasSuffix(a, filepath.Join("2019", "Music", "Live Sets", "ñandú 日本.pdf")) {
		t.Fatal(a, b)
	}
	r.Template = "{year:pad4}/{tag}/{filename}"
	if p, _ := r.Resolve("x.txt", yr(7), tag("A", "A")); !strings.Contains(p, filepath.Join(root, "0007")) {
		t.Fatal(p)
	}
}

func TestResolveCustomTemplates(t *testing.T) {
	root := t.TempDir()
	r := Resolver{Root: root, Template: "{tag}/{year}/{name}_copy{ext}", VarNames: []string{"year"}}
	got, err := r.Resolve("a.tar.gz", yr(2020), tag("Archive", "Arc"))
	if err != nil || got != filepath.Join(root, "Arc", "2020", "a.tar_copy.gz") {
		t.Fatal(got, err)
	}
	r.Template = "{filename}"
	if got, _ := r.Resolve("noext", yr(2020), tag("A", "A")); got != filepath.Join(root, "noext") {
		t.Fatal(got)
	}
}

func TestResolveRejectsTraversalAndBadInput(t *testing.T) {
	root := t.TempDir()
	r := yres(root)
	good := tag("A", "A")
	for _, name := range []string{"..", ".", "", "a/b", "../x", "a\x00b"} {
		if _, err := r.Resolve(name, yr(2020), good); err == nil {
			t.Errorf("file name %q accepted", name)
		}
	}
	for _, dest := range []string{"../out", "a/../../out", "/abs", ".", ""} {
		if _, err := r.Resolve("f.txt", yr(2020), tag("A", dest)); err == nil {
			t.Errorf("tag destination %q accepted", dest)
		}
	}
	for _, tpl := range []string{"../{filename}", "{year}/../../{filename}", "/etc/{filename}", "{year}/{tag}", "{bogus}/{filename}", "{year/{filename}", ""} {
		if tpl == "" {
			continue // empty template means default
		}
		if err := ValidateTemplate(tpl, []string{"year"}); err == nil {
			t.Errorf("template %q accepted", tpl)
		}
	}
	if _, err := (yres("relative/dir")).Resolve("f", yr(2020), good); err == nil {
		t.Error("relative root accepted")
	}
	if _, err := (Resolver{}).Resolve("f", yr(2020), good); err == nil {
		t.Error("empty root accepted")
	}
}

func TestResolveUnresolvedVariables(t *testing.T) {
	root := t.TempDir()
	r := yres(root)
	if _, err := r.Resolve("f", nil, tag("A", "A")); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("got %v", err)
	}
	// an explicit default in the template is the "unknown" rule
	r.Template = "{year|Unsorted}/{tag}/{filename}"
	got, err := r.Resolve("f", nil, tag("A", "A"))
	if err != nil || got != filepath.Join(root, "Unsorted", "A", "f") {
		t.Fatal(got, err)
	}
}

func TestResolvePerTagTemplate(t *testing.T) {
	root := t.TempDir()
	r := Resolver{Root: root, Template: "{tag}/{filename}", VarNames: []string{"year", "camera"}}
	work := tag("Work", "Jobs")
	work.DestinationTemplate = "{year}/{camera|unknown}/{tag}/{filename}"
	got, err := r.Resolve("a.jpg", map[string]string{"year": "2020"}, work)
	if err != nil || got != filepath.Join(root, "2020", "unknown", "Jobs", "a.jpg") {
		t.Fatal(got, err)
	}
	// tags without their own template use the resolver's
	got, _ = r.Resolve("a.jpg", nil, tag("Home", "Home"))
	if got != filepath.Join(root, "Home", "a.jpg") {
		t.Fatal(got)
	}
	// a value cannot add directory levels
	got, _ = r.Resolve("a.jpg", map[string]string{"year": "20/../20", "camera": "x"}, work)
	if strings.Contains(got, "..") && !strings.Contains(got, "20_.._20") {
		t.Fatal(got)
	}
	// unknown variable in a tag template is an error
	work.DestinationTemplate = "{nope}/{filename}"
	if _, err := r.Resolve("a.jpg", nil, work); err == nil {
		t.Fatal("unknown variable accepted")
	}
}

func TestResolveSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "2020")); err != nil {
		t.Skip(err)
	}
	if _, err := (yres(root)).Resolve("f.txt", yr(2020), tag("A", "A")); err == nil {
		t.Fatal("symlinked year directory escaping the root was accepted")
	}
	if _, err := (yres(root)).Resolve("f.txt", yr(2021), tag("A", "A")); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckStatuses(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	src := filepath.Join(in, "Concert_2019.mp3")
	put(t, src, "AAAA")
	ts := []tags.Tag{
		{ID: 1, Name: "Music", Destination: "Music", Enabled: true},
		{ID: 2, Name: "Events", Destination: "Events", Enabled: true},
		{ID: 3, Name: "Important", Destination: "Important", Enabled: true},
		{ID: 4, Name: "Archive", Destination: "Archive", Enabled: true},
		{ID: 5, Name: "Dir", Destination: "Dir", Enabled: true},
	}
	put(t, filepath.Join(root, "2019", "Events", "Concert_2019.mp3"), "AAAA")    // identical
	put(t, filepath.Join(root, "2019", "Important", "Concert_2019.mp3"), "BBBB") // same size, other content
	put(t, filepath.Join(root, "2019", "Archive", "Concert_2019.mp3"), "AAAAAA") // other size
	os.MkdirAll(filepath.Join(root, "2019", "Dir", "Concert_2019.mp3"), 0o755)   // directory in the way

	c := NewChecker(2)
	res := c.Check(context.Background(), yres(root), Source{Path: src, Name: "Concert_2019.mp3", Vars: yr(2019)}, ts)
	want := []Status{StatusReady, StatusAlreadyCopied, StatusConflict, StatusConflict, StatusConflict}
	for i, w := range want {
		if res[i].Status != w {
			t.Errorf("%s: got %s (%s) want %s", ts[i].Name, res[i].Status, res[i].Detail, w)
		}
	}
	if res[1].SrcHash == "" || res[1].SrcHash != res[1].DstHash || res[2].SrcHash == res[2].DstHash {
		t.Errorf("hashes: %+v %+v", res[1], res[2])
	}
	if res[3].SrcHash != "" {
		t.Error("different size must not need hashing")
	}
	if !res[0].CanCopy() || res[1].CanCopy() || res[2].CanCopy() {
		t.Error("CanCopy must be true only for READY")
	}
	// the checker is read-only
	if _, err := os.Stat(filepath.Join(root, "2019", "Music")); err == nil {
		t.Error("checker created a directory")
	}
}

func TestCheckUnresolvedVarAndPath(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	src := filepath.Join(in, "unknown_file.pdf")
	put(t, src, "x")
	c := NewChecker(1)
	ts := []tags.Tag{tag("A", "A"), tag("B", "../B")}
	res := c.Check(context.Background(), yres(root), Source{Path: src, Name: "unknown_file.pdf", Vars: yr(0)}, ts)
	if res[0].Status != StatusUnresolvedVar || res[0].CanCopy() {
		t.Fatalf("%+v", res)
	}
	res = c.Check(context.Background(), yres(root), Source{Path: src, Name: "unknown_file.pdf", Vars: yr(2020)}, ts)
	if res[0].Status != StatusReady || res[1].Status != StatusInvalidPath {
		t.Fatalf("%+v", res)
	}
	res = c.Check(context.Background(), Resolver{}, Source{Path: src, Name: "f", Vars: yr(2020)}, ts[:1])
	if res[0].Status != StatusInvalidPath {
		t.Fatalf("%+v", res)
	}
}

// A destination the pre-copy check calls ready must not then fail on its root:
// same rule, same message as the copy engine (which calls CheckRoot too).
func TestCheckReportsAnUnusableRoot(t *testing.T) {
	in := t.TempDir()
	src := filepath.Join(in, "f_2020.txt")
	put(t, src, "x")
	notDir := filepath.Join(t.TempDir(), "afile")
	put(t, notDir, "i am a file")
	c := NewChecker(1)
	for _, tc := range []struct{ root, want string }{
		{filepath.Join(t.TempDir(), "nope"), "not available"},
		{notDir, "not a directory"},
		{"", "not configured"},
	} {
		r := yres(tc.root)
		res := c.Check(context.Background(), r, Source{Path: src, Name: "f_2020.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A")})
		if res[0].Status != StatusInvalidPath || res[0].CanCopy() || !strings.Contains(res[0].Detail, tc.want) {
			t.Errorf("root %q: %+v, want INVALID_PATH containing %q", tc.root, res[0], tc.want)
		}
		if err := r.CheckRoot(); err == nil || err.Error() != res[0].Detail {
			t.Errorf("root %q: CheckRoot %v differs from the check detail %q", tc.root, err, res[0].Detail)
		}
	}
	if res := c.Check(context.Background(), yres(t.TempDir()), Source{Path: src, Name: "f_2020.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A")}); res[0].Status != StatusReady {
		t.Errorf("usable root: %+v", res[0])
	}
}

func TestAllBlocked(t *testing.T) {
	inv, rdy, cf := Check{Status: StatusInvalidPath}, Check{Status: StatusReady}, Check{Status: StatusConflict}
	for _, tc := range []struct {
		name string
		cs   []Check
		want bool
	}{
		{"none", nil, false},
		{"all invalid", []Check{inv, inv}, true},
		{"one ready", []Check{inv, rdy}, false},
		{"a conflict can still be replaced", []Check{inv, cf}, false},
		{"unresolved variable", []Check{{Status: StatusUnresolvedVar}}, true},
		{"invalid and unresolved", []Check{inv, {Status: StatusUnresolvedVar}}, true},
		{"unresolved and ready", []Check{{Status: StatusUnresolvedVar}, rdy}, false},
		{"a filesystem error may be temporary", []Check{{Status: StatusError}}, false},
	} {
		if got := AllBlocked(tc.cs); got != tc.want {
			t.Errorf("%s: AllBlocked = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCheckErrorsAndSelfCopy(t *testing.T) {
	in := t.TempDir()
	c := NewChecker(1)
	src := filepath.Join(in, "f_2020.txt")
	res := c.Check(context.Background(), yres(t.TempDir()), Source{Path: src, Name: "f_2020.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A")})
	if res[0].Status != StatusError {
		t.Fatalf("missing source: %+v", res[0])
	}
	put(t, src, "x")
	// destination root == input dir with template {filename}: destination is the source itself
	res = c.Check(context.Background(), Resolver{Root: in, Template: "{filename}"}, Source{Path: src, Name: "f_2020.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A")})
	if res[0].Status != StatusInvalidPath {
		t.Fatalf("self copy: %+v", res[0])
	}
	// a file where a directory is needed
	root := t.TempDir()
	put(t, filepath.Join(root, "2020"), "i am a file")
	res = c.Check(context.Background(), yres(root), Source{Path: src, Name: "f_2020.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A")})
	if res[0].Status != StatusInvalidPath {
		t.Fatalf("parent is file: %+v", res[0])
	}
}

func TestCheckPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	in, root := t.TempDir(), t.TempDir()
	src := filepath.Join(in, "f.txt")
	put(t, src, "x")
	locked := filepath.Join(root, "2020")
	put(t, filepath.Join(locked, "A", "f.txt"), "x")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o755)
	res := NewChecker(1).Check(context.Background(), yres(root), Source{Path: src, Name: "f.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A")})
	if res[0].Status != StatusError {
		t.Fatalf("%+v", res[0])
	}
}

func TestHashLargeFileCachingAndCancel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.bin")
	f, _ := os.Create(p)
	f.Truncate(96 << 20) // 96 MiB of zeros (sparse)
	f.Close()

	h := newHasher(1)
	start := time.Now()
	s1, err := h.Sum(context.Background(), p)
	if err != nil || len(s1) != 64 {
		t.Fatal(s1, err)
	}
	first := time.Since(start)
	start = time.Now()
	s2, _ := h.Sum(context.Background(), p)
	if s2 != s1 || time.Since(start) > first/2+time.Millisecond {
		t.Errorf("second call should be served from cache (%v vs %v)", time.Since(start), first)
	}
	// a changed file is re-hashed
	os.WriteFile(p, []byte("small"), 0o644)
	if s3, _ := h.Sum(context.Background(), p); s3 == s1 {
		t.Error("stale cache after modification")
	}
	// cancellation is honoured and not cached
	f, _ = os.Create(p)
	f.Truncate(96 << 20)
	f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Sum(ctx, p); err == nil {
		t.Error("expected cancellation error")
	}
	if s, err := h.Sum(context.Background(), p); err != nil || len(s) != 64 {
		t.Error("cancelled attempt poisoned the cache", err)
	}
}

func TestConcurrentChecksAreSafe(t *testing.T) {
	in, root := t.TempDir(), t.TempDir()
	src := filepath.Join(in, "a_2020.txt")
	put(t, src, "data")
	put(t, filepath.Join(root, "2020", "A", "a_2020.txt"), "data")
	c := NewChecker(2)
	done := make(chan bool)
	for i := 0; i < 16; i++ {
		go func() {
			res := c.Check(context.Background(), yres(root), Source{Path: src, Name: "a_2020.txt", Vars: yr(2020)}, []tags.Tag{tag("A", "A"), tag("B", "B")})
			done <- res[0].Status == StatusAlreadyCopied && res[1].Status == StatusReady
		}()
	}
	for i := 0; i < 16; i++ {
		if !<-done {
			t.Fatal("unexpected status under concurrency")
		}
	}
}

func TestResolveDotfileKeepsItsName(t *testing.T) {
	root := t.TempDir()
	r := Resolver{Root: root, Template: "{tag}/{name}{ext}", VarNames: nil}
	got, err := r.Resolve(".bashrc", nil, tag("Dots", "Dots"))
	if err != nil || got != filepath.Join(root, "Dots", ".bashrc") {
		t.Fatal(got, err)
	}
	// {name} alone is the whole dotfile name, never empty
	r.Template = "{tag}/{name}"
	if got, err := r.Resolve(".bashrc", nil, tag("Dots", "Dots")); err != nil || got != filepath.Join(root, "Dots", ".bashrc") {
		t.Fatal(got, err)
	}
}
