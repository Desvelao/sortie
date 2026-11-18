package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(r Result) []string {
	var n []string
	for _, f := range r.Files {
		n = append(n, f.Name)
	}
	return n
}

func TestScanGenericFiles(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "b.unknownext"), "data")
	write(t, filepath.Join(d, "NOEXT"), "hello")
	write(t, filepath.Join(d, "fake.jpg"), "this is text, not a jpeg")
	write(t, filepath.Join(d, "ñandú_日本.pdf"), "%PDF-1.4")
	write(t, filepath.Join(d, "A.txt"), "x")
	write(t, filepath.Join(d, "sub", "nested.txt"), "n")

	res, err := Scan(context.Background(), d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := names(res)
	want := []string{"A.txt", "b.unknownext", "fake.jpg", "NOEXT", "ñandú_日本.pdf"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	for _, f := range res.Files {
		switch f.Name {
		case "fake.jpg":
			if f.MIMEType == "image/jpeg" {
				t.Errorf("misleading extension trusted: %s", f.MIMEType)
			}
		case "NOEXT":
			if f.Ext != "" || f.MIMEType == "" {
				t.Errorf("bad no-ext: %+v", f)
			}
		case "ñandú_日本.pdf":
			if f.MIMEType != "application/pdf" {
				t.Errorf("pdf mime %s", f.MIMEType)
			}
		}
	}
}

func TestScanRecursive(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "a.txt"), "x")
	write(t, filepath.Join(d, "sub", "b.txt"), "y")
	res, err := Scan(context.Background(), d, Options{Recursive: true})
	if err != nil || len(res.Files) != 2 {
		t.Fatalf("err=%v files=%v", err, names(res))
	}
}

func TestScanSkipsSymlinks(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "real.txt"), "x")
	if err := os.Symlink(filepath.Join(d, "real.txt"), filepath.Join(d, "link.txt")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(d, filepath.Join(d, "loop")); err != nil {
		t.Skip(err)
	}
	res, err := Scan(context.Background(), d, Options{Recursive: true})
	if err != nil || len(res.Files) != 1 {
		t.Fatalf("err=%v files=%v", err, names(res))
	}
}

func TestScanUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	d := t.TempDir()
	write(t, filepath.Join(d, "ok.txt"), "x")
	locked := filepath.Join(d, "locked")
	write(t, filepath.Join(locked, "hidden.txt"), "y")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o755)
	res, err := Scan(context.Background(), d, Options{Recursive: true})
	if err != nil || len(res.Files) != 1 || len(res.Skipped) != 1 {
		t.Fatalf("err=%v files=%v skipped=%v", err, names(res), res.Skipped)
	}
}

func TestScanDoesNotModifySources(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.txt")
	write(t, p, "content")
	before, _ := os.Stat(p)
	if _, err := Scan(context.Background(), d, Options{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	if string(b) != "content" || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("source modified")
	}
}

func TestScanCancelAndMissing(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "a.txt"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, d, Options{}); err == nil {
		t.Error("expected cancel error")
	}
	if _, err := Scan(context.Background(), filepath.Join(d, "nope"), Options{}); err == nil {
		t.Error("expected missing dir error")
	}
}

func TestScanSymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	write(t, filepath.Join(real, "a.txt"), "a")
	write(t, filepath.Join(real, "sub", "b.txt"), "b")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	res, err := Scan(context.Background(), link, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Name != "a.txt" {
		t.Fatalf("%v", names(res))
	}
	// paths keep the root as given, which is what SyncFiles uses as its prefix
	if want := filepath.Join(link, "a.txt"); res.Files[0].Path != want {
		t.Fatalf("path %q, want %q", res.Files[0].Path, want)
	}
	rec, err := Scan(context.Background(), link, Options{Recursive: true})
	if err != nil || len(rec.Files) != 2 {
		t.Fatalf("%v %v", names(rec), err)
	}
	for _, f := range rec.Files {
		if !strings.HasPrefix(f.Path, link+string(filepath.Separator)) {
			t.Fatalf("path %q lost the link prefix", f.Path)
		}
	}
}

func TestScanDetectsMIMEForManyFilesAndHonoursCancel(t *testing.T) {
	d := t.TempDir()
	for i := 0; i < 50; i++ {
		write(t, filepath.Join(d, fmt.Sprintf("f%02d.txt", i)), "hello text")
	}
	res, err := Scan(context.Background(), d, Options{})
	if err != nil || len(res.Files) != 50 {
		t.Fatalf("%d files, %v", len(res.Files), err)
	}
	for _, f := range res.Files {
		if f.MIMEType == "" {
			t.Fatalf("%s: MIME not detected", f.Name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, d, Options{}); err == nil {
		t.Fatal("a cancelled scan must return an error")
	}
}

func TestScanSkipsCopierTempFilesAndHonoursFilters(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a.txt", ".hidden.txt", ".sortie-123.tmp", "movie.part", "x.crdownload", ".git/config", "sub/b.txt"} {
		write(t, filepath.Join(d, f), "x")
	}
	list := func(o Options) string {
		res, err := Scan(context.Background(), d, o)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(names(res), ",")
	}
	// the app's own leftovers are never listed; everything else is by default
	if got := list(Options{Recursive: true}); strings.Contains(got, ".sortie-") || !strings.Contains(got, ".hidden.txt") || !strings.Contains(got, "movie.part") {
		t.Errorf("default scan: %s", got)
	}
	got := list(Options{Recursive: true, SkipHidden: true, Exclude: []string{"*.part", "*.crdownload"}})
	if got != "a.txt,b.txt" {
		t.Errorf("filtered scan: %q, want a.txt,b.txt (no dotfiles, .git/, partial downloads)", got)
	}
}
