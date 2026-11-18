package fileinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMIME(t *testing.T) {
	d := t.TempDir()
	cases := map[string]struct{ content, want string }{
		"a.png":     {"\x89PNG\r\n\x1a\n0000", "image/png"},
		"b.txt":     {"hello", "text/plain"},
		"c.weird":   {"\x00\x01\x02\x03", "application/octet-stream"},
		"empty.pdf": {"", "application/pdf"},
		"liar.jpg":  {"\x89PNG\r\n\x1a\n0000", "image/png"},
	}
	for name, c := range cases {
		p := filepath.Join(d, name)
		os.WriteFile(p, []byte(c.content), 0o644)
		if got := DetectMIME(p); got != c.want {
			t.Errorf("%s: got %s want %s", name, got, c.want)
		}
	}
	if got := DetectMIME(filepath.Join(d, "missing")); got != "application/octet-stream" {
		t.Errorf("missing: %s", got)
	}
}

func TestExtension(t *testing.T) {
	for name, want := range map[string]string{
		"photo.JPG":        ".jpg",
		"archive.tar.gz":   ".gz",
		"noext":            "",
		".bashrc":          "",
		".gitignore":       "",
		"/home/u/.profile": "",
		"/dir.d/noext":     "",
		".hidden.txt":      ".txt",
		"trailing.":        ".",
	} {
		if got := Extension(name); got != want {
			t.Errorf("Extension(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDetectMIMEOfEmptyFileFallsBackToExtension(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "empty.png")
	os.WriteFile(p, nil, 0o644)
	if got := DetectMIME(p); got != "image/png" {
		t.Fatalf("%q", got)
	}
	q := filepath.Join(d, "empty.unknownext")
	os.WriteFile(q, nil, 0o644)
	if got := DetectMIME(q); got != "application/octet-stream" {
		t.Fatalf("%q", got)
	}
}
